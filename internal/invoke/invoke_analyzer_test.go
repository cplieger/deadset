package invoke_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/deadset/internal/config"
	"github.com/cplieger/deadset/internal/invoke"
	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/rundir"
)

// goAnalyzer is the first-party Go analyzer the tests of a real run look up on
// PATH.
const goAnalyzer = "deadset-go"

// goAnalyzerOnPath is the Go analyzer's executable, and skips the test when
// PATH holds none.
func goAnalyzerOnPath(t *testing.T) string {
	t.Helper()

	path, err := exec.LookPath(goAnalyzer)
	if err != nil {
		t.Skipf("a run of a real analyzer needs %s on PATH: %v", goAnalyzer, err)
	}
	return path
}

// goRun lays out a run of command over a Go module whose one file holds
// source: the module under target/, its scope document, and a run directory
// holding what the Go analyzer's entry printed when it described itself and
// the configuration it reads, split from a target configuration that names
// renderings and a maximum finding count. The analyzer runs from the
// directory holding all three.
func goRun(t *testing.T, command, source string) (invoke.Request, rundir.Entry) {
	t.Helper()

	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatalf("Setup: create %s: %v", target, err)
	}
	files := map[string]string{"go.mod": "module example.com/app\n\ngo 1.27\n", "app.go": source}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(target, name), []byte(text), 0o600); err != nil {
			t.Fatalf("Setup: write %s: %v", name, err)
		}
	}
	scope, err := json.Marshal(map[string]any{
		"target": map[string]string{"id": "example.com/app", "role": "target", "path": target},
	})
	if err != nil {
		t.Fatalf("Setup: encode the scope document: %v", err)
	}
	scopePath := filepath.Join(base, "scope.json")
	if err := os.WriteFile(scopePath, scope, 0o600); err != nil {
		t.Fatalf("Setup: write %s: %v", scopePath, err)
	}

	resolved, err := config.Resolve(&config.Inputs{
		ContractVersion: report.ContractVersion,
		Repository: config.Document{
			Path: filepath.Join(target, config.RepositoryFile),
			Data: []byte(`{"target": {"kind": "library"}, "reporters": {"formats": ["json", "github"], "max_findings": 1}}`),
		},
	})
	if err != nil {
		t.Fatalf("Setup: resolve the configuration: %v", err)
	}
	dir, err := rundir.Create(filepath.Join(base, "run"))
	if err != nil {
		t.Fatalf("Setup: create the run directory: %v", err)
	}
	entry, err := dir.Entry(goAnalyzer)
	if err != nil {
		t.Fatalf("Setup: Entry(%s): %v", goAnalyzer, err)
	}
	described, err := exec.CommandContext(t.Context(), command, "describe").Output()
	if err != nil {
		t.Fatalf("Setup: %s describe: %v", goAnalyzer, err)
	}
	if err := entry.WriteDescribe(described); err != nil {
		t.Fatalf("Setup: keep what %s described: %v", goAnalyzer, err)
	}
	if err := entry.WriteConfig(resolved, "go"); err != nil {
		t.Fatalf("Setup: write the configuration of %s: %v", goAnalyzer, err)
	}

	return invoke.Request{
		Analyzer: goAnalyzer,
		Command:  command,
		Dir:      base,
		Scope:    scopePath,
		Config:   entry.Config(),
		Report:   entry.Report(),
	}, entry
}

// TestAnalyzeReadsTheReportARealAnalyzerWrites runs the Go analyzer over a
// module with two dead functions, with the configuration the run directory
// holds, and reads the report it wrote there: the target the scope document
// names, the kind the configuration declares, the root relative to the
// directory the analyzer ran in, both findings although the target configures
// a maximum of one, and the report file kept as the run's evidence beside no
// rendering of the formats the target configures.
func TestAnalyzeReadsTheReportARealAnalyzerWrites(t *testing.T) {
	t.Parallel()

	req, entry := goRun(t, goAnalyzerOnPath(t),
		"package app\n\n// Used is called.\nfunc Used() int { return 1 }\n\nfunc dead() {}\n\nfunc alsoDead() {}\n")
	got, err := invoke.Analyze(t.Context(), &req)
	if err != nil {
		t.Fatalf("Analyze(%s over a module with two dead functions) = %v, want its report", goAnalyzer, err)
	}

	if got.Analyzer.Name != goAnalyzer {
		t.Errorf("Analyze() read a report by %q, want %q", got.Analyzer.Name, goAnalyzer)
	}
	want := report.Target{Kind: report.TargetLibrary, Root: "target", Identity: "example.com/app"}
	if got.Target != want {
		t.Errorf("Analyze() read a report on %+v, want %+v", got.Target, want)
	}
	var reported []string
	for i := range got.Findings {
		reported = append(reported, got.Findings[i].Code+" "+got.Findings[i].Symbol.Ref)
	}
	for _, want := range []string{"DS1002 go://example.com/app#dead", "DS1002 go://example.com/app#alsoDead"} {
		if !slices.Contains(reported, want) {
			t.Errorf("Analyze() read the findings %q, want %s", reported, want)
		}
	}
	if got.Totals.Omitted != 0 {
		t.Errorf("Analyze() read a report omitting %d findings, want none omitted", got.Totals.Omitted)
	}

	kept, err := os.ReadFile(entry.Report())
	if err != nil {
		t.Fatalf("read the report kept at %s: %v", entry.Report(), err)
	}
	if again, err := report.Decode(kept); err != nil || !reflect.DeepEqual(again, got) {
		t.Errorf("the report kept at %s decodes to %+v, %v, want the report Analyze read", entry.Report(), again, err)
	}

	listed, err := os.ReadDir(filepath.Dir(entry.Report()))
	if err != nil {
		t.Fatalf("list the run directory: %v", err)
	}
	var held []string
	for _, file := range listed {
		held = append(held, file.Name())
	}
	// The split withholds the formats, so the analyzer renders the Contract's
	// default, text, and nothing the target configures.
	evidence := []string{"config.deadset-go.json", "describe.deadset-go.json", "report.deadset-go.json", "report.deadset-go.json.txt"}
	if !slices.Equal(held, evidence) {
		t.Errorf("after Analyze(), the run directory holds %q, want %q", held, evidence)
	}
}

// TestRunPresentsNoReportBesideARealAnalyzerFailure runs the Go analyzer over a
// module that does not type-check, beside an analyzer that writes a valid
// report: the Go analyzer exits 3, its load error reaches the diagnostics, and
// the run has no report from either.
func TestRunPresentsNoReportBesideARealAnalyzerFailure(t *testing.T) {
	t.Parallel()

	failing, _ := goRun(t, goAnalyzerOnPath(t), "package app\n\nfunc broken() int { return \"one\" }\n")
	var printed bytes.Buffer
	failing.Diagnostics = &printed
	requests := []invoke.Request{
		request(t, "deadset-ts", fakeAnalyzer(t, 1, published(t, tsReport)).command),
		failing,
	}

	reports, err := invoke.Run(t.Context(), requests)
	if reports != nil {
		t.Errorf("Run(a valid report beside %s failing) = %d reports, want none", goAnalyzer, len(reports))
	}
	found := refusals(t, err)
	if len(found) != 1 || found[0].Analyzer != goAnalyzer || found[0].Exit != 3 {
		t.Errorf("Run() = %v, want one refusal, of %s exiting 3", err, goAnalyzer)
	}
	if !strings.Contains(printed.String(), "app.go") {
		t.Errorf("%s printed %q, want the load error naming app.go", goAnalyzer, printed.String())
	}
}
