package source_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/failure"
	"github.com/cplieger/deadset/v2/internal/plan"
	"github.com/cplieger/deadset/v2/internal/scope"
	"github.com/cplieger/deadset/v2/internal/source"
)

// goRun opens the Go analyzer PATH holds over a module whose one file holds
// text, under target/ in a fresh directory it runs in, admitted, with the
// files it is handed written: a scope document naming the module and the
// configuration split from one that names renderings and a maximum finding
// count. It skips the test when PATH holds no Go analyzer.
func goRun(t *testing.T, text string) (*source.Process, source.Files) {
	t.Helper()

	if _, err := exec.LookPath("deadset-go"); err != nil {
		t.Skipf("a run of a real analyzer needs deadset-go on PATH: %v", err)
	}
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	writeFile(t, filepath.Join(target, "go.mod"), []byte("module example.com/app\n\ngo 1.27\n"))
	writeFile(t, filepath.Join(target, "app.go"), []byte(text))
	resolved, err := config.Resolve(&config.Inputs{
		Repository: config.Document{
			Path: filepath.Join(target, config.RepositoryFile),
			Data: []byte(`{"target": {"kind": "library"}, "reporters": {"formats": ["json", "github"], "max_findings": 1}}`),
		},
	})
	if err != nil {
		t.Fatalf("Setup: resolve the configuration: %v", err)
	}
	entry := kept(t, "deadset-go")
	planned := &plan.Source{Entry: config.Provider{Name: "deadset-go", Command: "deadset-go"}, Languages: []vocab.Language{vocab.Go}}
	p, err := source.Open(planned, base, nil)
	if err != nil {
		t.Fatalf("Setup: Open(deadset-go) = %v", err)
	}
	if err := p.Admit(t.Context(), entry, vocab.SchemaVersions()); err != nil {
		t.Fatalf("Admit(deadset-go) = %v, want the Go analyzer admitted", err)
	}
	document, err := (&scope.Document{Target: scope.Module{ID: "example.com/app", Path: target}}).Encode()
	if err != nil {
		t.Fatalf("Setup: encode the scope: %v", err)
	}
	split, err := resolved.Split("go")
	if err != nil {
		t.Fatalf("Setup: split the configuration: %v", err)
	}
	files := source.Files{Scope: filepath.Join(filepath.Dir(entry.Config()), "scope.json"), Config: entry.Config(), Report: entry.Report()}
	writeFile(t, files.Scope, document)
	writeFile(t, files.Config, split)
	return p, files
}

// The Go analyzer, admitted, analyzes a module with two dead functions with
// the configuration it is handed and the report it writes is read: the
// target the scope names, the kind the configuration declares, the root
// relative to the directory it ran in, and both findings although the target
// configures a maximum of one.
func TestProduceReadsTheReportARealAnalyzerWrites(t *testing.T) {
	t.Parallel()

	p, files := goRun(t, "package app\n\n// Used is called.\nfunc Used() int { return 1 }\n\nfunc dead() {}\n\nfunc alsoDead() {}\n")
	got := p.Produce(t.Context(), files)
	if got.Err != nil || got.Report == nil {
		t.Fatalf("Produce(deadset-go) = %v\n%s", got.Err, got.Printed)
	}
	want := report.Target{Kind: report.TargetKindLibrary, Root: "target", Identity: "example.com/app"}
	if !reflect.DeepEqual(got.Report.Target, want) || got.Report.Totals.Omitted != 0 {
		t.Errorf("Produce() read a report on %+v omitting %d, want %+v and none omitted", got.Report.Target, got.Report.Totals.Omitted, want)
	}
	var reported []string
	for i := range got.Report.Findings {
		reported = append(reported, string(got.Report.Findings[i].Code)+" "+got.Report.Findings[i].Symbol.Ref)
	}
	for _, want := range []string{"DS1002 go://example.com/app#dead", "DS1002 go://example.com/app#alsoDead"} {
		if !slices.Contains(reported, want) {
			t.Errorf("Produce() read the findings %q, want %s", reported, want)
		}
	}
}

// The Go analyzer over a module importing a package nothing provides exits
// with the failure code, and its setup-failure line is relayed as its class.
func TestRunRelaysARealAnalyzersSetupFailure(t *testing.T) {
	t.Parallel()

	p, files := goRun(t, "package app\n\nimport \"example.com/app/gen\"\n\nvar V = gen.X\n")
	outcomes, err := source.Run(t.Context(), []source.Source{p}, []source.Files{files}, false)
	got := outcomes[0]
	relayed, ok := errors.AsType[*failure.Error](err)
	if got.Report != nil || got.Exit != vocab.ExitFailure || !got.SetupFailed() || !ok ||
		relayed.Failures[0].Class != vocab.SetupMissingModule || !strings.HasPrefix(relayed.Failures[0].Detail, "app.go:") {
		t.Errorf("Run(deadset-go over a missing module) = exit %d, printed %q, %v, want the setup failure missing-module naming app.go", got.Exit, got.Printed, err)
	}
}
