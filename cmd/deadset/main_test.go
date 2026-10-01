package main

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"

	spec "github.com/cplieger/deadset-spec/v3"
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
			wantExit:   exitUsage,
			wantStderr: []string{"usage: deadset", "analyze", "explain", "print-config", "install", "describe", "version"},
		},
		{
			name:       "unknown_command",
			args:       []string{"frobnicate"},
			wantExit:   exitUsage,
			wantStderr: []string{`unknown command "frobnicate"`, "usage: deadset"},
		},
		{
			name:       "listed_verb_without_a_handler",
			args:       []string{"analyze", "."},
			wantExit:   exitUsage,
			wantStderr: []string{`unknown command "analyze"`, "usage: deadset"},
		},
		{
			name:       "undefined_flag",
			args:       []string{"--verbose", "version"},
			wantExit:   exitUsage,
			wantStderr: []string{"flag provided but not defined: -verbose", "usage: deadset"},
		},
		{
			name:       "fix_after_command",
			args:       []string{"analyze", "--fix"},
			wantExit:   exitUsage,
			wantStderr: []string{"--fix requested a source edit", "report-only"},
		},
		{
			name:       "fix_before_command",
			args:       []string{"-fix", "analyze"},
			wantExit:   exitUsage,
			wantStderr: []string{"-fix requested a source edit", "report-only"},
		},
		{
			name:       "fix_with_value",
			args:       []string{"analyze", "--fix=true"},
			wantExit:   exitUsage,
			wantStderr: []string{"--fix=true requested a source edit", "report-only"},
		},
		{
			name:       "fix_with_version",
			args:       []string{"version", "--fix"},
			wantExit:   exitUsage,
			wantStderr: []string{"--fix requested a source edit"},
		},
		{
			name:       "fix_as_positional_is_a_command",
			args:       []string{"fix"},
			wantExit:   exitUsage,
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
			if tt.wantExit == exitClean && stderr.Len() != 0 {
				t.Errorf("dispatch(%q) stderr = %q, want empty on exit 0", tt.args, stderr.String())
			}
			if tt.wantExit != exitClean && stdout.Len() != 0 {
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
	stub := func([]string, io.Writer, io.Writer) int { return exitClean }
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

// TestTheExitCodesAreTheContractsTable pins every exit code this command
// returns to the code contract/exit-codes.json gives the same name.
func TestTheExitCodesAreTheContractsTable(t *testing.T) {
	t.Parallel()

	body, err := spec.Contract.ReadFile("contract/exit-codes.json")
	if err != nil {
		t.Fatalf("Setup: read contract/exit-codes.json: %v", err)
	}
	var table struct {
		ExitCodes []struct {
			Name string `json:"name"`
			Code int    `json:"code"`
		} `json:"exit_codes"`
	}
	if err := json.Unmarshal(body, &table); err != nil {
		t.Fatalf("Setup: decode contract/exit-codes.json: %v", err)
	}
	contract := make(map[string]int, len(table.ExitCodes))
	for _, row := range table.ExitCodes {
		contract[row.Name] = row.Code
	}

	ours := map[string]int{
		"clean":    exitClean,
		"findings": exitFindings,
		"usage":    exitUsage,
		"failure":  exitFailure,
		"pending":  exitPending,
	}
	if !maps.Equal(ours, contract) {
		t.Errorf("exit codes = %v, want contract/exit-codes.json's %v", ours, contract)
	}
}
