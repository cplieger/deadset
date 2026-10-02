// Package merge combines the reports analyzers wrote over one target into one
// merged report, by the algorithm contract/grammar/merge.md states. A merge
// reads nothing but its arguments, and two calls over the same inputs in any
// order return the same report.
package merge

import (
	"slices"

	"github.com/cplieger/deadset/internal/report"
)

// Input is one analyzer report a merge reads, with the digest of the analyzer
// artifact that wrote it.
type Input struct {
	Report *report.Report

	// Digest is the sha256 of the analyzer artifact that ran, spelled
	// sha256:<64 lowercase hex digits>; the merged report names it in
	// merged_from.
	Digest string
}

// Caller is what the product running a merge knows about itself that no input
// report carries: the versions the merged report is written to, and the name,
// version and conformance block of its analyzer member.
type Caller struct {
	SchemaVersion   string
	ContractVersion string
	Name            string
	Version         string
	Conformance     report.Conformance
}

// Merge admits every input against the accepted schema versions, carries every
// record of every input under the name of its analyzer, and orders the merged
// arrays by the canonical key. The merged report's versions and analyzer member
// are the caller's, with the inputs' languages and accepted beside them. Merge
// modifies none of its arguments.
//
// It returns no report with [ErrNoInput] or an admission refusal, an
// [*AdmissionError] per refused input or one error for the first two that cannot merge.
func Merge(inputs []Input, accepted []string, caller *Caller) (*report.Report, error) {
	if len(inputs) == 0 {
		return nil, ErrNoInput
	}
	ordered := slices.Clone(inputs)
	slices.SortFunc(ordered, compareInputs)
	if err := admit(ordered, accepted); err != nil {
		return nil, err
	}
	carried := union(ordered)
	carried.order()
	return envelope(ordered, accepted, caller, carried), nil
}
