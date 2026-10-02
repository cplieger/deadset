package render

import (
	"bytes"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"testing"

	spec "github.com/cplieger/deadset-spec/v4"
	"github.com/cplieger/deadset/internal/verdict"
)

const templateVectors = "vectors/template"

// Every published template case renders its expected bytes, or ends the run
// with the code it states: 2 for a template refused at parse, before any
// analysis, and 3 for a rendering that fails, which writes nothing.
func TestTemplateReproducesEveryPublishedVector(t *testing.T) {
	t.Parallel()

	for _, name := range vectorCaseNames(t, templateVectors) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := path.Join(templateVectors, name)
			text := string(readSpecVector(t, path.Join(dir, "template.tmpl")))
			document := readSpecVector(t, path.Join(dir, "report.json"))
			want, wantExit := templateOutcome(t, dir)

			parsed, err := ParseTemplate(text)
			if err != nil {
				if wantExit != verdict.Usage {
					t.Fatalf("ParseTemplate(%s) = %v, want the template parsed", name, err)
				}
				return
			}
			if wantExit == verdict.Usage {
				t.Fatalf("ParseTemplate(%s) parsed the template, want it refused before any analysis", name)
			}
			var out bytes.Buffer
			err = parsed.execute(&out, document)
			if wantExit != 0 {
				if err == nil || verdict.ForError(err) != wantExit || out.Len() != 0 {
					t.Errorf("render(%s) = %v and wrote %q, want a failure ending the run with %d and nothing written",
						name, err, out.String(), wantExit)
				}
				return
			}
			if err != nil {
				t.Fatalf("render(%s) = %v, want %q", name, err, want)
			}
			if got := out.String(); got != want {
				t.Errorf("render(%s) = %q, want expected.txt %q", name, got, want)
			}
		})
	}
}

// templateOutcome is a case's expected rendering, or the exit code it ends
// the run with.
func templateOutcome(t *testing.T, dir string) (string, int) {
	t.Helper()

	if expected, err := fs.ReadFile(spec.Vectors, path.Join(dir, "expected.txt")); err == nil {
		return string(expected), 0
	}
	exit, err := strconv.Atoi(strings.TrimSpace(string(readSpecVector(t, path.Join(dir, "expected_exit")))))
	if err != nil {
		t.Fatalf("Setup: %s/expected_exit: %v", dir, err)
	}
	return "", exit
}
