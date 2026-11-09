// Package merge combines the reports analyzers wrote over one target into one
// merged report, by the algorithm contract/grammar/merge.md states. A merge
// reads nothing but its arguments, and two calls over the same inputs in any
// order return the same report.
package merge

import (
	"slices"

	"github.com/cplieger/deadset-spec/v8/facts"
	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
)

// Input is one analyzer report a merge reads, with the digest of the analyzer
// artifact that wrote it.
type Input struct {
	Report *report.Report

	// Facts is the facts document the analyzer wrote beside the report, nil
	// where the run asked for none.
	Facts *facts.Document

	// Digest is the sha256 of the analyzer artifact that ran, spelled
	// sha256:<64 lowercase hex digits>; the merged report names it in
	// merged_from.
	Digest string
}

// Caller is what the product running a merge knows about itself that no input
// report carries: the versions the merged report is written to, the name,
// version and conformance block of its analyzer member, and the run's
// analysis.min_confidence, empty for none.
type Caller struct {
	SchemaVersion   string
	ContractVersion string
	Name            string
	Version         string
	MinConfidence   vocab.Class
	Conformance     report.Conformance
}

// Merge admits every input against the accepted schema versions, carries every
// record of every input under its analyzer's name but an unmatched root another
// analyzer matched, resolves every pending finding, reports every stale edge,
// and orders the merged arrays by the canonical key, inside the caller's
// versions and analyzer member. It modifies none of its arguments. It returns
// no report with [errNoInput], an admission refusal ([*admissionError] per
// refused input or one error for the first two that cannot merge), or the
// run failure unresolved-edge for a pending finding no report can resolve.
func Merge(inputs []Input, accepted []string, caller *Caller) (*report.Report, error) {
	if len(inputs) == 0 {
		return nil, errNoInput
	}
	ordered := slices.Clone(inputs)
	slices.SortFunc(ordered, compareInputs)
	if err := admit(ordered, accepted); err != nil {
		return nil, err
	}
	carried := union(ordered)
	if err := carried.resolve(ordered, caller); err != nil {
		return nil, err
	}
	return envelope(ordered, accepted, caller, carried, mergedTarget(ordered)), nil
}

// None is the merged report of a run that merged no report, as
// contract/languages.json states for a run whose every analyzer found no
// program: the caller's product over target, naming no language, no input
// report and no finding.
func None(accepted []string, caller *Caller, target report.Target) *report.Report {
	return envelope(nil, accepted, caller, union(nil), target)
}
