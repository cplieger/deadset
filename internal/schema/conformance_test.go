package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	spec "github.com/cplieger/deadset-spec/v8"
	"github.com/cplieger/deadset-spec/v8/corpus"
	"github.com/cplieger/deadset-spec/v8/facts"
	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/suppress"
	"github.com/cplieger/deadset-spec/v8/vocab"
)

// testVersion is the version the reader answers the corpus at, so the
// results document is the same at every build.
const testVersion = "0.0.0-devel"

const resultsPath = "reader-results.json"

// TestConformanceCorpus answers every corpus fixture listing proto through
// the reader in this process, and holds the run to the committed results
// document. UPDATE_GOLDEN=1 writes the document instead.
func TestConformanceCorpus(t *testing.T) {
	results, err := corpus.Run(t.Context(), spec.Corpus, corpus.Analyzer{Language: string(vocab.Proto), InProcess: answer},
		corpus.Options{Jobs: runtime.GOMAXPROCS(0), Scratch: t.TempDir()})
	if err != nil {
		t.Fatalf("corpus.Run = %v", err)
	}
	got, err := corpus.Encode(results)
	if err != nil {
		t.Fatalf("corpus.Encode: %v", err)
	}
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(resultsPath, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range results.Fixtures {
		if row.Result == corpus.Fail {
			t.Errorf("%s: %s", row.Fixture, row.Message)
		}
		for _, held := range row.Expectations {
			if held.Result == corpus.Fail {
				t.Errorf("%s %s: %s", row.Fixture, held.Symbol, held.Message)
			}
		}
		for _, one := range row.Unexpected {
			t.Errorf("%s: %s at %s:%d, which no expectation names", row.Fixture, one.Report, one.File, one.Line)
		}
	}
	if results.Result != corpus.Pass {
		t.Errorf("the corpus result is %s, want pass", results.Result)
	}
	if !bytes.Equal(got, resultsDocument) {
		t.Errorf("%s differs from this run (regenerate with UPDATE_GOLDEN=1 go test ./internal/schema -run TestConformanceCorpus)", resultsPath)
	}
}

// answer is the reader's command line as the corpus runner drives it:
// describe, analyze and print-retained.
func answer(ctx context.Context, dir string, args []string) ([]byte, string, int) {
	if len(args) == 0 {
		return nil, "no verb", vocab.ExitUsage
	}
	switch args[0] {
	case "describe":
		out, _ := json.Marshal(map[string]any{"name": Name, "version": testVersion}) //nolint:errchkjson // strings only
		return out, "", vocab.ExitClean
	case "print-retained":
		return nil, "", vocab.ExitClean
	case "analyze":
		var stderr bytes.Buffer
		code := analyze(ctx, dir, args[1:], &stderr)
		return nil, stderr.String(), code
	}
	return nil, "unknown verb " + args[0], vocab.ExitUsage
}

func analyze(ctx context.Context, dir string, args []string, stderr io.Writer) int {
	set := flag.NewFlagSet("analyze", flag.ContinueOnError)
	target, config, reportPath, factsPath := set.String("target", ".", ""), set.String("config", "", ""), set.String("report", "", ""), set.String("facts", "", "")
	set.String("scope", "", "")
	if err := set.Parse(args); err != nil {
		return vocab.ExitUsage
	}
	data, err := os.ReadFile(*config)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return vocab.ExitFailure
	}
	settings, err := ParseSettings(data)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return vocab.ExitUsage
	}
	conformance, err := Conformance()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return vocab.ExitFailure
	}
	result, err := Read(ctx, &Input{Target: filepath.Join(dir, *target), Root: *target, Version: testVersion, Conformance: conformance, Settings: settings})
	var setup *SetupError
	switch {
	case errors.As(err, &setup):
		fmt.Fprintf(stderr, "%s%s\n", vocab.SetupFailurePrefix, setup.Error())
		return vocab.ExitFailure
	case errors.Is(err, suppress.ErrMalformed):
		fmt.Fprintln(stderr, err)
		return vocab.ExitUsage
	case err != nil:
		fmt.Fprintln(stderr, err)
		return vocab.ExitFailure
	}
	if err := write(*reportPath, func(w io.Writer) error { return report.Encode(w, result.Report) }); err != nil {
		fmt.Fprintln(stderr, err)
		return vocab.ExitFailure
	}
	if *factsPath != "" {
		if err := write(*factsPath, func(w io.Writer) error { return facts.Encode(w, result.Facts) }); err != nil {
			fmt.Fprintln(stderr, err)
			return vocab.ExitFailure
		}
	}
	if len(result.Report.Findings) > 0 {
		return vocab.ExitFindings
	}
	return vocab.ExitClean
}

func write(path string, encode func(io.Writer) error) error {
	var b bytes.Buffer
	if err := encode(&b); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o600)
}
