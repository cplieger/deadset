package providers_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/deadset/internal/config"
	"github.com/cplieger/deadset/internal/providers"
	"github.com/cplieger/deadset/internal/verdict"
)

// executable writes an executable file named name in dir and returns its path.
// Selection never runs it.
func executable(t *testing.T, dir, name string) string {
	t.Helper()

	file := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := os.WriteFile(file, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return file
}

// resolvedList is the provider list a repository configuration naming analyzers
// resolves to.
func resolvedList(t *testing.T, analyzers string) []config.Provider {
	t.Helper()

	document := `{"target": {"kind": "library"}}`
	if analyzers != "" {
		document = `{"target": {"kind": "library"}, "providers": {"analyzers": ` + analyzers + `}}`
	}
	r, err := config.Resolve(&config.Inputs{
		ContractVersion: "3.2.0",
		Repository:      config.Document{Path: "deadset.json", Data: []byte(document)},
	})
	if err != nil {
		t.Fatalf("Setup: Resolve(%s) = error %v", document, err)
	}
	return r.Config.Providers
}

// entry spells one installed provider entry.
func entry(name, command string, languages ...string) string {
	quoted := make([]string, len(languages))
	for i, language := range languages {
		quoted[i] = fmt.Sprintf("%q", language)
	}
	return fmt.Sprintf(`{"name": %q, "languages": [%s], "command": %q}`, name, strings.Join(quoted, ", "), command)
}

// list spells a provider list.
func list(entries ...string) string { return "[" + strings.Join(entries, ", ") + "]" }

// selection is what one selection chose: each analyzer's name and executable.
func selection(analyzers []providers.Analyzer) []string {
	chosen := make([]string, len(analyzers))
	for i, a := range analyzers {
		chosen[i] = a.Entry.Name + "=" + a.Executable
	}
	return chosen
}

// The run invokes every entry that claims a language in scope, two entries
// claiming one language included, each once and in the list's order, and no
// entry that claims none.
func TestSelect_invokesEveryEntryClaimingALanguageInScope(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	goA, goB := executable(t, dir, "go-a"), executable(t, dir, "go-b")
	both, tsOnly := executable(t, dir, "both"), executable(t, dir, "ts-only")
	entries := resolvedList(t, list(
		entry("go-b", goB, "go"),
		entry("ts-only", tsOnly, "ts"),
		entry("both", both, "ts", "go"),
		entry("go-a", goA, "go"),
	))
	cases := []struct {
		name    string
		inScope []string
		want    []string
	}{
		{name: "go", inScope: []string{"go"}, want: []string{"go-b=" + goB, "both=" + both, "go-a=" + goA}},
		{name: "ts", inScope: []string{"ts"}, want: []string{"ts-only=" + tsOnly, "both=" + both}},
		{
			name: "go-and-ts", inScope: []string{"go", "ts"},
			want: []string{"go-b=" + goB, "ts-only=" + tsOnly, "both=" + both, "go-a=" + goA},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := providers.Select(entries, c.inScope)
			if err != nil {
				t.Fatalf("Select(%v) = error %v", c.inScope, err)
			}
			if chosen := selection(got); !slices.Equal(chosen, c.want) {
				t.Errorf("Select(%v) chose %v, want %v", c.inScope, chosen, c.want)
			}
		})
	}
}

// An entry is read whole into the analyzer the run invokes, its artifact
// included.
func TestSelect_carriesTheEntry(t *testing.T) {
	t.Parallel()

	command := executable(t, t.TempDir(), "other-go")
	digest := "sha256:" + strings.Repeat("0", 64)
	entries := resolvedList(t, list(fmt.Sprintf(`{"name": "other-go", "languages": ["go"], "command": %q, `+
		`"source": "go:example.com/other/cmd/other-go", "version": "1.4.0", "digest": %q}`, command, digest)))

	got, err := providers.Select(entries, []string{"go"})
	if err != nil {
		t.Fatalf("Select(go) = error %v", err)
	}
	want := config.Provider{Name: "other-go", Languages: []string{"go"}, Command: command}
	if len(got) != 1 {
		t.Fatalf("Select(go) chose %v, want other-go alone", selection(got))
	}
	if e := got[0].Entry; e.Name != want.Name || e.Command != want.Command || !slices.Equal(e.Languages, want.Languages) {
		t.Errorf("Select(go)[0].Entry = %+v, want %+v", e, want)
	}
}

// A default entry the list deletes is not invoked, even when its own command
// would resolve, and the entry that replaces it is.
func TestSelect_doesNotInvokeADeletedDefaultEntry(t *testing.T) {
	dir := t.TempDir()
	executable(t, dir, "deadset-go")
	executable(t, dir, "deadset-ts")
	replacement := executable(t, dir, "other-go")
	t.Setenv("PATH", dir)

	got, err := providers.Select(resolvedList(t, list(entry("other-go", replacement, "go"), entry("deadset-ts", "deadset-ts", "ts"))),
		[]string{"go", "ts"})
	if err != nil {
		t.Fatalf("Select(go, ts) = error %v", err)
	}
	want := []string{"other-go=" + replacement, "deadset-ts=" + filepath.Join(dir, "deadset-ts")}
	if chosen := selection(got); !slices.Equal(chosen, want) {
		t.Errorf("Select(go, ts) chose %v, want %v", chosen, want)
	}
}

// A command that is a name is looked up on PATH, and an absolute command is
// taken as it is.
func TestSelect_resolvesACommandOnPathOrAsAnAbsolutePath(t *testing.T) {
	onPath, elsewhere := t.TempDir(), t.TempDir()
	executable(t, onPath, "deadset-go")
	absolute := executable(t, elsewhere, "deadset-ts")
	t.Setenv("PATH", onPath)

	got, err := providers.Select(resolvedList(t, list(entry("deadset-go", "deadset-go", "go"), entry("deadset-ts", absolute, "ts"))),
		[]string{"go", "ts"})
	if err != nil {
		t.Fatalf("Select(go, ts) = error %v", err)
	}
	want := []string{"deadset-go=" + filepath.Join(onPath, "deadset-go"), "deadset-ts=" + absolute}
	if chosen := selection(got); !slices.Equal(chosen, want) {
		t.Errorf("Select(go, ts) chose %v, want %v", chosen, want)
	}
}

// A language in scope that no entry claims ends the run with the usage code,
// naming the language, and before any command is resolved.
func TestSelect_refusesALanguageNoEntryClaims(t *testing.T) {
	t.Parallel()

	absent := filepath.Join(t.TempDir(), "absent")
	cases := []struct {
		name      string
		analyzers string
		inScope   []string
		unclaimed []string
	}{
		{name: "default-deleted", analyzers: list(entry("deadset-go", absent, "go")), inScope: []string{"go", "ts"}, unclaimed: []string{`"ts"`}},
		{name: "every-entry-deleted", analyzers: list(), inScope: []string{"go", "ts"}, unclaimed: []string{`"go"`, `"ts"`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := providers.Select(resolvedList(t, c.analyzers), c.inScope)
			if got != nil {
				t.Errorf("Select(%v) chose %v, want nothing", c.inScope, selection(got))
			}
			refused, ok := errors.AsType[*config.Error](err)
			if !ok {
				t.Fatalf("Select(%v) = error %v, want a *config.Error", c.inScope, err)
			}
			if !strings.Contains(refused.Error(), "providers.analyzers") {
				t.Errorf("Select(%v) refusal = %q, want the message to name providers.analyzers", c.inScope, refused.Error())
			}
			for _, language := range c.unclaimed {
				if !strings.Contains(refused.Message, language) {
					t.Errorf("Select(%v) = %q, want it to name %s", c.inScope, refused.Message, language)
				}
			}
			if code := verdict.ForError(err); code != verdict.Usage {
				t.Errorf("verdict.ForError(Select(%v)) = %d, want %d", c.inScope, code, verdict.Usage)
			}
		})
	}
}

// An entry the run invokes whose command resolves to no executable ends the
// run with the failure code, naming the analyzer and its entry; every such
// entry is named, and an entry the run does not invoke is not looked at.
func TestSelect_refusesACommandThatResolvesToNoExecutable(t *testing.T) {
	dir := t.TempDir()
	notExecutable := filepath.Join(dir, "not-executable")
	if err := os.WriteFile(notExecutable, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	present := executable(t, dir, "present")
	t.Setenv("PATH", dir)

	entries := resolvedList(t, list(
		entry("present", present, "go"),
		entry("not-on-path", "deadset-go", "go"),
		entry("unused", filepath.Join(dir, "absent"), "ts"),
		entry("not-executable", notExecutable, "go"),
		entry("absent", filepath.Join(dir, "absent"), "go"),
	))
	got, err := providers.Select(entries, []string{"go"})
	if got != nil {
		t.Errorf("Select(go) chose %v, want nothing", selection(got))
	}
	var refused []string
	for _, one := range joined(err) {
		e, ok := errors.AsType[*providers.CommandError](one)
		if !ok {
			t.Fatalf("Select(go) = error %v, want only *providers.CommandError", err)
		}
		if _, lookup := errors.AsType[*exec.Error](e); !lookup {
			t.Errorf("Select(go) refused %s with %v, want the lookup's *exec.Error", e.Name, e.Err)
		}
		refused = append(refused, fmt.Sprintf("%s[%d]", e.Name, e.Index))
	}
	if want := []string{"not-on-path[1]", "not-executable[3]", "absent[4]"}; !slices.Equal(refused, want) {
		t.Errorf("Select(go) refused %v, want %v", refused, want)
	}
	if code := verdict.ForError(err); code != verdict.Failure {
		t.Errorf("verdict.ForError(Select(go)) = %d, want %d", code, verdict.Failure)
	}
	if msg := err.Error(); !strings.Contains(msg, "analyzer absent, provider entry providers.analyzers[4]") {
		t.Errorf("Select(go) = %q, want it to name the analyzer and providers.analyzers[4]", msg)
	}
}

// A command that is a relative path is refused, even where it would resolve
// against the working directory.
func TestSelect_refusesARelativeCommand(t *testing.T) {
	dir := t.TempDir()
	executable(t, dir, "bin/deadset-go")
	t.Chdir(dir)

	got, err := providers.Select(resolvedList(t, list(entry("deadset-go", "bin/deadset-go", "go"))), []string{"go"})
	if got != nil {
		t.Errorf("Select(go) chose %v, want nothing", selection(got))
	}
	e, ok := errors.AsType[*providers.CommandError](err)
	if !ok || !errors.Is(err, providers.ErrRelativeCommand) {
		t.Fatalf("Select(go) = error %v, want a *providers.CommandError for providers.ErrRelativeCommand", err)
	}
	if e.Name != "deadset-go" || e.Index != 0 {
		t.Errorf("Select(go) refused %s[%d], want deadset-go[0]", e.Name, e.Index)
	}
}

// joined returns the errors err joins, or err alone.
func joined(err error) []error {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	return []error{err}
}
