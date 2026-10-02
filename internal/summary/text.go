package summary

import (
	"fmt"
	"io"
	"strings"

	"github.com/cplieger/deadset/internal/report"
)

// The kind token and the confidence a stale suppression's text line carries:
// the record is about a suppression whatever subject it named, and its kind is
// fixed on, so the line claims what the analysis knows.
const (
	staleSuppressionToken      = "suppression"
	staleSuppressionConfidence = "certain"
)

// Text writes one text line per finding of r and then one per stale
// suppression, each in the order r holds them, in the shape
// contract/grammar/text-line.md states: the position, the kind, the name, the
// message, the confidence and the code.
func Text(w io.Writer, r *report.Report) error {
	var text strings.Builder
	for i := range r.Findings {
		found := &r.Findings[i]
		fmt.Fprintf(&text, "%s:%d:%d: %s %s: %s [%s] (%s)\n",
			found.Position.Path, found.Position.Line, found.Position.Column,
			found.Symbol.Kind, found.Symbol.Name, found.Message, found.Confidence, found.Code)
	}
	for i := range r.StaleSuppressions {
		stale := &r.StaleSuppressions[i]
		fmt.Fprintf(&text, "%s:%d:%d: %s %s: %s [%s] (%s)\n",
			stale.Position.Path, stale.Position.Line, stale.Position.Column,
			staleSuppressionToken, stale.Symbol, stale.Message, staleSuppressionConfidence, stale.Code)
	}
	_, err := io.WriteString(w, text.String())
	return err
}

// remediation is what a run that a finding failed prints after its summary.
const remediation = "remediation: for each failing finding, delete the symbol, wire it up so the program uses it, " +
	"or record an adjudication with its reason in an inline directive or a deadset-ignore.json entry\n"

// Remediation writes the three ways to resolve a failing finding.
func Remediation(w io.Writer) error {
	_, err := io.WriteString(w, remediation)
	return err
}
