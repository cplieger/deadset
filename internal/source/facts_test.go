package source_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/plan"
	"github.com/cplieger/deadset/v2/internal/source"
)

// askedForFacts is the process source of command asked for facts, as a run
// with a schema language in scope opens it.
func askedForFacts(t *testing.T, command string) *source.Process {
	t.Helper()

	planned := &plan.Source{Entry: config.Provider{Name: "deadset-go", Command: command}, Languages: []vocab.Language{vocab.Go}, Facts: true}
	p, err := source.Open(planned, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Setup: Open(%s) = %v", command, err)
	}
	return p
}

// An analyzer asked for facts whose describe document names no facts version
// written is refused at the handshake.
func TestAdmitRefusesAnAnalyzerAskedForFactsThatWritesNone(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	described := filepath.Join(dir, "described.json")
	writeFile(t, described, describedAs("deadset-go", `["go"]`, `["`+vocab.SchemaVersion+`"]`, "pass"))
	analyzer := script(t, dir, fmt.Sprintf("cat '%s'\nexit 0\n", described))
	err := askedForFacts(t, analyzer.command).Admit(t.Context(), kept(t, "deadset-go"), []string{vocab.SchemaVersion})
	if !errors.Is(err, source.ErrFactsVersion) {
		t.Fatalf("Admit() = %v, want ErrFactsVersion", err)
	}
}

// An analyzer handed --facts that writes its report and no facts document
// leaves no report the run may use; one that writes a facts document has it
// read.
func TestProduceReadsTheFactsDocumentItAskedFor(t *testing.T) {
	t.Parallel()

	paths := files(t)
	paths.Facts = filepath.Join(t.TempDir(), "facts.json")
	written := filepath.Join(t.TempDir(), "report.json")
	writeFile(t, written, published(t, goReport))
	dir := t.TempDir()
	analyzer := script(t, dir, fmt.Sprintf("cp '%s' \"$report\"\nexit 0\n", written))
	out := askedForFacts(t, analyzer.command).Produce(t.Context(), paths)
	if !errors.Is(out.Err, source.ErrNoFacts) || out.Report != nil {
		t.Fatalf("Produce() = %v, report %v; want ErrNoFacts and no report", out.Err, out.Report != nil)
	}
	if args, _ := os.ReadFile(analyzer.args); !strings.Contains(string(args), "--facts="+paths.Facts+"\n") {
		t.Errorf("Produce() ran the analyzer with %q, want --facts=%s", args, paths.Facts)
	}
}
