package merge

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cplieger/deadset/internal/report"
)

// ErrNoInput is a merge given no report to merge.
var ErrNoInput = errors.New("merge: no input report")

// The reasons an [*AdmissionError] refuses an input report.
var (
	ErrSchemaVersion = errors.New("the report's schema version is outside the accepted range")
	ErrConformance   = errors.New("the report's analyzer has no conformance pass")
)

// AdmissionError is one input report the admission step refuses. Err is
// [ErrSchemaVersion] or [ErrConformance].
type AdmissionError struct {
	Err error

	// Analyzer is the refused report's analyzer name.
	Analyzer string

	// SchemaVersion is the schema version the refused report names, and
	// Accepted the versions the merge admits.
	SchemaVersion string

	// Result is the refused report's conformance result.
	Result report.Result

	Accepted []string
}

// Error names the analyzer and, for a schema version, both versions.
func (e *AdmissionError) Error() string {
	if errors.Is(e.Err, ErrSchemaVersion) {
		return fmt.Sprintf("merge: %s writes schema version %s, and the merge accepts %s",
			e.Analyzer, e.SchemaVersion, strings.Join(e.Accepted, ", "))
	}
	return fmt.Sprintf("merge: %s has no conformance pass: its conformance result is %q", e.Analyzer, e.Result)
}

// Unwrap is the reason for the refusal.
func (e *AdmissionError) Unwrap() error { return e.Err }

// TargetError is two input reports naming different targets, where a merged
// report names the one target every input names.
type TargetError struct {
	Analyzers [2]string
	Targets   [2]report.Target
}

// Error names both analyzers and the target each names.
func (e *TargetError) Error() string {
	return fmt.Sprintf("merge: %s reports on target %s and %s on target %s, and a merge reads reports of one target",
		e.Analyzers[0], describeTarget(e.Targets[0]), e.Analyzers[1], describeTarget(e.Targets[1]))
}

// describeTarget is a target as an error message names it.
func describeTarget(t report.Target) string {
	return fmt.Sprintf("%s %q at %q", t.Kind, t.Identity, t.Root)
}

// admit runs the admission step over every input before any other step: a
// report whose schema version is outside accepted is refused, and otherwise a
// report whose conformance result is not a pass. With no refusal, every input
// must name one target. The refusals are in merged_from's order, so the error
// does not depend on the order of the inputs.
func admit(inputs []Input, accepted []string) error {
	ordered := slices.Clone(inputs)
	slices.SortFunc(ordered, compareInputs)
	var refused []error
	for i := range ordered {
		if err := admitOne(ordered[i].Report, accepted); err != nil {
			refused = append(refused, err)
		}
	}
	if len(refused) == 1 {
		return refused[0]
	}
	if refused != nil {
		return errors.Join(refused...)
	}
	first := ordered[0].Report
	for i := range ordered[1:] {
		other := ordered[1+i].Report
		if other.Target != first.Target {
			return &TargetError{
				Analyzers: [2]string{first.Analyzer.Name, other.Analyzer.Name},
				Targets:   [2]report.Target{first.Target, other.Target},
			}
		}
	}
	return nil
}

// admitOne is the refusal of one report, or nil. A report of a schema version
// the merge does not accept is refused on that alone, because its other
// members are of a shape the merge does not model.
func admitOne(r *report.Report, accepted []string) error {
	switch {
	case !slices.Contains(accepted, r.SchemaVersion):
		return &AdmissionError{
			Err:           ErrSchemaVersion,
			Analyzer:      r.Analyzer.Name,
			SchemaVersion: r.SchemaVersion,
			Accepted:      slices.Clone(accepted),
		}
	case r.Analyzer.Conformance.Result != report.ResultPass:
		return &AdmissionError{Err: ErrConformance, Analyzer: r.Analyzer.Name, Result: r.Analyzer.Conformance.Result}
	}
	return nil
}

// compareInputs orders two inputs as merged_from orders their entries: by
// analyzer name, then version, then digest. Two inputs equal on all three are
// one artifact run twice, and every member admission reads breaks the tie, so
// the refusal a merge names never depends on the order of its inputs.
func compareInputs(a, b Input) int {
	return cmp.Or(
		strings.Compare(a.Report.Analyzer.Name, b.Report.Analyzer.Name),
		strings.Compare(a.Report.Analyzer.Version, b.Report.Analyzer.Version),
		strings.Compare(a.Digest, b.Digest),
		strings.Compare(a.Report.SchemaVersion, b.Report.SchemaVersion),
		strings.Compare(string(a.Report.Analyzer.Conformance.Result), string(b.Report.Analyzer.Conformance.Result)),
		strings.Compare(string(a.Report.Target.Kind), string(b.Report.Target.Kind)),
		strings.Compare(a.Report.Target.Root, b.Report.Target.Root),
		strings.Compare(a.Report.Target.Identity, b.Report.Target.Identity),
	)
}
