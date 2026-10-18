// Package failure is a run that ends with exit code 3 and the one rendering
// of it on standard error. Each failure is of one class of the Contract's
// failure_classes; the first prints after its group's prefix as the run's one
// class line, and every further one on a line of its own without a prefix.
// What an analyzer printed beside its failures leaves this package only
// [Quoted], each line opened by the analyzer's name, so [Write] alone opens a
// line with a group prefix.
package failure

import (
	"errors"
	"fmt"
	"io"
	"iter"
	"slices"
	"strings"

	"github.com/cplieger/deadset-spec/v8/vocab"
)

// Failure is one cause that ends a run: its class and what fails, the fix
// included.
type Failure struct {
	Class  string
	Detail string
}

// Error is a run that ends with exit code 3 for its failures, in the order
// they were found.
type Error struct {
	err      error
	Failures []Failure
}

func (e *Error) Error() string {
	lines := make([]string, len(e.Failures))
	for i, f := range e.Failures {
		lines[i] = f.Class + ": " + f.Detail
	}
	return strings.Join(lines, "\n")
}

func (e *Error) Unwrap() error { return e.err }

// Setup reports whether the first failure is of the setup group.
func (e *Error) Setup() bool { return isSetup(e.Failures[0].Class) }

// fixes is the fix each class deadset names itself states after what fails.
var fixes = map[string]string{
	vocab.SetupNoProgram:          noProgramFix(vocab.LanguageRows()),
	vocab.SetupUnreadableDocument: "make it a regular file inside the target root that the run can read, or remove it",
	vocab.RunUnwrittenOutput:      "name a path the run can write, or correct the template that renders it",
	vocab.RunMissingAnalyzer:      "install the analyzer, or correct the command of its provider entry",
	vocab.RunUnadmittedAnalyzer:   "install a release of the analyzer that the run admits",
	vocab.RunUnresolvedEdge:       "run the analyzer of that side's language, or remove the edge from " + vocab.EdgesFileName,
	vocab.RunInterruptedRun:       "run it again without interrupting it",
	vocab.RunProductFault:         "reinstall deadset and its analyzers, and report the fault to their maintainers where a reinstalled product fails the same way",
}

// noProgramFix is the fix of the setup failure no-program over rows: the
// markers and the source extensions each row reads a program from.
func noProgramFix(rows []vocab.LanguageRow) string {
	reads := make([]string, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		from := slices.Clone(row.Markers)
		if len(row.Extensions) > 0 {
			from = append(from, "a "+strings.Join(row.Extensions, " or ")+" file")
		}
		reads = append(reads, string(row.Language)+" reads one from "+strings.Join(from, " or "))
	}
	return "run from the directory that holds a program (" + strings.Join(reads, "; ") + "), or name the languages to analyze in " + string(vocab.KeyAnalysisLanguages)
}

// New is a failure of class whose detail names what fails and the fix.
func New(class, detail string) *Error {
	return &Error{Failures: []Failure{{Class: class, Detail: detail}}}
}

// Wrap is err as a failure of class, followed by the class's fix.
func Wrap(class string, err error) *Error {
	e := New(class, err.Error()+"; "+fixes[class])
	e.err = err
	return e
}

// Of is the failures err holds: an [*Error]'s, or a product fault naming an
// error no class names.
func Of(err error) []Failure {
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Failures
	}
	return Wrap(vocab.RunProductFault, err).Failures
}

// Write prints failures on w, the first as the class line.
func Write(w io.Writer, failures []Failure) error {
	var out strings.Builder
	for i, f := range failures {
		if i == 0 {
			out.WriteString(prefix(f.Class))
		}
		fmt.Fprintf(&out, "%s: %s\n", f.Class, strings.Join(slices.Collect(lines(f.Detail)), " "))
	}
	_, err := io.WriteString(w, out.String())
	return err
}

// Relayed is what an analyzer that ended with exit code 3 printed, split into
// its failures and every other line, [Quoted]. Its first prefixed line is its
// class line; below it, a line of a class is a further failure. A prefixed
// line of a class deadset cannot read is a product fault quoting it, and an
// analyzer that printed no prefixed line a product fault naming it, beside
// all it printed.
func Relayed(analyzer string, printed []byte) (relayed *Error, rest []byte) {
	relayed = &Error{}
	for line := range lines(string(printed)) {
		unprefixed, prefixed := cutPrefix(line)
		f, ok := classed(unprefixed)
		switch {
		case prefixed && !ok:
			f = Failure{Class: vocab.RunProductFault, Detail: fmt.Sprintf("analyzer %s printed a class line deadset cannot read: %s; %s",
				analyzer, unprefixed, fixes[vocab.RunProductFault])}
		case !ok, !prefixed && len(relayed.Failures) == 0:
			rest = quote(rest, analyzer, line)
			continue
		}
		relayed.Failures = append(relayed.Failures, f)
	}
	if len(relayed.Failures) == 0 {
		return New(vocab.RunProductFault, fmt.Sprintf("analyzer %s ended with exit code %d and printed no class line; %s",
			analyzer, vocab.ExitFailure, fixes[vocab.RunProductFault])), Quoted(analyzer, printed)
	}
	return relayed, rest
}

// Quoted is every line an analyzer printed opened by its name, a colon and a
// space, each ended by a line feed; a line the analyzer already opened so is
// kept as it is. analyzer is a provider entry's name, which the configuration
// schema spells with no space, so no quoted line opens with a group prefix.
func Quoted(analyzer string, printed []byte) []byte {
	var out []byte
	for line := range lines(string(printed)) {
		out = quote(out, analyzer, line)
	}
	return out
}

func quote(out []byte, analyzer, line string) []byte {
	if !strings.HasPrefix(line, analyzer+": ") {
		out = append(out, analyzer+": "...)
	}
	return append(append(out, line...), '\n')
}

// lines is every line of text without its break: a line feed, a carriage
// return, or the two together. A carriage return counts because a terminal
// prints what follows it from the start of the line.
func lines(text string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for text != "" {
			at := strings.IndexAny(text, "\r\n")
			if at < 0 {
				yield(text)
				return
			}
			next := at + 1
			if text[at] == '\r' && strings.HasPrefix(text[next:], "\n") {
				next++
			}
			if !yield(text[:at]) {
				return
			}
			text = text[next:]
		}
	}
}

func cutPrefix(line string) (string, bool) {
	for _, p := range []string{vocab.SetupFailurePrefix, vocab.RunFailurePrefix} {
		if rest, ok := strings.CutPrefix(line, p); ok {
			return rest, true
		}
	}
	return line, false
}

func classed(line string) (Failure, bool) {
	class, detail, ok := strings.Cut(line, ": ")
	if !ok || !isSetup(class) && !slices.ContainsFunc(vocab.RunClasses(), func(c vocab.RunClass) bool { return c.Name == class }) {
		return Failure{}, false
	}
	return Failure{Class: class, Detail: detail}, true
}

func isSetup(class string) bool {
	return slices.ContainsFunc(vocab.SetupClasses(), func(c vocab.SetupClass) bool { return c.Name == class })
}

func prefix(class string) string {
	if isSetup(class) {
		return vocab.SetupFailurePrefix
	}
	return vocab.RunFailurePrefix
}
