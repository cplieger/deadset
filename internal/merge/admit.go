package merge

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/cplieger/deadset/internal/report"
)

// ErrNoInput is a merge given no report to merge.
var ErrNoInput = errors.New("merge: no input report")

// The reasons an admission refusal names, one per admission rule. An
// [*AdmissionError] wraps one of the first three, a [*TargetError] the fourth,
// an [*EntryError] one of the next two and a [*NameError] the last.
var (
	ErrSchemaVersion = errors.New("the report's schema version is outside the accepted range")
	ErrConformance   = errors.New("the report's analyzer has no conformance pass")
	ErrOmitted       = errors.New("the report omits findings its analysis produced")
	ErrTarget        = errors.New("two reports name different targets")
	ErrEntryIdentity = errors.New("two reports hold different entries under one id")
	ErrEntryState    = errors.New("two reports place one id in two states")
	ErrAnalyzerName  = errors.New("two reports carry one analyzer name")
)

// AdmissionError is one input report the admission step refuses. Err is
// [ErrSchemaVersion], [ErrConformance] or [ErrOmitted].
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

	// Omitted is how many findings the refused report's totals count as
	// omitted.
	Omitted int
}

// Error names the analyzer and what the refusal reads: for a schema version
// both versions, for a conformance block its result, and for an omission the
// count.
func (e *AdmissionError) Error() string {
	switch {
	case errors.Is(e.Err, ErrSchemaVersion):
		return fmt.Sprintf("merge: %s writes schema version %s, and the merge accepts %s",
			e.Analyzer, e.SchemaVersion, strings.Join(e.Accepted, ", "))
	case errors.Is(e.Err, ErrOmitted):
		return fmt.Sprintf("merge: %s omitted %d findings from its report, and a merge reads every finding an analysis produced",
			e.Analyzer, e.Omitted)
	default:
		return fmt.Sprintf("merge: %s has no conformance pass: its conformance result is %q", e.Analyzer, e.Result)
	}
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

// Unwrap is [ErrTarget].
func (e *TargetError) Unwrap() error { return ErrTarget }

// describeTarget is a target as an error message names it.
func describeTarget(t report.Target) string {
	return fmt.Sprintf("%s %q at %q", t.Kind, t.Identity, t.Root)
}

// EntryError is two input reports that disagree on what one identifier of the
// run names. Err is [ErrEntryIdentity] when both list the id in one array and
// the entries differ in an identity member, and [ErrEntryState] when one lists
// it as built or loaded and the other as not built or unavailable.
type EntryError struct {
	Err error

	// Analyzers names the two reports, and Arrays the array of each that
	// holds the id, each spelled as the report document names it.
	Analyzers [2]string
	Arrays    [2]string
	ID        string
}

// Error names both reports, the id and the arrays holding it.
func (e *EntryError) Error() string {
	if errors.Is(e.Err, ErrEntryState) {
		return fmt.Sprintf("merge: %s lists %q in %s and %s lists it in %s, and one id of a run is in one state",
			e.Analyzers[0], e.ID, e.Arrays[0], e.Analyzers[1], e.Arrays[1])
	}
	return fmt.Sprintf("merge: %s and %s list different entries under %q in %s, and one id names one entry of a run",
		e.Analyzers[0], e.Analyzers[1], e.ID, e.Arrays[0])
}

// Unwrap is the reason for the refusal.
func (e *EntryError) Unwrap() error { return e.Err }

// NameError is two input reports carrying one analyzer name. The name prefixes
// every component id an analyzer mints and stamps every record a merge carries
// from its report, so a merged report cannot tell the two apart, whatever their
// versions and digests.
type NameError struct {
	Name     string
	Versions [2]string
	Digests  [2]string
}

// Error names the analyzer and both versions.
func (e *NameError) Error() string {
	return fmt.Sprintf("merge: two input reports carry the analyzer name %s, at versions %s and %s, and a merge reads one report per name",
		e.Name, e.Versions[0], e.Versions[1])
}

// Unwrap is [ErrAnalyzerName].
func (e *NameError) Unwrap() error { return ErrAnalyzerName }

// admit runs the admission step over inputs in merged_from's order. Each
// report is refused on its own first: its schema version, its conformance
// result and its omitted count. With no such refusal every two reports are
// compared, and the first pair that cannot merge is refused: two targets, one
// analyzer name, two entries under one id that differ, one id in two states.
// Every refusal is in merged_from's order, and two reports of one name are
// refused on the name before their entries are compared, so the error does not
// depend on the order of the inputs.
func admit(ordered []Input, accepted []string) error {
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
	held := make([]idEntries, len(ordered))
	for i := range ordered {
		held[i] = entriesOf(ordered[i].Report)
	}
	for i := range ordered {
		for j := i + 1; j < len(ordered); j++ {
			if err := admitPair(&ordered[i], &ordered[j], &held[i], &held[j]); err != nil {
				return err
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
	case r.Totals.Omitted != 0:
		return &AdmissionError{Err: ErrOmitted, Analyzer: r.Analyzer.Name, Omitted: r.Totals.Omitted}
	}
	return nil
}

// admitPair is the refusal of two reports that cannot merge, or nil.
func admitPair(a, b *Input, aEntries, bEntries *idEntries) error {
	first, second := a.Report, b.Report
	names := [2]string{first.Analyzer.Name, second.Analyzer.Name}
	if first.Target != second.Target {
		return &TargetError{Analyzers: names, Targets: [2]report.Target{first.Target, second.Target}}
	}
	if first.Analyzer.Name == second.Analyzer.Name {
		return &NameError{
			Name:     first.Analyzer.Name,
			Versions: [2]string{first.Analyzer.Version, second.Analyzer.Version},
			Digests:  [2]string{a.Digest, b.Digest},
		}
	}
	return collide(names, aEntries, bEntries)
}

// The id-keyed arrays of a report, as admission indexes them and as a refusal
// names them.
const (
	builtArray = iota
	notBuiltArray
	loadedArray
	unavailableArray
	idArrays
)

var arrayNames = [idArrays]string{
	"configurations", "configurations_not_built", "consumers.loaded", "consumers.unavailable",
}

// pairedArray is, for each id-keyed array, the array holding the same ids in
// the other state.
var pairedArray = [idArrays]int{notBuiltArray, builtArray, unavailableArray, loadedArray}

// idEntries is every entry of a report's id-keyed arrays, by array and then by
// id, each as the compact encoding of its identity members: everything but the
// free text of a configuration's error and a consumer's reason.
type idEntries [idArrays]map[string]string

func entriesOf(r *report.Report) idEntries {
	var held idEntries
	for i := range held {
		held[i] = map[string]string{}
	}
	for i := range r.Configurations {
		held[builtArray][r.Configurations[i].ID] = compact(&r.Configurations[i])
	}
	for i := range r.ConfigurationsNotBuilt {
		dropped := &r.ConfigurationsNotBuilt[i].Configuration
		held[notBuiltArray][dropped.ID] = compact(dropped)
	}
	for i := range r.Consumers.Loaded {
		held[loadedArray][r.Consumers.Loaded[i].ID] = compact(&r.Consumers.Loaded[i])
	}
	for i := range r.Consumers.Unavailable {
		consumer := &r.Consumers.Unavailable[i]
		held[unavailableArray][consumer.ID] = compact(&report.UnavailableConsumer{ID: consumer.ID, Role: consumer.Role})
	}
	return held
}

// collide is the refusal of two reports' id-keyed entries, or nil: an id both
// list in one array under entries whose identity members differ, and then an
// id one lists in an array and the other in the array paired with it. The
// arrays are read in a fixed order and the ids in ascending order, so the
// refusal is the same on every run.
func collide(names [2]string, a, b *idEntries) error {
	for array := range idArrays {
		for _, id := range sortedIDs(a[array]) {
			if other, held := b[array][id]; held && other != a[array][id] {
				return &EntryError{
					Err: ErrEntryIdentity, Analyzers: names, ID: id,
					Arrays: [2]string{arrayNames[array], arrayNames[array]},
				}
			}
		}
	}
	for array := range idArrays {
		paired := pairedArray[array]
		for _, id := range sortedIDs(a[array]) {
			if _, held := b[paired][id]; held {
				return &EntryError{
					Err: ErrEntryState, Analyzers: names, ID: id,
					Arrays: [2]string{arrayNames[array], arrayNames[paired]},
				}
			}
		}
	}
	return nil
}

func sortedIDs(entries map[string]string) []string { return slices.Sorted(maps.Keys(entries)) }

// compareInputs orders two inputs as merged_from orders their entries, by
// analyzer name. Admission refuses two inputs of one name, so the members
// after the name only break the ties between inputs it refuses, and every
// member admission reads breaks one, so the refusal a merge names never depends
// on the order of its inputs.
func compareInputs(a, b Input) int {
	return cmp.Or(
		strings.Compare(a.Report.Analyzer.Name, b.Report.Analyzer.Name),
		strings.Compare(a.Report.Analyzer.Version, b.Report.Analyzer.Version),
		strings.Compare(a.Digest, b.Digest),
		strings.Compare(a.Report.SchemaVersion, b.Report.SchemaVersion),
		strings.Compare(string(a.Report.Analyzer.Conformance.Result), string(b.Report.Analyzer.Conformance.Result)),
		cmp.Compare(a.Report.Totals.Omitted, b.Report.Totals.Omitted),
		strings.Compare(string(a.Report.Target.Kind), string(b.Report.Target.Kind)),
		strings.Compare(a.Report.Target.Root, b.Report.Target.Root),
		strings.Compare(a.Report.Target.Identity, b.Report.Target.Identity),
	)
}
