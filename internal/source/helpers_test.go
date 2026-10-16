package source_test

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	spec "github.com/cplieger/deadset-spec/v8"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/plan"
	"github.com/cplieger/deadset/v2/internal/rundir"
	"github.com/cplieger/deadset/v2/internal/source"
)

// The report documents the fake analyzers write, each a published merge
// input.
const (
	goReport = "vectors/merge/one-report/inputs/00-go.json"
	tsReport = "vectors/merge/two-reports-no-edges/inputs/01-ts.json"
)

// published is a report document the Contract publishes.
func published(t *testing.T, name string) []byte {
	t.Helper()

	body, err := fs.ReadFile(spec.Vectors, name)
	if err != nil {
		t.Fatalf("Setup: read %s: %v", name, err)
	}
	return body
}

// fake is an executable standing in for an analyzer, and the files it leaves.
// Before it does anything else, the fake writes its arguments to args, one to
// a line, and the directory it runs in to dir.
type fake struct {
	command string
	args    string
	dir     string
}

// fakeAnalyzer writes a fake analyzer whose describe verb prints described and
// whose analyze verb copies written to the path its --report argument names,
// unless written is nil, and exits with exit.
func fakeAnalyzer(t *testing.T, described []byte, exit int, written []byte) fake {
	t.Helper()

	dir := t.TempDir()
	body := ""
	if described != nil {
		describedFile := filepath.Join(dir, "described.json")
		writeFile(t, describedFile, described)
		body += fmt.Sprintf("if [ \"$1\" = describe ]; then cat '%s'; echo 'a diagnostic' >&2; exit 0; fi\n", describedFile)
	}
	if written != nil {
		document := filepath.Join(dir, "written.json")
		writeFile(t, document, written)
		body += fmt.Sprintf("cp '%s' \"$report\"\n", document)
	}
	return script(t, dir, body+fmt.Sprintf("exit %d\n", exit))
}

func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()

	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("Setup: write %s: %v", path, err)
	}
}

// script writes a fake analyzer into dir whose body runs after the fake has
// recorded its arguments and set report to the path --report names.
func script(t *testing.T, dir, body string) fake {
	t.Helper()

	f := fake{command: filepath.Join(dir, "analyzer"), args: filepath.Join(dir, "args"), dir: filepath.Join(dir, "pwd")}
	text := strings.Join([]string{
		"#!/bin/sh",
		fmt.Sprintf(`printf '%%s\n' "$@" > '%s'`, f.args),
		fmt.Sprintf(`pwd > '%s'`, f.dir),
		`for argument in "$@"; do`,
		`	case "$argument" in --report=*) report="${argument#--report=}" ;; esac`,
		`done`,
		body,
	}, "\n")
	// A fork in a parallel test inherits the descriptor the script is written
	// through, and the script's exec then fails with ETXTBSY until that child
	// execs (go.dev/issue/22315); a fork waits for every ForkLock reader.
	syscall.ForkLock.RLock()
	err := os.WriteFile(f.command, []byte(text), 0o700)
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatalf("Setup: write %s: %v", f.command, err)
	}
	return f
}

// opened is the process source of command as the entry named name, handed
// languages, running in a fresh directory; diagnostics receives the describe
// verb's standard error.
func opened(t *testing.T, name, command string, diagnostics io.Writer, languages ...vocab.Language) *source.Process {
	t.Helper()

	planned := &plan.Source{Entry: config.Provider{Name: name, Command: command}, Languages: languages}
	p, err := source.Open(planned, t.TempDir(), diagnostics)
	if err != nil {
		t.Fatalf("Setup: Open(%s) = %v", command, err)
	}
	return p
}

// files is a fresh set of the paths one source is handed.
func files(t *testing.T) source.Files {
	t.Helper()

	dir := t.TempDir()
	return source.Files{
		Scope:  filepath.Join(dir, "scope.json"),
		Config: filepath.Join(dir, "config.json"),
		Report: filepath.Join(dir, "report.json"),
	}
}

// kept is the files of the provider entry named name in a fresh run directory.
func kept(t *testing.T, name string) rundir.Entry {
	t.Helper()

	dir, err := rundir.Create(filepath.Join(t.TempDir(), "run"))
	if err != nil {
		t.Fatalf("Setup: create the run directory: %v", err)
	}
	entry, err := dir.Entry(name)
	if err != nil {
		t.Fatalf("Setup: Entry(%q): %v", name, err)
	}
	return entry
}
