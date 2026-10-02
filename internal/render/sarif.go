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
	sarifRootComment = "The target root, the directory the analyzer was run on."
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

// ErrNotMerged reports a report that names no input report, which the merged
// rendering has no run for.
var ErrNotMerged = errors.New("render: the report is not a merged report")

// Sources is what the SARIF rendering reads beside the merged report.
type Sources struct {
	// Read returns the content of the file at a target-relative path, which
	// the line fingerprint of every result hashes.
	Read func(path string) ([]byte, error)

	// Analyzers is the analyzer member of every report the merge read. A
	// merged report names each input's name and version but not the languages
	// its run is categorized and its rules selected by.
	Analyzers []report.Analyzer
}

// SARIF writes r, a merged report, as one SARIF 2.1.0 log: one run per report
// r was merged from, in the order r names them, holding the findings and then
// the stale suppressions that report carried, and one run under r's own
// analyzer for the findings the merge emitted itself. A suppressed finding has
// no record in r and so no result. A file Read cannot return, or a line it
// does not hold, fails the rendering, because a fingerprint computed from other
// bytes opens a second alert for the same finding.
func SARIF(w io.Writer, r *report.Report, src *Sources) error {
	if len(r.MergedFrom) == 0 {
		return ErrNotMerged
	}
	hashes := &lineHashCache{read: src.Read, held: make(map[string][]string)}
	runs := make([]sarifRun, 0, len(r.MergedFrom)+1)
	carried := make(map[string]bool, len(r.MergedFrom))
	for _, from := range r.MergedFrom {
		analyzer := slices.IndexFunc(src.Analyzers, func(a report.Analyzer) bool {
			return a.Name == from.Name && a.Version == from.Version
		})
		if analyzer < 0 {
			return fmt.Errorf("render: no input report is %s %s, which the merged report names", from.Name, from.Version)
		}
		languages := slices.Sorted(slices.Values(src.Analyzers[analyzer].Languages))
		run, err := newRun(from.Name, from.Version, automationPrefix+strings.Join(languages, "+")+"/", languages, r)
		if err != nil {
			return err
		}
		if err := run.add(r, from.Name, hashes); err != nil {
			return err
		}
		runs = append(runs, run.sarifRun)
		carried[from.Name] = true
	}
	if slices.ContainsFunc(r.Findings, func(f report.Finding) bool { return f.Analyzer == "" }) {
		run, err := newRun(r.Analyzer.Name, r.Analyzer.Version, mergeAutomation, nil, r)
		if err != nil {
			return err
		}
		if err := run.add(r, "", hashes); err != nil {
			return err
		}
		runs = append(runs, run.sarifRun)
		carried[""] = true
	}
	if err := everyRecordRendered(r, carried); err != nil {
		return err
	}
	return encode(w, &sarifLog{Schema: sarifSchema, Version: sarifVersion, Runs: runs})
}

// everyRecordRendered refuses a record that names an analyzer the merged
// report was not merged from, which no run holds.
func everyRecordRendered(r *report.Report, carried map[string]bool) error {
	for i := range r.Findings {
		if !carried[r.Findings[i].Analyzer] {
			return fmt.Errorf("render: a finding names the analyzer %q, which the merged report was not merged from", r.Findings[i].Analyzer)
		}
	}
	for i := range r.StaleSuppressions {
		if !carried[r.StaleSuppressions[i].Analyzer] {
			return fmt.Errorf("render: a stale suppression names the analyzer %q, which the merged report was not merged from",
				r.StaleSuppressions[i].Analyzer)
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
// (every live kind when languages is nil) and r's totals.
func newRun(name, version, automation string, languages []string, r *report.Report) (*run, error) {
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
		Properties: sarifRunProperties{Totals: r.Totals},
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
		if member := &found.Component.Members[i]; member.Ref != found.Symbol.Ref {
			add(labelMember, &member.Position)
		}
	}
	return held
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
