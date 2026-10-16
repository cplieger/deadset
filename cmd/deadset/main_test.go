package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/cplieger/deadset-spec/v8/vocab"
)

// invoke runs a command line the way main does.
func invoke(args []string, stdout, stderr io.Writer) int {
	return (&command{}).dispatch(args, stdout, stderr)
}

// How a command line reaches a verb: the verbs are cli.json's, every other
// name is unknown, and a verb refuses an option its row does not list, a
// request to edit source among them, and an argument.
func TestDispatch(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		args       []string
		wantExit   int
		wantStdout string
		wantStderr []string
	}{
		{name: "no-arguments", wantExit: vocab.ExitUsage, wantStderr: []string{"usage: deadset <command>", "analyze", "print-config", "version"}},
		{name: "unknown-command", args: []string{"frobnicate"}, wantExit: vocab.ExitUsage, wantStderr: []string{`unknown command "frobnicate"`, "usage: deadset <command>"}},
		{name: "a-verb-cli-json-does-not-list", args: []string{"explain"}, wantExit: vocab.ExitUsage, wantStderr: []string{`unknown command "explain"`}},
		{name: "undefined-flag-before-the-verb", args: []string{"--verbose", "version"}, wantExit: vocab.ExitUsage, wantStderr: []string{"-verbose", "usage: deadset <command>"}},
		{name: "fix-after-the-verb", args: []string{"analyze", "--fix=true"}, wantExit: vocab.ExitUsage, wantStderr: []string{"-fix", "usage: deadset analyze"}},
		{name: "fix-before-the-verb", args: []string{"-fix", "analyze"}, wantExit: vocab.ExitUsage, wantStderr: []string{"-fix", "usage: deadset <command>"}},
		{name: "version-refuses-an-option", args: []string{"version", "--target=."}, wantExit: vocab.ExitUsage, wantStderr: []string{"-target", "usage: deadset version"}},
		{name: "version-refuses-an-argument", args: []string{"version", "extra"}, wantExit: vocab.ExitUsage, wantStderr: []string{`version takes no argument, got "extra"`}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			got := invoke(tt.args, &stdout, &stderr)
			if got != tt.wantExit || stdout.String() != tt.wantStdout {
				t.Errorf("run(%q) = %d, stdout %q, want %d and %q\nstderr: %q", tt.args, got, stdout.String(), tt.wantExit, tt.wantStdout, stderr.String())
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("run(%q) stderr = %q, want it to contain %q", tt.args, stderr.String(), want)
				}
			}
		})
	}
}

// Every verb's flag set defines exactly the options its row of cli.json
// lists, and its usage text names each with the row's summary.
func TestEveryVerbDefinesTheOptionsItsRowLists(t *testing.T) {
	t.Parallel()

	for i := range verbs {
		verb := &verbs[i]
		(&command{}).flags(verb)
		text := verbUsage(verb)
		if !strings.HasPrefix(text, "usage: deadset "+verb.Name) || !strings.Contains(text, verb.Summary) {
			t.Errorf("verbUsage(%s) =\n%s\nwant the verb's command line and summary", verb.Name, text)
		}
		for _, option := range verb.Options {
			if !strings.Contains(text, " --"+option+"=") {
				t.Errorf("verbUsage(%s) names no --%s", verb.Name, option)
			}
		}
	}
}
