// Package policy shapes the merged report a run writes and prints, once,
// after the merge returns: the configured severity of every finding the merge
// emitted itself, and the configured maximum finding count. An analyzer
// applied the severity map to its own findings and was handed no maximum, so
// both are applied here and nowhere else.
package policy

import (
	"slices"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
)

// Options is how one run shapes its merged report.
type Options struct {
	// Severity is the severity the configuration sets for an issue-kind code,
	// and false where it sets none, which leaves the finding as the merge
	// wrote it.
	Severity func(code string) (vocab.Severity, bool)

	// FailOn is the least severity that fails the run; empty fails on none.
	FailOn vocab.Severity

	// MaxFindings is the most findings the report keeps; zero keeps every one.
	MaxFindings int

	// BySize keeps the largest deletions where the maximum drops findings.
	BySize bool
}

// Apply shapes r in place and returns the subject of every finding at or
// above opts.FailOn, in kinds.json's order. A finding no input report
// carried, which the merge emitted itself, takes the severity the
// configuration sets for its code, and a severity of allow withholds it, as an
// analyzer withholds its own; the counts the findings decide are then
// recounted. The findings past the maximum, in the configured order, are then
// dropped and counted as omitted, after the subjects are read, since the exit
// code counts every finding.
func Apply(r *report.Report, opts *Options) []vocab.Subject {
	if opts.Severity != nil && restate(r, opts.Severity) {
		report.Recount(r)
	}
	failing := subjects(r, opts.FailOn)
	report.Cap(r, opts.MaxFindings, opts.BySize)
	return failing
}

// subjects is the subject of every finding of r at or above failOn, in
// kinds.json's order.
func subjects(r *report.Report, failOn vocab.Severity) []vocab.Subject {
	order := vocab.Severities()
	at := slices.Index(order, failOn)
	if at < 0 {
		return nil
	}
	held := map[vocab.Subject]bool{}
	for i := range r.Findings {
		if slices.Contains(order[at:], r.Findings[i].Severity) {
			held[r.Findings[i].Code.Subject()] = true
		}
	}
	return slices.DeleteFunc(vocab.Subjects(), func(s vocab.Subject) bool { return !held[s] })
}

// restate gives every finding the merge emitted the severity configured for
// its code, withholding one configured allow, and reports whether any finding
// changed.
func restate(r *report.Report, configured func(code string) (vocab.Severity, bool)) bool {
	changed := false
	kept := r.Findings[:0:0]
	for i := range r.Findings {
		found := r.Findings[i]
		if severity, set := configured(string(found.Code)); set && found.Analyzer == "" {
			changed = changed || severity != found.Severity || severity == vocab.Allow
			if severity == vocab.Allow {
				continue
			}
			found.Severity = severity
		}
		kept = append(kept, found)
	}
	r.Findings = kept
	return changed
}
