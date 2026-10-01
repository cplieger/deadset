// Package merge combines the reports analyzers wrote over one target into one
// merged report, by the algorithm contract/grammar/merge.md states. A merge
// reads nothing but its arguments, and two calls over the same inputs in any
// order return the same report.
package merge

import "github.com/cplieger/deadset/internal/report"

// Input is one analyzer report a merge reads, with the digest of the analyzer
// artifact that wrote it.
type Input struct {
	Report *report.Report

	// Digest is the sha256 of the analyzer artifact that ran, spelled
	// sha256:<64 lowercase hex digits>; the merged report names it in
	// merged_from.
	Digest string
}

// Merge admits every input against the accepted schema versions, carries every
// record of every input under the name of its analyzer, and orders the merged
// arrays by the canonical key. The merged report names a copy of self, with the
// inputs' languages and accepted in place of self's own. Merge modifies none of
// its arguments.
//
// It returns [ErrNoInput], an [*AdmissionError] per refused input (joined when
// there are several) or a [*TargetError], and then no report.
func Merge(inputs []Input, accepted []string, self *report.Analyzer) (*report.Report, error) {
	if len(inputs) == 0 {
		return nil, ErrNoInput
	}
	if err := admit(inputs, accepted); err != nil {
		return nil, err
	}
	carried := union(inputs)
	carried.order()
	return envelope(inputs, accepted, self, carried), nil
}
