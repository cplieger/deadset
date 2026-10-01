package main

import (
	"bytes"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/deadset/internal/verdict"
)

// TestDispatch pins how a command line reaches a verb over a build whose only
// handler is version, so a verb file a later build adds changes none of these
// outcomes.
func TestDispatch(t *testing.T) {
	t.Parallel()

	onlyVersion := map[string]handler{"version": runVersion}

	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantStdout []string
		wantStderr []string
	}{
		{
			name:       "no_arguments",
			args:       nil,
			wantExit:   verdict.Usage,
			wantStderr: []string{"usage: deadset", "analyze", "explain", "print-config", "install", "describe", "version"},
		},
		{
			name:       "unknown_command",
			args:       []string{"frobnicate"},
			wantExit:   verdict.Usage,
			wantStderr: []string{`unknown command "frobnicate"`, "usage: deadset"},
		},
		{
			name:       "listed_verb_without_a_handler",
			args:       []string{"analyze", "."},
			wantExit:   verdict.Usage,
			wantStderr: []string{`unknown command "analyze"`, "usage: deadset"},
		},
		{
			name:       "undefined_flag",
			args:       []string{"--verbose", "version"},
			wantExit:   verdict.Usage,
			wantStderr: []string{"flag provided but not defined: -verbose", "usage: deadset"},
		},
		{
			name:       "fix_after_command",
			args:       []string{"analyze", "--fix"},
			wantExit:   verdict.Usage,
			wantStderr: []string{"--fix requested a source edit", "report-only"},
		},
		{
			name:       "fix_before_command",
			args:       []string{"-fix", "analyze"},
			wantExit:   verdict.Usage,
			wantStderr: []string{"-fix requested a source edit", "report-only"},
		},
		{
			name:       "fix_with_value",
			args:       []string{"analyze", "--fix=true"},
			wantExit:   verdict.Usage,
			wantStderr: []string{"--fix=true requested a source edit", "report-only"},
		},
		{
			name:       "fix_with_version",
			args:       []string{"version", "--fix"},
			wantExit:   verdict.Usage,
			wantStderr: []string{"--fix requested a source edit"},
		},
		{
			name:       "fix_as_positional_is_a_command",
			args:       []string{"fix"},
			wantExit:   verdict.Usage,
			wantStderr: []string{`unknown command "fix"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			got := dispatch(onlyVersion, tt.args, &stdout, &stderr)
			if got != tt.wantExit {
				t.Errorf("dispatch(%q) = %d, want %d\nstdout: %q\nstderr: %q", tt.args, got, tt.wantExit, stdout.String(), stderr.String())
			}
			for _, want := range tt.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("dispatch(%q) stdout = %q, want it to contain %q", tt.args, stdout.String(), want)
				}
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("dispatch(%q) stderr = %q, want it to contain %q", tt.args, stderr.String(), want)
				}
			}
			if tt.wantExit == verdict.Clean && stderr.Len() != 0 {
				t.Errorf("dispatch(%q) stderr = %q, want empty on exit 0", tt.args, stderr.String())
			}
			if tt.wantExit != verdict.Clean && stdout.Len() != 0 {
				t.Errorf("dispatch(%q) stdout = %q, want empty on exit %d", tt.args, stdout.String(), tt.wantExit)
			}
		})
	}
}

// TestTheUsageTextListsEveryVerbAndNamesTheImplementedOnes pins the whole usage
// text and how its closing line names the implemented verbs, over fixed handler
// sets, so registering a verb changes the closing line and nothing else.
func TestTheUsageTextListsEveryVerbAndNamesTheImplementedOnes(t *testing.T) {
	t.Parallel()

	const listing = `usage: deadset <command> [arguments]

commands:
  analyze       find dead code in a repository and merge every analyzer's report
  explain       say why one symbol is or is not reported
  print-config  print the resolved configuration and where each setting came from
  install       install the analyzers the provider list names
  describe      print this build's capabilities as JSON
  version       print the version of this build and of the contract it implements
`
	stub := func([]string, io.Writer, io.Writer) int { return verdict.Clean }
	for name, tt := range map[string]struct {
		verbs map[string]handler
		want  string
	}{
		"one implemented verb": {
			verbs: map[string]handler{"version": stub},
			want:  listing + "\nOnly version is implemented in this build.\n",
		},
		"two implemented verbs": {
			verbs: map[string]handler{"version": stub, "print-config": stub},
			want:  listing + "\nOnly print-config and version are implemented in this build.\n",
		},
		"three implemented verbs, named in the listing's order": {
			verbs: map[string]handler{"version": stub, "analyze": stub, "describe": stub},
			want:  listing + "\nOnly analyze, describe and version are implemented in this build.\n",
		},
		"every verb implemented": {
			verbs: map[string]handler{
				"analyze": stub, "explain": stub, "print-config": stub,
				"install": stub, "describe": stub, "version": stub,
			},
			want: listing,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := usage(tt.verbs); got != tt.want {
				t.Errorf("usage(%v) =\n%s\nwant\n%s", slices.Sorted(maps.Keys(tt.verbs)), got, tt.want)
			}
		})
	}
}

// TestRegisterRefusesAVerbTheCommandListDoesNotName pins the two mistakes a
// verb file can make in its registration, each of which must stop the build's
// initialization rather than leave a verb the usage text cannot reach. It runs
// serially because a register that failed to refuse would write the map every
// other test reads.
func TestRegisterRefusesAVerbTheCommandListDoesNotName(t *testing.T) {
	for name, verb := range map[string]string{
		"a name the command list does not hold": "print_config",
		"a verb that already has a handler":     "version",
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("register(%q) returned, want a panic", verb)
				}
			}()
			register(verb, runVersion)
		})
	}
}
