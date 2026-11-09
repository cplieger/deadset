package schema

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/suppress"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/documents"
	"github.com/cplieger/deadset/v2/internal/schema/proto"
)

const suppressionSubject = "suppression"

// suppress is schemas.21: the inline directives of the schema files, the
// ignore file's entries and the baseline's rows, decided against the findings
// the records would carry; a record they withhold, or one a suppression of a
// declaration kind marks under any code, is published live (suppression.md
// "Staleness").
func (r *reading) suppress(ctx context.Context) error {
	var records []suppress.Record
	var refusals []suppress.Refusal
	for _, f := range r.files {
		if f.unit == nil {
			continue
		}
		for _, c := range f.unit.File.LineComments {
			held, refused, err := directive(f, c)
			if err != nil {
				return err
			}
			records, refusals = append(records, held...), append(refusals, refused...)
		}
	}
	entries, refused, err := r.entries(ctx)
	if err != nil {
		return err
	}
	records, refusals = append(records, entries...), append(refusals, refused...)
	ledger, would := r.decide(records)
	r.inEffect, r.reasons = suppress.Counts(records, ledger)
	stale := slices.DeleteFunc(suppress.StaleRecords(records, ledger), func(s suppress.Record) bool {
		_, unclean := r.unclean[s.Path]
		return unclean
	})
	r.selfChecks(refusals, stale, would)
	return nil
}

// entries binds the ignore file's entries and the baseline's rows of the
// proto:// scheme.
func (r *reading) entries(ctx context.Context) ([]suppress.Record, []suppress.Refusal, error) {
	binder := &suppress.Binder{Scheme: "proto://", Lookup: r.lookup, Holder: func(string) (string, bool) { return "", false }}
	var records []suppress.Record
	var refusals []suppress.Refusal
	for _, doc := range []struct {
		read      func([]byte) ([]suppress.Entry, error)
		name      string
		mechanism suppress.Mechanism
	}{
		{name: suppress.IgnoreFileName, read: suppress.ReadIgnore, mechanism: suppress.Ignore},
		{name: suppress.BaselineFileName, read: suppress.ReadBaseline, mechanism: suppress.Baseline},
	} {
		data, err := documents.Read(ctx, r.in.Target, doc.name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("schema: %w", err)
		}
		entries, err := doc.read(data)
		if err != nil {
			return nil, nil, err
		}
		bound, refused, err := binder.Bind(entries, doc.mechanism)
		if err != nil {
			return nil, nil, err
		}
		records, refusals = append(records, bound...), append(refusals, refused...)
	}
	return records, refusals, nil
}

// decide decides the records against every record's pending finding, would,
// and holds each record whose finding they withhold or that a record marks.
func (r *reading) decide(records []suppress.Record) (*suppress.Ledger, []suppress.Finding) {
	var would []suppress.Finding
	var held []*decl
	byKey := map[string]*decl{}
	for _, d := range r.decls {
		if d.held == "" {
			would = append(would, suppress.Finding{Code: codeOf[d.kind], SymbolKind: d.kind, Ref: d.ref, Path: d.file.rel, File: d.file.rel, Decl: key(d), ID: key(d)})
			held = append(held, d)
			byKey[key(d)] = d
		}
	}
	ledger := suppress.Decide(records, would, &suppress.Dials{
		Allows: func(code vocab.Code) bool { return r.in.Settings.severity(code) == vocab.Allow },
		Withholds: func(f *suppress.Finding) bool {
			return r.in.Settings.severity(f.Code) == vocab.Allow || r.in.Settings.below(r.byRef[f.Ref].class)
		},
	})
	for i := range would {
		if ledger.Withheld(&would[i]) {
			held[i].hold("suppressed")
		}
	}
	for i := range records {
		if d := byKey[records[i].Bound]; d != nil && records[i].MarksLive() {
			d.hold("suppressed")
		}
	}
	return ledger, would
}

// directive reads one // comment: no record where it is no directive, one
// per code and declaration it binds, and a refusal per code where it carries
// no reason.
func directive(f *schemaFile, c proto.LineComment) ([]suppress.Record, []suppress.Refusal, error) {
	read := suppress.ReadDirective(c.Text)
	if read.Verdict == suppress.NotADirective {
		return nil, nil, nil
	}
	site := suppress.Site{Path: f.rel, File: f.rel, Line: int(c.Line) + 1, Column: utf16Column(f.src, int(c.Line), int(c.Column))}
	if read.Verdict == suppress.Malformed {
		return nil, nil, suppress.MalformedDirective(c.Text, site)
	}
	line := site.Line + 1
	if !c.Alone {
		line = site.Line
	}
	bound := slices.DeleteFunc(slices.Clone(f.decls), func(d *decl) bool { return d.line != line })
	var records []suppress.Record
	var refusals []suppress.Refusal
	for _, code := range read.Codes {
		if read.Verdict == suppress.Reasonless {
			refusals = append(refusals, suppress.Refusal{Reported: vocab.SuppressionWithoutReason, Code: code, Path: f.rel, Mechanism: suppress.Inline, Site: site})
			continue
		}
		one := suppress.Record{Code: code, Path: f.rel, Reason: read.Reason, Mechanism: suppress.Inline, Site: site}
		if len(bound) == 0 {
			records = append(records, one)
		}
		for _, d := range bound {
			one.Symbol, one.Bound = d.ref, key(d)
			records = append(records, one)
		}
	}
	return records, refusals, nil
}

func key(d *decl) string { return fmt.Sprintf("%s:%d:%d", d.file.rel, d.line, d.column) }

func (r *reading) lookup(symbol, path string) []string {
	if d := r.byRef[symbol]; d != nil && d.file.rel == path {
		return []string{key(d)}
	}
	return nil
}

// selfChecks writes the findings suppression.md reports about suppressions:
// a refused one, and one stale suppression per site, naming every code.
func (r *reading) selfChecks(refusals []suppress.Refusal, stale []suppress.Record, would []suppress.Finding) {
	for i := range refusals {
		refused := &refusals[i]
		what := "carries no reason"
		if refused.Reported == vocab.UnscopedIgnoreEntry {
			what = "names a symbol and no path"
		}
		r.checks = append(r.checks, r.selfCheck(refused.Reported, refused.Symbol, refused.Path, refused.Site,
			refused.Mechanism.Written()+" for "+string(refused.Code)+" "+what, refused.Mechanism,
			&report.Entry{Code: refused.Code, Symbol: refused.Symbol, Path: refused.Path, Reason: refused.Reason}))
	}
	for _, held := range suppress.StaleSites(stale) {
		first, site := held[0], held[0].Site
		r.stale = append(r.stale, report.StaleSuppression{
			Code:      vocab.StaleSuppression,
			Mechanism: string(first.Mechanism),
			Entry:     report.SuppressionEntry{Code: first.Code, Symbol: first.Symbol, Path: first.Path, Reason: first.Reason},
			Position:  report.SuppressionPosition{Path: site.Path, Line: max(site.Line, 1), Column: max(site.Column, 1)},
			Symbol:    cmp.Or(first.Symbol, r.fileRef(first.Path)),
			Message:   suppress.StaleMessage(held, would),
		})
	}
}

// selfCheck is one finding about a suppression, at its own site, its
// component numbered by mint.
func (r *reading) selfCheck(code vocab.Code, symbol, at string, site suppress.Site, message string, m suppress.Mechanism, entry *report.Entry) report.Finding {
	kind, _ := vocab.KindOf(code)
	ref := symbol
	if ref == "" {
		ref = r.fileRef(at)
	}
	line := max(site.Line, 1)
	return report.Finding{
		Code: code, Kind: kind.Name, Language: vocab.Proto,
		Position:          report.Position{Path: site.Path, Line: line, Column: max(site.Column, 1), EndLine: line},
		Symbol:            report.Symbol{Ref: ref, Kind: suppressionSubject, Name: ref, SizeLines: 1},
		ReachabilityClass: kind.MaxClass, Confidence: kind.MaxClass,
		Component:  report.Component{Root: true, SymbolCount: 1, DeletableLines: 1},
		RetainedBy: []string{}, Configurations: []string{}, ConsumersLoaded: []string{},
		Fixability: kind.Fixability, Severity: r.in.Settings.severity(code), Message: message,
		Details: report.Details{Mechanism: string(m), Entry: entry},
	}
}

// fileRef is symbol-ref.md's schema file reference of a target-relative path,
// the path itself where no schema file is there.
func (r *reading) fileRef(rel string) string {
	for _, f := range r.files {
		if f.rel == rel && f.unit != nil {
			return scopeOf(f.unit.File.Package) + f.importPath + ":file"
		}
	}
	return rel
}

// withhold is schemas.4's file not read cleanly, schemas.20 and the
// severity dial: a record whose finding the configuration withholds is
// published live, the confidence it lies below counted in the report's
// withheld totals, and every record is published live where a schema file
// of the target is not read cleanly.
func (r *reading) withhold() {
	for _, d := range r.decls {
		switch {
		case d.held != "":
		case len(r.unclean) > 0:
			d.hold("unclean-file")
		case r.in.Settings.severity(codeOf[d.kind]) == vocab.Allow:
			d.hold("severity-allow")
		case r.in.Settings.below(d.class):
			d.hold("withheld")
			switch d.class {
			case vocab.Certain:
				r.withheld.Certain++
			case vocab.Probable:
				r.withheld.Probable++
			case vocab.Possible:
				r.withheld.Possible++
			}
		}
	}
}
