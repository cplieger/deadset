// Package present shapes the merged report a run writes and prints, once,
// after the merge returns: the configured severity of every finding the merge
// emitted itself, the configured order of the findings, and the configured
// maximum finding count. An analyzer applied the severity map to its own
// findings and was handed no maximum, so each of the three is applied here and
// nowhere else.
package present

import (
	"cmp"
	"slices"

	"github.com/cplieger/deadset/internal/config"
	"github.com/cplieger/deadset/internal/merge"
	"github.com/cplieger/deadset/internal/report"
)

// Options is how one run shapes its merged report.
type Options struct {
	// Severity is the severity the configuration sets for an issue-kind code,
	// and false where it sets none, which leaves the finding as the merge
	// wrote it.
	Severity func(code string) (config.Severity, bool)

	Sort config.Sort

	// MaxFindings is the most findings the report keeps; zero keeps every one.
	MaxFindings int
}

// Apply shapes r in place. A finding no input report carried, which the merge
// emitted itself, takes the severity the configuration sets for its code, and
// a severity of allow withholds it, as an analyzer withholds its own; the
// counts the findings decide are then recounted. The findings are then
// ordered, by size where the configuration says so and otherwise in the
// canonical order the merge left them in, and bounded: the findings past the
// maximum are dropped and counted as omitted, and every other count keeps
// describing the whole set.
func Apply(r *report.Report, opts *Options) {
	if opts.Severity != nil && restate(r, opts.Severity) {
		merge.Recount(r)
	}
	if opts.Sort == config.SortSize {
		slices.SortStableFunc(r.Findings, func(a, b report.Finding) int {
			return cmp.Or(
				cmp.Compare(b.Component.DeletableLines, a.Component.DeletableLines),
				cmp.Compare(b.Symbol.SizeLines, a.Symbol.SizeLines),
			)
		})
	}
	if opts.MaxFindings > 0 && len(r.Findings) > opts.MaxFindings {
		r.Totals.Omitted += len(r.Findings) - opts.MaxFindings
		r.Findings = r.Findings[:opts.MaxFindings]
	}
}

// restate gives every finding the merge emitted the severity configured for
// its code, withholding one configured allow, and reports whether any finding
// changed.
func restate(r *report.Report, configured func(code string) (config.Severity, bool)) bool {
	changed := false
	kept := r.Findings[:0:0]
	for i := range r.Findings {
		found := r.Findings[i]
		if severity, set := configured(found.Code); set && found.Analyzer == "" {
			changed = changed || report.Severity(severity) != found.Severity || severity == config.Allow
			if severity == config.Allow {
				continue
			}
			found.Severity = report.Severity(severity)
		}
		kept = append(kept, found)
	}
	r.Findings = kept
	return changed
}
