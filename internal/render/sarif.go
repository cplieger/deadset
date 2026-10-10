// Package render writes the merged report in the two formats that are
// documents of their own rather than lines of the run's output: a SARIF 2.1.0
// log for upload to code scanning, and the rendering of a template the user
// supplies. Both read the report as the run presents it, so they hold the
// findings the text lines and the JSON report hold, in the same order.
package render

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"

	"github.com/cplieger/deadset/internal/report"
)

// The document's fixed values: the schema it names, the format version, the
// unit a column counts, the base every location resolves against and its
// description, and the automation identifiers.
const (
	sarifSchema      = "https://docs.oasis-open.org/sarif/sarif/v2.1.0/errata01/os/schemas/sarif-schema-2.1.0.json"
	sarifVersion     = "2.1.0"
	sarifColumnKind  = "utf16CodeUnits"
	sarifURIBaseID   = "%SRCROOT%"
	sarifRootComment = "The target root."
	automationPrefix = "deadset/"
	mergeAutomation  = "deadset/merge/"
)

// The labels of the related locations, one per list a finding names positions
// in, and the most related locations one result carries.
const (
	labelImplementation = "implementation"
	labelWrite          = "write"
	labelMember         = "member"

	maxRelatedLocations = 100
)

// errUnplaced reports a merged record whose analyzer member names no report
// the merged report was merged from, which no run of the log holds.
var errUnplaced = errors.New("render: a record names an analyzer the merged report was not merged from")

// Sources is what the SARIF rendering reads beside the report.
type Sources struct {
	// Read returns the content of the file at a target-relative path, which
	// the line fingerprint of every result hashes.
	Read func(path string) ([]byte, error)

	// Inputs is every report a merged report was merged from. A merged report
	// names each input's name and version but not the languages its run is
	// categorized and its rules selected by, nor the totals its run carries.
	Inputs []*report.Report
}

// SARIF writes r as one SARIF 2.1.0 log. An analyzer's own report is one run.
// A merged report is one run per report it was merged from, in bytewise order
// of name, holding the findings and then the stale suppressions that report
// carried, then one run under r's own analyzer for the records that name no
// analyzer, and the log carries r's totals and their withheld line. A record no
// run holds, a file Read cannot return, and a line the file does not hold each
// fail the rendering, the last two because a fingerprint computed from other
// bytes opens a second alert for the same finding.
func SARIF(w io.Writer, r *report.Report, src *Sources) error {
	hashes := &lineHashCache{read: src.Read, held: make(map[string][]string)}
	if len(r.MergedFrom) == 0 {
		run, err := newRun(r.Analyzer.Name, r.Analyzer.Version, automationOf(r.Analyzer.Languages), r.Analyzer.Languages, &r.Totals)
		if err != nil {
			return err
		}
		if err := run.add(r, "", hashes); err != nil {
			return err
		}
		return encode(w, &sarifLog{Schema: sarifSchema, Version: sarifVersion, Runs: []sarifRun{run.sarifRun}})
	}
	runs, err := mergedRuns(r, src.Inputs, hashes)
	if err != nil {
		return err
	}
	return encode(w, &sarifLog{
		Schema: sarifSchema, Version: sarifVersion, Runs: runs,
		Properties: &sarifLogProperties{Totals: r.Totals, Withheld: r.Totals.Withheld.Line()},
	})
}

// mergedRuns is the runs of a merged report: one per report it was merged
// from, by name, each with that input's languages and totals, then the
// merge's own when a record names no analyzer.
func mergedRuns(r *report.Report, inputs []*report.Report, hashes *lineHashCache) ([]sarifRun, error) {
	if err := everyRecordPlaced(r); err != nil {
		return nil, err
	}
	from := slices.SortedFunc(slices.Values(r.MergedFrom), func(a, b report.InputReport) int {
		return strings.Compare(a.Name, b.Name)
	})
	runs := make([]sarifRun, 0, len(from)+1)
	for i := range from {
		at := slices.IndexFunc(inputs, func(input *report.Report) bool { return input.Analyzer.Name == from[i].Name })
		if at < 0 {
			return nil, fmt.Errorf("render: no input report is %s, which the merged report names", from[i].Name)
		}
		input := inputs[at]
		run, err := newRun(from[i].Name, from[i].Version, automationOf(input.Analyzer.Languages), input.Analyzer.Languages, &input.Totals)
		if err != nil {
			return nil, err
		}
		if err := run.add(r, from[i].Name, hashes); err != nil {
			return nil, err
		}
		runs = append(runs, run.sarifRun)
	}
	if slices.ContainsFunc(r.Findings, func(f report.Finding) bool { return f.Analyzer == "" }) ||
		slices.ContainsFunc(r.StaleSuppressions, func(s report.StaleSuppression) bool { return s.Analyzer == "" }) {
		run, err := newRun(r.Analyzer.Name, r.Analyzer.Version, mergeAutomation, nil, &r.Totals)
		if err != nil {
			return nil, err
		}
		if err := run.add(r, "", hashes); err != nil {
			return nil, err
		}
		runs = append(runs, run.sarifRun)
	}
	return runs, nil
}

// automationOf is the automation identifier of a run over languages: their
// names in bytewise order, joined by a plus sign.
func automationOf(languages []string) string {
	return automationPrefix + strings.Join(slices.Sorted(slices.Values(languages)), "+") + "/"
}

// everyRecordPlaced refuses a record whose analyzer member names no report
// the merged report was merged from.
func everyRecordPlaced(r *report.Report) error {
	placed := func(analyzer string) bool {
		return analyzer == "" || slices.ContainsFunc(r.MergedFrom, func(m report.InputReport) bool { return m.Name == analyzer })
	}
	for i := range r.Findings {
		if !placed(r.Findings[i].Analyzer) {
			return fmt.Errorf("%w: a finding names %q", errUnplaced, r.Findings[i].Analyzer)
		}
	}
	for i := range r.StaleSuppressions {
		if !placed(r.StaleSuppressions[i].Analyzer) {
			return fmt.Errorf("%w: a stale suppression names %q", errUnplaced, r.StaleSuppressions[i].Analyzer)
		}
	}
	return nil
}

// encode writes the log as indented JSON with one trailing newline.
func encode(w io.Writer, log *sarifLog) error {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(log); err != nil {
		return fmt.Errorf("render: encode the SARIF log: %w", err)
	}
	if _, err := w.Write(encoded.Bytes()); err != nil {
		return fmt.Errorf("render: write the SARIF log: %w", err)
	}
	return nil
}

// run is one run being built, with the index of each rule's code.
type run struct {
	index map[string]int
	sarifRun
}

// newRun is an empty run under the named driver, with the rules of languages
// (every live kind when languages is nil) and the totals of the report the
// driver wrote, with their withheld line.
func newRun(name, version, automation string, languages []string, totals *report.Totals) (*run, error) {
	rules, err := rulesFor(languages)
	if err != nil {
		return nil, err
	}
	index := make(map[string]int, len(rules))
	for at := range rules {
		index[rules[at].ID] = at
	}
	return &run{
		index:             index,
		Tool:              sarifTool{Driver: sarifDriver{Name: name, Version: version, SemanticVersion: version, Rules: rules}},
		AutomationDetails: sarifAutomationDetails{ID: automation},
		ColumnKind:        sarifColumnKind,
		OriginalURIBaseIDs: map[string]sarifURIBase{
			sarifURIBaseID: {Description: sarifMessage{Text: sarifRootComment}},
		},
		Results:    []sarifResult{},
		Properties: sarifRunProperties{Totals: *totals, Withheld: totals.Withheld.Line()},
	}, nil
}

// add appends a result for every finding and then every stale suppression of
// r that the named analyzer carried, in r's order.
func (u *run) add(r *report.Report, analyzer string, hashes *lineHashCache) error {
	for i := range r.Findings {
		if r.Findings[i].Analyzer != analyzer {
			continue
		}
		result, err := u.findingResult(&r.Findings[i], hashes)
		if err != nil {
			return err
		}
		u.Results = append(u.Results, result)
	}
	for i := range r.StaleSuppressions {
		if r.StaleSuppressions[i].Analyzer != analyzer {
			continue
		}
		result, err := u.staleResult(&r.StaleSuppressions[i], hashes)
		if err != nil {
			return err
		}
		u.Results = append(u.Results, result)
	}
	return nil
}

// ruleIndex is the index of code among the run's rules.
func (u *run) ruleIndex(code string) (int, error) {
	at, held := u.index[code]
	if !held {
		return 0, fmt.Errorf("render: the run of %s has no rule %s, which one of its records names", u.Tool.Driver.Name, code)
	}
	return at, nil
}

// findingResult is one finding as a result: its rule, the level its severity
// maps to, its message linking every related location, its own location, the
// positions it names beyond its own, both fingerprints and the members the
// mapping places nowhere else.
func (u *run) findingResult(found *report.Finding, hashes *lineHashCache) (sarifResult, error) {
	at, err := u.ruleIndex(found.Code)
	if err != nil {
		return sarifResult{}, err
	}
	hash, err := hashes.at(found.Position.Path, found.Position.Line)
	if err != nil {
		return sarifResult{}, err
	}
	related := relatedLocations(found)
	return sarifResult{
		RuleID:    found.Code,
		RuleIndex: at,
		Level:     levelOf(found.Severity),
		Message:   sarifMessage{Text: messageWithLinks(found.Message, related)},
		Locations: []sarifLocation{{PhysicalLocation: physicalLocation(&found.Position)}},

		RelatedLocations: related,
		PartialFingerprints: sarifFingerprints{
			PrimaryLocationLineHash: hash,
			DeadsetSymbolRef:        symbolFingerprint(found.Code, found.Symbol.Ref),
		},
		Properties: &sarifResultProperties{
			Language:          found.Language,
			Symbol:            found.Symbol,
			ReachabilityClass: found.ReachabilityClass,
			Confidence:        found.Confidence,
			LivenessRelation:  found.LivenessRelation,
			TestOnly:          found.TestOnly,
			Generated:         found.Generated,
			Component:         found.Component,
			RetainedBy:        listed(found.RetainedBy),
			Configurations:    listed(found.Configurations),
			ConsumersLoaded:   listed(found.ConsumersLoaded),
			Fixability:        found.Fixability,
			Details:           found.Details,
		},
	}, nil
}

// staleResult is one stale suppression as a result at the failing level,
// located at the suppression's own site. A stale-suppression record is not a
// finding, so the result carries no member bag.
func (u *run) staleResult(stale *report.StaleSuppression, hashes *lineHashCache) (sarifResult, error) {
	at, err := u.ruleIndex(stale.Code)
	if err != nil {
		return sarifResult{}, err
	}
	hash, err := hashes.at(stale.Position.Path, stale.Position.Line)
	if err != nil {
		return sarifResult{}, err
	}
	site := report.Position{
		Path: stale.Position.Path, Line: stale.Position.Line,
		Column: stale.Position.Column, EndLine: stale.Position.Line,
	}
	return sarifResult{
		RuleID:    stale.Code,
		RuleIndex: at,
		Level:     levelError,
		Message:   sarifMessage{Text: stale.Message},
		Locations: []sarifLocation{{PhysicalLocation: physicalLocation(&site)}},
		PartialFingerprints: sarifFingerprints{
			PrimaryLocationLineHash: hash,
			DeadsetSymbolRef:        symbolFingerprint(stale.Code, stale.Symbol),
		},
	}, nil
}

// listed is items, or an empty list where items is nil.
func listed(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

// relatedLocations is every position a finding names beyond its own, numbered
// from one: its implementations, then its write positions, then the other
// members of its component where it lists them, the first hundred of these.
func relatedLocations(found *report.Finding) []sarifLocation {
	var held []sarifLocation
	add := func(label string, at *report.Position) {
		if len(held) < maxRelatedLocations {
			held = append(held, sarifLocation{
				ID:               len(held) + 1,
				PhysicalLocation: physicalLocation(at),
				Message:          &sarifMessage{Text: label},
				path:             at.Path,
			})
		}
	}
	for i := range found.Details.Implementations {
		add(labelImplementation, &found.Details.Implementations[i].Position)
	}
	for i := range found.Details.WritePositions {
		add(labelWrite, &found.Details.WritePositions[i])
	}
	for i := range found.Component.Members {
		if member := &found.Component.Members[i]; !ownDeclaration(found, member) {
			add(labelMember, &member.Position)
		}
	}
	return held
}

// ownDeclaration reports whether member is the declaration found is about: one
// reference can spell a declaration in each of two packages of one name, so
// the path tells them apart.
func ownDeclaration(found *report.Finding, member *report.Positioned) bool {
	return member.Ref == found.Symbol.Ref && member.Position.Path == found.Position.Path
}

// messageWithLinks is a result's message: the finding's message, and where
// the result has related locations, a link to each, because a consumer shows
// a related location only where the message links to it.
func messageWithLinks(message string, related []sarifLocation) string {
	if len(related) == 0 {
		return message
	}
	links := make([]string, len(related))
	for i := range related {
		at := &related[i]
		links[i] = fmt.Sprintf("[%s %s:%d:%d](%d)", at.Message.Text, at.path,
			at.PhysicalLocation.Region.StartLine, at.PhysicalLocation.Region.StartColumn, at.ID)
	}
	return message + " (see " + strings.Join(links, ", ") + ")"
}

// physicalLocation is one position as a location against the declared base,
// from its first character to the end of its last line.
func physicalLocation(at *report.Position) sarifPhysical {
	return sarifPhysical{
		ArtifactLocation: sarifArtifactLocation{URI: relativeReference(at.Path), URIBaseID: sarifURIBaseID},
		Region:           sarifRegion{StartLine: at.Line, StartColumn: at.Column, EndLine: at.EndLine},
	}
}

// relativeReference is a target-relative path as an RFC 3986 relative
// reference: each segment percent-encoded as a path segment, and a colon in
// the first segment encoded as well, where it would read as a scheme.
func relativeReference(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	segments[0] = strings.ReplaceAll(segments[0], ":", "%3A")
	return strings.Join(segments, "/")
}

// lineHashCache is the line fingerprints of the files one rendering reads,
// each file hashed once.
type lineHashCache struct {
	read func(path string) ([]byte, error)
	held map[string][]string
}

// at is the line fingerprint of one line of one file.
func (c *lineHashCache) at(path string, line int) (string, error) {
	hashes, held := c.held[path]
	if !held {
		content, err := c.read(path)
		if err != nil {
			return "", fmt.Errorf("render: read %s for its line fingerprint: %w", path, err)
		}
		hashes = lineHashes(content)
		c.held[path] = hashes
	}
	if line < 1 || line > len(hashes) {
		return "", fmt.Errorf("render: %s holds %d lines and a record names line %d", path, len(hashes), line)
	}
	return hashes[line-1], nil
}
