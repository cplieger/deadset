package merge

import (
	"bytes"
	"cmp"
	"encoding/json"
	"slices"
	"strings"

	"github.com/cplieger/deadset/internal/report"
)

// key is the canonical key merge.md fixes for a finding, a stale suppression
// and a declared gap: the path, line and column, the code, the symbol
// reference and the name of the analyzer that carried the record. A component
// a record does not carry is the empty value, which sorts before every present
// one.
type key struct {
	path     string
	code     string
	ref      string
	analyzer string
	line     int
	column   int
}

func (k *key) compare(other *key) int {
	return cmp.Or(
		strings.Compare(k.path, other.path),
		cmp.Compare(k.line, other.line),
		cmp.Compare(k.column, other.column),
		strings.Compare(k.code, other.code),
		strings.Compare(k.ref, other.ref),
		strings.Compare(k.analyzer, other.analyzer),
	)
}

func findingKey(f *report.Finding) key {
	return key{
		path:     f.Position.Path,
		line:     f.Position.Line,
		column:   f.Position.Column,
		code:     f.Code,
		ref:      f.Symbol.Ref,
		analyzer: f.Analyzer,
	}
}

func staleKey(s *report.StaleSuppression) key {
	return key{
		path:     s.Position.Path,
		line:     s.Position.Line,
		column:   s.Position.Column,
		code:     s.Code,
		ref:      s.Symbol,
		analyzer: s.Analyzer,
	}
}

// gapKey is a declared gap's key, which carries the analyzer name alone.
func gapKey(g *report.DeclaredGap) key {
	return key{analyzer: g.Analyzer}
}

func compareFindings(a, b *report.Finding) int {
	first, second := findingKey(a), findingKey(b)
	return first.compare(&second)
}

func compareStale(a, b *report.StaleSuppression) int {
	first, second := staleKey(a), staleKey(b)
	return first.compare(&second)
}

func compareGaps(a, b *report.DeclaredGap) int {
	first, second := gapKey(a), gapKey(b)
	return first.compare(&second)
}

func compareEvaluations(a, b *report.EdgeEvaluation) int {
	return cmp.Or(
		strings.Compare(a.Edge, b.Edge),
		strings.Compare(string(a.Side), string(b.Side)),
		strings.Compare(a.Analyzer, b.Analyzer),
	)
}

func (r *records) order() {
	sortTotal(r.findings, compareFindings)
	sortTotal(r.stale, compareStale)
	sortTotal(r.gaps, compareGaps)
	sortTotal(r.evaluations, compareEvaluations)
}

type encoded[T any] struct {
	record   T
	encoding string
}

// sortTotal sorts records by compare and then by the bytewise order of their
// compact JSON encodings in the schema's member order, which merge.md makes
// the last tiebreak of every order: two records it leaves tied encode to the
// same bytes, so the sorted array does not depend on the order it started in.
func sortTotal[T any](records []T, compare func(a, b *T) int) {
	held := sortEncoded(records, compare)
	for i := range held {
		records[i] = held[i].record
	}
}

// distinct is records in sortTotal's order with every repeated record, one
// whose encoding equals another's, appearing once.
func distinct[T any](records []T, compare func(a, b *T) int) []T {
	held := slices.CompactFunc(sortEncoded(records, compare), func(a, b encoded[T]) bool { return a.encoding == b.encoding })
	kept := make([]T, len(held))
	for i := range held {
		kept[i] = held[i].record
	}
	return kept
}

// sortEncoded is every record with its encoding, in sortTotal's order.
func sortEncoded[T any](records []T, compare func(a, b *T) int) []encoded[T] {
	held := make([]encoded[T], len(records))
	for i := range records {
		held[i] = encoded[T]{record: records[i], encoding: compact(&records[i])}
	}
	slices.SortFunc(held, func(a, b encoded[T]) int {
		return cmp.Or(compare(&a.record, &b.record), strings.Compare(a.encoding, b.encoding))
	})
	return held
}

// compact is v's compact JSON encoding, escaping only what strict JSON
// requires escaped, as a report document writes its strings. Every record of
// a report is strings, integers, booleans and arrays and objects of them, so
// the encoding cannot fail.
func compact(v any) string {
	var written bytes.Buffer
	encoder := json.NewEncoder(&written)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		panic("merge: a report record does not encode: " + err.Error())
	}
	return strings.TrimSuffix(written.String(), "\n")
}
