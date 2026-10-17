package failure_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/failure"
)

// The first failure prints after its group's prefix and every further one
// without a prefix, each on one line.
func TestWritePrintsOneClassLine(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		failures []failure.Failure
		want     string
	}{
		{
			failures: []failure.Failure{{Class: vocab.SetupNoProgram, Detail: "a"}, {Class: vocab.RunProductFault, Detail: "b\nc"}},
			want:     "setup failure: no-program: a\nproduct-fault: b c\n",
		},
		{
			failures: []failure.Failure{{Class: vocab.RunMissingAnalyzer, Detail: "a"}, {Class: vocab.SetupMissingModule, Detail: "b"}},
			want:     "run failure: missing-analyzer: a\nmissing-module: b\n",
		},
	} {
		var out strings.Builder
		if err := failure.Write(&out, c.failures); err != nil || out.String() != c.want {
			t.Errorf("Write(%v) = %q, %v, want %q", c.failures, out.String(), err, c.want)
		}
	}
}

// Of finds a failure through every error that wraps it, and names an error no
// class names a product fault.
func TestOfNamesAProductFaultForAnUnclassedError(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("context: %w", failure.New(vocab.RunUnresolvedEdge, "edge"))
	if got := failure.Of(wrapped); len(got) != 1 || got[0].Class != vocab.RunUnresolvedEdge {
		t.Errorf("Of(%v) = %v, want the unresolved-edge failure", wrapped, got)
	}
	if got := failure.Of(errors.New("broke")); len(got) != 1 || got[0].Class != vocab.RunProductFault || !strings.HasPrefix(got[0].Detail, "broke; reinstall") {
		t.Errorf("Of(an unclassed error) = %v, want a product fault naming it and the fix", got)
	}
}

func TestRelayedTakesTheFirstClassLine(t *testing.T) {
	t.Parallel()

	printed := "note: loading\nmissing-module: b\nrun failure: memory-exhausted: a\nsetup failure: no-program: c\nmissing-module: d\nnot-a-class: e\n"
	relayed, rest := failure.Relayed("deadset-go", []byte(printed))
	want := []failure.Failure{
		{Class: vocab.RunMemoryExhausted, Detail: "a"},
		{Class: vocab.SetupNoProgram, Detail: "c"},
		{Class: vocab.SetupMissingModule, Detail: "d"},
	}
	if fmt.Sprint(relayed.Failures) != fmt.Sprint(want) || string(rest) != "deadset-go: note: loading\ndeadset-go: missing-module: b\ndeadset-go: not-a-class: e\n" || relayed.Setup() {
		t.Errorf("Relayed(%q) = %v, %q, want %v and the lines above the class line", printed, relayed.Failures, rest, want)
	}
}

func TestRelayedNamesAnUnreadableClassLineAProductFault(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		printed string
		names   []string
	}{
		{printed: "setup failure: not-a-class: x\nsetup failure: missing-module\n", names: []string{"deadset-ts", "cannot read", "not-a-class: x", "\nproduct-fault: ", "missing-module"}},
		{printed: "setup failure: missing-module\n", names: []string{"deadset-ts", "cannot read", "missing-module"}},
		{printed: "missing-module: b\n", names: []string{"deadset-ts", "printed no class line"}},
	} {
		relayed, rest := failure.Relayed("deadset-ts", []byte(c.printed))
		var out strings.Builder
		if err := failure.Write(&out, relayed.Failures); err != nil {
			t.Fatalf("Write: %v", err)
		}
		printed := string(rest) + out.String()
		if got := strings.Count(printed, vocab.SetupFailurePrefix) + strings.Count(printed, vocab.RunFailurePrefix); got != 1 {
			t.Errorf("Relayed(%q) prints %d prefixed lines, want 1\n%s", c.printed, got, printed)
		}
		if relayed.Failures[0].Class != vocab.RunProductFault {
			t.Errorf("Relayed(%q) class line = %v, want a product fault", c.printed, relayed.Failures[0])
		}
		for _, name := range c.names {
			if !strings.Contains(out.String(), name) {
				t.Errorf("Relayed(%q) failures %q, want them to name %q", c.printed, out.String(), name)
			}
		}
	}
}

func TestQuotedShowsEveryLineUnderTheAnalyzersName(t *testing.T) {
	t.Parallel()

	for printed, want := range map[string]string{
		"":                      "",
		"a":                     "deadset-go: a\n",
		"\n":                    "deadset-go: \n",
		"a\n\nb\r\nc\rd\n":      "deadset-go: a\ndeadset-go: \ndeadset-go: b\ndeadset-go: c\ndeadset-go: d\n",
		" run failure: x\r\n\r": "deadset-go:  run failure: x\ndeadset-go: \n",
		"setup failure: a\nb\n": "deadset-go: setup failure: a\ndeadset-go: b\n",
		"deadset-go:":           "deadset-go: deadset-go:\n",
		"deadset-go: a\ndeadset-go:\ndeadset-gox: b\ndeadset-go:c": "deadset-go: a\ndeadset-go: deadset-go:\ndeadset-go: deadset-gox: b\ndeadset-go: deadset-go:c\n",
	} {
		if got := string(failure.Quoted("deadset-go", []byte(printed))); got != want {
			t.Errorf("Quoted(%q) = %q, want %q", printed, got, want)
		}
	}
}

// A further failure opens with its class, which no class of the table spells
// with a group prefix, and every line break of a detail is a space.
func TestWritePrintsNoFurtherClassLine(t *testing.T) {
	t.Parallel()

	var classes []string
	for _, c := range vocab.SetupClasses() {
		classes = append(classes, c.Name)
	}
	for _, c := range vocab.RunClasses() {
		classes = append(classes, c.Name)
	}
	for _, class := range classes {
		var out strings.Builder
		failures := []failure.Failure{{Class: class, Detail: "a\rrun failure: b\r\nsetup failure: c"}, {Class: class, Detail: "d\nrun failure: e"}}
		if err := failure.Write(&out, failures); err != nil {
			t.Fatalf("Write: %v", err)
		}
		lines := strings.FieldsFunc(out.String(), func(r rune) bool { return r == '\n' || r == '\r' })
		if len(lines) != 2 || !strings.HasPrefix(lines[1], class+": d run failure: e") ||
			strings.HasPrefix(class, strings.TrimSuffix(vocab.SetupFailurePrefix, " ")) || strings.HasPrefix(class, strings.TrimSuffix(vocab.RunFailurePrefix, " ")) {
			t.Errorf("Write(%s) = %q, want the class line and one further line its class opens", class, out.String())
		}
	}
}

func TestNoProgramNamesWhatALanguageReadsAProgramFrom(t *testing.T) {
	t.Parallel()

	detail := failure.Wrap(vocab.SetupNoProgram, errors.New("the target cannot be read")).Failures[0].Detail
	for _, name := range []string{"go reads one from go.mod or a .go file", "ts reads one from tsconfig*.json", "run from the directory", "analysis.languages"} {
		if !strings.Contains(detail, name) {
			t.Errorf("the no-program failure %q, want it to name %q", detail, name)
		}
	}
}
