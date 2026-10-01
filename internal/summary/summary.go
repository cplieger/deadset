// Package summary renders what a run prints about its merged report beside the
// report itself: the analyzers the merge read, one summary line over every
// language the run covered, and one workflow annotation per finding and per
// stale suppression.
package summary

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/cplieger/deadset/internal/config"
	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/verdict"
)

// Write writes one line per analyzer r was merged from, naming its version and
// the digest of its artifact in the order r lists them, then the summary line:
// every count r's totals hold and the languages r covers. Neither shape is a
// finding line's, so a filter for finding lines passes none of them.
func Write(w io.Writer, r *report.Report) error {
	var text strings.Builder
	for _, ran := range r.MergedFrom {
		fmt.Fprintf(&text, "analyzer %s %s %s\n", ran.Name, ran.Version, ran.Digest)
	}
	totals := &r.Totals
	fmt.Fprintf(&text, "summary: %s (%d allow, %d warn, %d deny), %s, %s, %s, %s, %d pending, %d omitted, across %s\n",
		counted(totals.Findings, "finding", "findings"),
		totals.BySeverity.Allow, totals.BySeverity.Warn, totals.BySeverity.Deny,
		counted(totals.DeletableLines, "deletable line", "deletable lines"),
		counted(totals.SuppressionsInEffect, "suppression in effect", "suppressions in effect"),
		counted(totals.ReasonsRecorded, "reason recorded", "reasons recorded"),
		counted(totals.StaleSuppressions, "stale suppression", "stale suppressions"),
		totals.Pending, totals.Omitted, listed(r.Analyzer.Languages))
	_, err := io.WriteString(w, text.String())
	return err
}

// counted renders a count with the noun for it.
func counted(n int, one, many string) string {
	if n == 1 {
		return strconv.Itoa(n) + " " + one
	}
	return strconv.Itoa(n) + " " + many
}

// listed joins names as a sentence lists them: "go", "go and ts".
func listed(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// staleSuppressionKind is the name contract/kinds.json gives DS1703, the one
// code a stale-suppression record carries.
const staleSuppressionKind = "stale-suppression"

// Annotations writes one GitHub workflow command per finding of r and one per
// stale suppression, each naming the file, line, column and last line of its
// record, titled with the code and the kind, and carrying the record's message.
// A record that fails the run under failOn is an error, which a stale
// suppression always is; a warn finding that does not is a warning, and an
// allow finding that does not is a notice.
func Annotations(w io.Writer, r *report.Report, failOn config.Severity) error {
	var text strings.Builder
	for i := range r.Findings {
		found := &r.Findings[i]
		annotate(&text, commandOf(found.Severity, failOn), &found.Position, found.Code+" "+found.Kind, found.Message)
	}
	for i := range r.StaleSuppressions {
		stale := &r.StaleSuppressions[i]
		at := report.Position{
			Path: stale.Position.Path, Line: stale.Position.Line,
			Column: stale.Position.Column, EndLine: stale.Position.Line,
		}
		annotate(&text, "error", &at, stale.Code+" "+staleSuppressionKind, stale.Message)
	}
	_, err := io.WriteString(w, text.String())
	return err
}

// commandOf is the workflow command a finding of the given severity is
// annotated with under failOn.
func commandOf(severity report.Severity, failOn config.Severity) string {
	switch {
	case verdict.Fails(severity, failOn):
		return "error"
	case severity == report.SeverityWarn:
		return "warning"
	default:
		return "notice"
	}
}

// annotate writes one workflow command, each value escaped as the command
// grammar requires so a separator inside it cannot end a property or the line.
func annotate(text *strings.Builder, command string, at *report.Position, title, message string) {
	fmt.Fprintf(text, "::%s file=%s,line=%d,col=%d,endLine=%d,title=%s::%s\n",
		command, propertyEscapes.Replace(at.Path), at.Line, at.Column, at.EndLine,
		propertyEscapes.Replace(title), dataEscapes.Replace(message))
}

// The escapes of a workflow command: its message escapes the escape character
// and both line breaks, and a property value escapes those and the two
// separators of the property list as well.
var (
	dataEscapes     = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")
	propertyEscapes = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C")
)
