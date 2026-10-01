package invoke_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	spec "github.com/cplieger/deadset-spec/v3"
	"github.com/cplieger/deadset/internal/invoke"
)

// The report documents the fake analyzers write: one the Contract publishes,
// and two it was edited into.
const (
	oneReport  = "vectors/merge/one-report/inputs/00-go.json"
	tsReport   = "vectors/merge/two-reports-no-edges/inputs/01-ts.json"
	undeclared = "undeclared"
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

// truncated is the first half of a published report, the document an analyzer
// that died mid-write leaves.
func truncated(t *testing.T) []byte {
	t.Helper()

	body := published(t, oneReport)
	return body[:len(body)/2]
}

// withUndeclaredMember is a published report carrying one member the schema
// does not declare.
func withUndeclaredMember(t *testing.T) []byte {
	t.Helper()

	body := published(t, oneReport)
	return []byte(`{"` + undeclared + `": true,` + string(body[1:]))
}

// fake is an executable standing in for an analyzer, and the files it leaves.
// Before it does anything else, the fake writes its arguments to args, one to
// a line, and the directory it runs in to dir.
type fake struct {
	command string
	args    string
	dir     string
}

// fakeAnalyzer writes a fake analyzer that copies written to the path its
// --report argument names, unless written is nil, and exits with exit.
func fakeAnalyzer(t *testing.T, exit int, written []byte) fake {
	t.Helper()

	dir := t.TempDir()
	write := ""
	if written != nil {
		document := filepath.Join(dir, "written.json")
		if err := os.WriteFile(document, written, 0o600); err != nil {
			t.Fatalf("Setup: write %s: %v", document, err)
		}
		write = fmt.Sprintf("cp '%s' \"$report\"\n", document)
	}
	return script(t, dir, write+fmt.Sprintf("exit %d\n", exit))
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

// request is an invocation of command as the entry named analyzer, every path
// in a fresh directory, which is also the directory the analyzer runs in.
func request(t *testing.T, analyzer, command string) invoke.Request {
	t.Helper()

	dir := t.TempDir()
	return invoke.Request{
		Analyzer: analyzer,
		Command:  command,
		Dir:      dir,
		Scope:    filepath.Join(dir, "scope.json"),
		Config:   filepath.Join(dir, "config."+analyzer+".json"),
		Report:   filepath.Join(dir, "report."+analyzer+".json"),
	}
}
