package main

import (
	"os/exec"
	"path/filepath"
	"testing"

	spec "github.com/cplieger/deadset-spec/v8"
	"github.com/cplieger/deadset-spec/v8/corpus"
	"github.com/cplieger/deadset/v2/internal/run"
)

// TestCommandLineVectors runs every command-line vector naming this product
// through the built command; a pending case reports what it awaits.
func TestCommandLineVectors(t *testing.T) {
	binary := filepath.Join(t.TempDir(), run.Product)
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("Setup: build the command: %v\n%s", err, out)
	}
	results, err := corpus.RunCases(t.Context(), spec.Vectors, run.Product, corpus.Analyzer{Command: []string{binary}})
	if err != nil {
		t.Fatalf("corpus.RunCases = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("no command-line vector names this product")
	}
	for _, result := range results {
		if result.Result == corpus.Fail {
			t.Errorf("%s: %s", result.Case, result.Message)
		}
	}
}
