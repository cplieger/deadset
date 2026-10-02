package main

import (
	"bytes"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/deadset/internal/verdict"
)

// goAnalyzerOnPath skips a test of a real run when PATH holds no Go analyzer.
func goAnalyzerOnPath(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("deadset-go"); err != nil {
		t.Skipf("a run of the real Go analyzer needs deadset-go on PATH: %v", err)
	}
}

// goModule is a target holding a Go application whose one file holds source,
// under the default provider list, and a run directory path beside it.
func goModule(t *testing.T, source string) (target, runDir string) {
	t.Helper()

	base := t.TempDir()
	target = filepath.Join(base, "target")
	writeFile(t, filepath.Join(target, "go.mod"), []byte("module example.com/app\n\ngo 1.27\n"))
	writeFile(t, filepath.Join(target, "main.go"), []byte(source))
	writeFile(t, filepath.Join(target, "deadset.json"), []byte(`{"target": {"kind": "application"}}`+"\n"))
	return target, filepath.Join(base, "run")
}

// A Go-only module runs the Go analyzer the default provider list names and no
// other: a module with nothing dead exits clean, one with a dead function exits
// with the findings code and prints its line, and each leaves the evidence
// and a merged report the schemas admit.
func TestAnalyzeRunsTheGoAnalyzerOverAGoModule(t *testing.T) {
	t.Parallel()
	goAnalyzerOnPath(t)

	for _, c := range []struct {
		name, source, line string
		code               int
	}{
		{name: "clean", source: "package main\n\nfunc main() { used() }\n\nfunc used() {}\n", code: verdict.Clean},
		{
			name: "dead-function", source: "package main\n\nfunc main() {}\n\nfunc unused() {}\n", code: verdict.Findings,
			line: "main.go:5:6: function unused: unexported function has no reference in the target [certain] (DS1002)\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			target, runDir := goModule(t, c.source)
			got := analyze(t, target, runDir)
			if got.code != c.code {
				t.Fatalf("analyze(%s) = %d, want %d\nstdout: %s\nstderr: %s", c.name, got.code, c.code, got.stdout, got.stderr)
			}
			if !strings.HasPrefix(got.stdout, c.line) || !strings.Contains(got.stdout, "analyzer deadset-go ") {
				t.Errorf("analyze(%s) stdout = %q, want it to open with %q and name the Go analyzer", c.name, got.stdout, c.line)
			}
			want := []string{
				"config.deadset-go.json", "describe.deadset-go.json", "report.deadset-go.json",
				"report.deadset-go.json.txt", "report.json", "scope.json",
			}
			if names := listing(t, runDir); !slices.Equal(names, want) {
				t.Errorf("the run directory holds %q, want %q", names, want)
			}
			merged := mergedReport(t, runDir)
			if len(merged.MergedFrom) != 1 || merged.MergedFrom[0].Name != "deadset-go" || merged.Target.Root != "." {
				t.Errorf("the merged report was merged from %+v over target %+v, want the Go analyzer alone over the root", merged.MergedFrom, merged.Target)
			}
		})
	}
}

// A run of the Go analyzer alone over each harness variant holds the Go side's
// pending finding, and no report of the run evaluates the edge's other side,
// so the run exits with the failure code naming the edge, the pending side and
// its symbol, and presents nothing as its result.
func TestAnalyzeOverTheHarnessGoHalfLeavesThePendingEdgeUnresolved(t *testing.T) {
	t.Parallel()
	goAnalyzerOnPath(t)

	archives, err := filepath.Glob(filepath.Join("..", "..", "testdata", "harness", "*.txtar"))
	if err != nil || len(archives) == 0 {
		t.Fatalf("Setup: glob the harness archives = %v, %v", archives, err)
	}
	for _, archive := range archives {
		t.Run(strings.TrimSuffix(filepath.Base(archive), ".txtar"), func(t *testing.T) {
			t.Parallel()

			base := t.TempDir()
			target := filepath.Join(base, "target")
			extractArchive(t, archive, target)
			got := analyze(t, target, filepath.Join(base, "run"), "--languages=go")
			if got.code != verdict.Failure {
				t.Fatalf("analyze(%s) = %d, want %d\nstderr: %s", archive, got.code, verdict.Failure, got.stderr)
			}
			for _, named := range []string{"wire/ServerEvent", "provides", "go://example.com/server/internal/wire#ServerEvent", "deadset-go"} {
				if !strings.Contains(got.stderr, named) {
					t.Errorf("analyze(%s) stderr = %q, want it to name %s", archive, got.stderr, named)
				}
			}
			if got.stdout != "" {
				t.Errorf("analyze(%s) stdout = %q, want nothing presented as the run's result", archive, got.stdout)
			}
		})
	}
}

// extractArchive writes every file of a txtar archive under root: a marker
// line "-- NAME --" opens each file, whose content runs to the next marker.
func extractArchive(t *testing.T, archive, root string) {
	t.Helper()

	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("Setup: read %s: %v", archive, err)
	}
	files := map[string][]byte{}
	current := ""
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		text := strings.TrimSuffix(string(line), "\n")
		if inner, ok := strings.CutPrefix(text, "-- "); ok && strings.HasSuffix(inner, " --") {
			current = strings.TrimSpace(strings.TrimSuffix(inner, " --"))
			files[current] = []byte{}
			continue
		}
		if current != "" {
			files[current] = append(files[current], line...)
		}
	}
	for name, body := range files {
		if !filepath.IsLocal(filepath.FromSlash(name)) {
			t.Fatalf("Setup: %s names the file %q outside its root", archive, name)
		}
		writeFile(t, filepath.Join(root, filepath.FromSlash(name)), body)
	}
}

// The SARIF log of a run over a Go module carries, for the Go analyzer's
// finding, the fingerprints the Go analyzer computes for the same finding in
// its own SARIF log, and the template renders the finding.
func TestAnalyzeRendersTheGoAnalyzersFindingAsTheGoAnalyzerDoes(t *testing.T) {
	t.Parallel()
	goAnalyzerOnPath(t)

	target, runDir := goModule(t, "package main\n\nfunc main() {}\n\nfunc unused() {}\n")
	template := filepath.Join(t.TempDir(), "report.tmpl")
	writeFile(t, template, []byte("{{range .Findings}}{{.Code}} {{.Analyzer}} {{.Position.Path}}:{{.Position.Line}}\n{{end}}"))
	got := analyze(t, target, runDir, "--formats=sarif,template", "--template="+template)
	if got.code != verdict.Findings {
		t.Fatalf("analyze = %d, want %d\nstdout: %s\nstderr: %s", got.code, verdict.Findings, got.stdout, got.stderr)
	}
	rendered, err := os.ReadFile(filepath.Join(runDir, "report.json.tmpl"))
	if err != nil || string(rendered) != "DS1002 deadset-go main.go:5\n" {
		t.Errorf("the template rendering is %q (%v), want %q", rendered, err, "DS1002 deadset-go main.go:5\n")
	}

	own := filepath.Join(t.TempDir(), "report.json")
	direct := exec.CommandContext(t.Context(), "deadset-go", "analyze", "--target=.", "--report="+own, "--format=sarif")
	direct.Dir = target
	if output, err := direct.CombinedOutput(); direct.ProcessState == nil || direct.ProcessState.ExitCode() != verdict.Findings {
		t.Fatalf("Setup: deadset-go analyze over %s = %v\n%s", target, err, output)
	}
	merged, analyzerOwn := readSARIF(t, filepath.Join(runDir, "report.json.sarif")), readSARIF(t, own+".sarif")
	if len(merged.Runs) != 1 || len(merged.Runs[0].Results) != 1 || len(analyzerOwn.Runs) != 1 || len(analyzerOwn.Runs[0].Results) != 1 {
		t.Fatalf("the merged log holds %+v and the Go analyzer's %+v, want one run with one result each", merged.Runs, analyzerOwn.Runs)
	}
	ours, theirs := merged.Runs[0].Results[0], analyzerOwn.Runs[0].Results[0]
	if ours.RuleID != "DS1002" || !maps.Equal(ours.PartialFingerprints, theirs.PartialFingerprints) {
		t.Errorf("the merged log's result is %s with %v, want DS1002 with the Go analyzer's own %v",
			ours.RuleID, ours.PartialFingerprints, theirs.PartialFingerprints)
	}
}
