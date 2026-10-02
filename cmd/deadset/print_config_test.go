package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/deadset/internal/verdict"
)

// targetHolding returns a target root holding repository as its repository
// configuration, or no repository configuration when repository is empty.
func targetHolding(t *testing.T, repository string) string {
	t.Helper()

	dir := t.TempDir()
	if repository != "" {
		if err := os.WriteFile(filepath.Join(dir, "deadset.json"), []byte(repository), 0o600); err != nil {
			t.Fatalf("Setup: write deadset.json: %v", err)
		}
	}
	return dir
}

// print-config prints the resolved configuration with the source of every setting:
// the repository configuration at the target root, a flag over it, and the
// documented default under both.
func TestPrintConfigPrintsTheResolutionAndItsSources(t *testing.T) {
	t.Parallel()

	dir := targetHolding(t, `{"target": {"kind": "library"}, "analysis": {"min_confidence": "probable"}, "reporters": {"fail_on": "deny"}}`)
	var stdout, stderr bytes.Buffer
	args := []string{"print-config", "--target=" + dir, "--fail-on=warn"}
	if code := run(args, &stdout, &stderr); code != verdict.Clean {
		t.Fatalf("run(%q) = %d, want %d\nstderr: %s", args, code, verdict.Clean, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("run(%q) stderr = %q, want empty", args, stderr.String())
	}
	var printed struct {
		Reporters struct {
			FailOn string `json:"fail_on"`
		} `json:"reporters"`
		Provenance map[string]string `json:"provenance"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &printed); err != nil {
		t.Fatalf("run(%q) printed %q, which does not decode: %v", args, stdout.String(), err)
	}
	repository := "repository: " + filepath.Join(dir, "deadset.json")
	want := map[string]string{
		"target.kind":             repository,
		"analysis.min_confidence": repository,
		"reporters.fail_on":       "flag: --fail-on",
		"reporters.sort":          "default",
	}
	for path, source := range want {
		if got := printed.Provenance[path]; got != source {
			t.Errorf("run(%q) provenance of %s = %q, want %q", args, path, got, source)
		}
	}
	if printed.Reporters.FailOn != "warn" {
		t.Errorf("run(%q) reporters.fail_on = %q, want the flag's warn", args, printed.Reporters.FailOn)
	}
}

// A target that is not a directory is a failure, not a usage error: the run
// had a target to read and could not read it, which no configuration fixes,
// and a central configuration supplying the target kind changes nothing.
func TestPrintConfigFailsOnATargetItCannotRead(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("Setup: write %s: %v", file, err)
	}
	central := filepath.Join(t.TempDir(), "central.json")
	if err := os.WriteFile(central, []byte(`{"target": {"kind": "library"}}`), 0o600); err != nil {
		t.Fatalf("Setup: write %s: %v", central, err)
	}
	for name, target := range map[string]string{
		"absent":        filepath.Join(t.TempDir(), "absent"),
		"not-directory": file,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			args := []string{"print-config", "--target=" + target, "--central=" + central}
			if code := run(args, &stdout, &stderr); code != verdict.Failure {
				t.Errorf("run(%q) = %d, want %d\nstderr: %s", args, code, verdict.Failure, stderr.String())
			}
			if !strings.Contains(stderr.String(), "target") || strings.Contains(stderr.String(), "usage:") {
				t.Errorf("run(%q) stderr = %q, want it to name the target and print no usage", args, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("run(%q) stdout = %q, want no configuration printed", args, stdout.String())
			}
		})
	}
}

// A configuration print-config refuses exits with the usage code, naming the key,
// and prints no configuration: a target whose sources supply no target kind is
// refused rather than printed with a guessed one, a configuration absent included.
func TestPrintConfigRefusesWithTheUsageCode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		repository string
		extra      []string
		names      string
	}{
		{name: "language-in-scope-without-a-target-kind", repository: `{"analysis": {"languages": ["go"]}}`, names: "target.kind"},
		{name: "no-configuration-at-all", names: "target.kind"},
		{name: "unimplemented-key", repository: `{"target": {"kind": "library"}, "reporters": {"fail_under": "warn"}}`, names: "reporters.fail_under"},
		{name: "flag-outside-its-set", repository: `{"target": {"kind": "library"}}`, extra: []string{"--fail-on=loud"}, names: "reporters.fail_on"},
		{name: "central-configuration-absent", repository: `{"target": {"kind": "library"}}`, extra: []string{"--central=" + filepath.Join(t.TempDir(), "central.json")}, names: "central.json"},
		{name: "argument", repository: `{"target": {"kind": "library"}}`, extra: []string{"extra"}, names: `"extra"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			args := append([]string{"print-config", "--target=" + targetHolding(t, c.repository)}, c.extra...)
			if code := run(args, &stdout, &stderr); code != verdict.Usage {
				t.Errorf("run(%q) = %d, want %d\nstderr: %s", args, code, verdict.Usage, stderr.String())
			}
			if !strings.Contains(stderr.String(), c.names) || !strings.Contains(stderr.String(), "usage: deadset print-config") {
				t.Errorf("run(%q) stderr = %q, want it to name %s and print the usage", args, stderr.String(), c.names)
			}
			if stdout.Len() != 0 {
				t.Errorf("run(%q) stdout = %q, want no configuration printed", args, stdout.String())
			}
		})
	}
}
