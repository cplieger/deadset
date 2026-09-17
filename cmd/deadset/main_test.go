package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantStdout []string
		wantStderr []string
	}{
		{
			name:       "version",
			args:       []string{"version"},
			wantExit:   exitOK,
			wantStdout: []string{"deadset 0.1.0-dev\n", "contract 0.1.0\n"},
		},
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
			name:       "analyze_not_implemented",
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
			got := run(tt.args, &stdout, &stderr)
			if got != tt.wantExit {
				t.Errorf("run(%q) = %d, want %d\nstdout: %q\nstderr: %q", tt.args, got, tt.wantExit, stdout.String(), stderr.String())
			}
			for _, want := range tt.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("run(%q) stdout = %q, want it to contain %q", tt.args, stdout.String(), want)
				}
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("run(%q) stderr = %q, want it to contain %q", tt.args, stderr.String(), want)
				}
			}
			if tt.wantExit == exitOK && stderr.Len() != 0 {
				t.Errorf("run(%q) stderr = %q, want empty on exit 0", tt.args, stderr.String())
			}
			if tt.wantExit != exitOK && stdout.Len() != 0 {
				t.Errorf("run(%q) stdout = %q, want empty on exit %d", tt.args, stdout.String(), tt.wantExit)
			}
		})
	}
}

func TestVersionOutputIsExact(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if got := run([]string{"version"}, &stdout, &stderr); got != exitOK {
		t.Fatalf("run([version]) = %d, want %d; stderr: %q", got, exitOK, stderr.String())
	}
	const want = "deadset 0.1.0-dev\ncontract 0.1.0\n"
	if stdout.String() != want {
		t.Errorf("run([version]) stdout = %q, want %q", stdout.String(), want)
	}
}
