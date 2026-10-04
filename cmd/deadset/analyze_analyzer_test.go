package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/verdict"
)

// goAnalyzerOnPath skips a test of a real run when PATH holds no Go analyzer.
func goAnalyzerOnPath(t *testing.T) {
	t.Helper()

	analyzerOnPath(t, "deadset-go")
}

// analyzerOnPath skips a test of a real run when PATH holds no analyzer named
// command.
func analyzerOnPath(t *testing.T, command string) {
	t.Helper()

	if _, err := exec.LookPath(command); err != nil {
		t.Skipf("a run of the real analyzer needs %s on PATH: %v", command, err)
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

// harnessResult is the part of the harness's recorded result this file reads:
// per archive, the merge of the report each analyzer wrote over it.
type harnessResult struct {
	Variants []struct {
		Merged  *mergedRun `json:"merged"`
		Archive string     `json:"archive"`
	} `json:"variants"`
}

// mergedRun is what the harness records of a merged report: the exit code of
// its verdict, its pending count, and its findings and edge evaluations by
// code and symbol.
type mergedRun struct {
	Findings    []mergedFinding    `json:"findings"`
	Evaluations []mergedEvaluation `json:"evaluations"`
	Exit        int                `json:"exit"`
	Pending     int                `json:"pending"`
}

// mergedFinding is one finding of a merged report, by code, symbol and
// component.
type mergedFinding struct {
	Code      string `json:"code"`
	Symbol    string `json:"symbol"`
	Component string `json:"component"`
}

// mergedEvaluation is one edge evaluation of a merged report, with the code
// of the finding it carries when its side is dead.
type mergedEvaluation struct {
	Edge   string `json:"edge"`
	Side   string `json:"side"`
	Symbol string `json:"symbol"`
	State  string `json:"state"`
	Code   string `json:"code,omitempty"`
}

// recordedMerges is every harness archive mapped to the merge the harness
// records over it.
func recordedMerges(t *testing.T) map[string]*mergedRun {
	t.Helper()

	path := filepath.Join("..", "..", "testdata", "harness", "result.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Setup: read %s: %v", path, err)
	}
	var result harnessResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Setup: decode %s: %v", path, err)
	}
	merges := make(map[string]*mergedRun, len(result.Variants))
	for _, variant := range result.Variants {
		merges[variant.Archive] = variant.Merged
	}
	return merges
}

// harnessView is what the harness records of the merged report r of a run
// that exited with code.
func harnessView(r *report.Report, code int) *mergedRun {
	run := &mergedRun{
		Findings:    make([]mergedFinding, 0, len(r.Findings)),
		Evaluations: make([]mergedEvaluation, 0, len(r.EdgeEvaluations)),
		Exit:        code,
		Pending:     r.Totals.Pending,
	}
	for i := range r.Findings {
		f := &r.Findings[i]
		run.Findings = append(run.Findings, mergedFinding{Code: f.Code, Symbol: f.Symbol.Ref, Component: f.Component.ID})
	}
	for i := range r.EdgeEvaluations {
		e := &r.EdgeEvaluations[i]
		code := ""
		if e.Finding != nil {
			code = e.Finding.Code
		}
		run.Evaluations = append(run.Evaluations, mergedEvaluation{Edge: e.Edge, Side: string(e.Side), Symbol: e.Symbol, State: string(e.State), Code: code})
	}
	return run
}

// A run of both analyzers over each harness variant, a target holding Go and
// TypeScript, merges their two reports into one target and exits with the
// merged report's verdict, and the merged report is the merge the harness
// records of the two analyzers' own reports over that variant.
func TestAnalyzeOverTheHarnessMergesBothAnalyzersReports(t *testing.T) {
	t.Parallel()
	goAnalyzerOnPath(t)
	analyzerOnPath(t, "deadset-ts")

	merges := recordedMerges(t)
	archives, err := filepath.Glob(filepath.Join("..", "..", "testdata", "harness", "*.txtar"))
	if err != nil || len(archives) == 0 {
		t.Fatalf("Setup: glob the harness archives = %v, %v", archives, err)
	}
	for _, archive := range archives {
		t.Run(strings.TrimSuffix(filepath.Base(archive), ".txtar"), func(t *testing.T) {
			t.Parallel()

			want := merges[filepath.Base(archive)]
			if want == nil {
				t.Fatalf("Setup: the harness records no merge over %s", archive)
			}
			base := t.TempDir()
			target := filepath.Join(base, "target")
			extractArchive(t, archive, target)
			got := analyze(t, target, filepath.Join(base, "run"))
			if got.code != want.Exit {
				t.Fatalf("analyze(%s) = %d, want %d, the verdict of the recorded merge\nstderr: %s", archive, got.code, want.Exit, got.stderr)
			}
			r := mergedReport(t, filepath.Join(base, "run"))
			gotJSON, err := json.Marshal(harnessView(r, got.code))
			if err != nil {
				t.Fatalf("encode the merged run: %v", err)
			}
			wantJSON, err := json.Marshal(want)
			if err != nil {
				t.Fatalf("Setup: encode the recorded merge: %v", err)
			}
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("analyze(%s) merged %s\nwant %s, the merge the harness records", archive, gotJSON, wantJSON)
			}
			var names []string
			for _, from := range r.MergedFrom {
				names = append(names, from.Name)
			}
			if !slices.Equal(names, []string{"deadset-go", "deadset-ts"}) || r.Target.Identity != "example.com/server" {
				t.Errorf("analyze(%s) merged the reports of %q over the target %q, want deadset-go and deadset-ts over example.com/server",
					archive, names, r.Target.Identity)
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
	writeFile(t, template, []byte("{{range .findings}}{{.code}} {{.analyzer}} {{.position.path}}:{{.position.line}}\n{{end}}"))
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

// claims is each finding of a merged report as its code, its subject's name,
// its reachability class and the consumers it names.
func claims(r *report.Report) []string {
	var found []string
	for i := range r.Findings {
		f := &r.Findings[i]
		found = append(found, fmt.Sprintf("%s %s %s %v", f.Code, f.Symbol.Name, f.ReachabilityClass, f.ConsumersLoaded))
	}
	return found
}

// A library and one consumer referencing part of its published API, run
// through the real analyzers with the archive's scope document and without it.
// With it, the merged report names the consumer once under consumers.loaded,
// every finding is certain and names it, and the unreferenced exported
// function is reported. Without it, nothing is loaded, the default minimum
// confidence withholds the published API's findings, and the minimum possible
// reports each as possible, the function only the consumer calls among them.
func TestAnalyzeClassesALibrarysFindingsByTheConsumersItLoads(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		archive, consumer string
		analyzers         []string
		with, possible    []string
	}{
		{
			archive: "go.txtar", analyzers: []string{"deadset-go"}, consumer: "example.com/consumer",
			with:     []string{"DS1003 Options.Spare certain [example.com/consumer]", "DS1001 Farewell certain [example.com/consumer]"},
			possible: []string{"DS1003 Options.Spare possible []", "DS1001 Greet possible []", "DS1001 Farewell possible []"},
		},
		{
			archive: "ts.txtar", analyzers: []string{"deadset-ts"}, consumer: "@example/consumer",
			with:     []string{"DS1003 Options.spare certain [@example/consumer]", "DS1001 farewell certain [@example/consumer]"},
			possible: []string{"DS1003 Options.spare possible []", "DS1001 greet possible []", "DS1001 farewell possible []"},
		},
		{
			archive: "mixed.txtar", analyzers: []string{"deadset-go", "deadset-ts"}, consumer: "example.com/consumer",
			with: []string{
				"DS1003 Options.Spare certain [example.com/consumer]", "DS1001 Farewell certain [example.com/consumer]",
				"DS1003 Options.spare certain [example.com/consumer]", "DS1001 farewell certain [example.com/consumer]",
			},
			possible: []string{
				"DS1003 Options.Spare possible []", "DS1001 Greet possible []", "DS1001 Farewell possible []",
				"DS1003 Options.spare possible []", "DS1001 greet possible []", "DS1001 farewell possible []",
			},
		},
	} {
		t.Run(strings.TrimSuffix(c.archive, ".txtar"), func(t *testing.T) {
			t.Parallel()
			for _, analyzer := range c.analyzers {
				analyzerOnPath(t, analyzer)
			}

			for _, run := range []struct {
				name     string
				loaded   []report.LoadedConsumer
				root     string
				flags    []string
				findings []string
				code     int
				scoped   bool
			}{
				{
					name: "with-the-consumer", scoped: true, root: "lib", findings: c.with, code: verdict.Findings,
					loaded: []report.LoadedConsumer{{ID: c.consumer, Role: "consumer", Path: "consumer"}},
				},
				{name: "without-it", root: ".", code: verdict.Clean},
				{name: "without-it-at-possible", root: ".", flags: []string{"--min-confidence=possible"}, findings: c.possible, code: verdict.Findings},
			} {
				t.Run(run.name, func(t *testing.T) {
					t.Parallel()

					base := t.TempDir()
					extractArchive(t, filepath.Join("testdata", "consumers", c.archive), base)
					extra := slices.Clone(run.flags)
					if run.scoped {
						extra = append(extra, "--scope="+filepath.Join(base, "scope.json"))
					}
					got := analyze(t, filepath.Join(base, "lib"), filepath.Join(base, "run"), extra...)
					if got.code != run.code {
						t.Fatalf("analyze(%s, %s) = %d, want %d\nstderr: %s", c.archive, run.name, got.code, run.code, got.stderr)
					}
					merged := mergedReport(t, filepath.Join(base, "run"))
					if !slices.Equal(merged.Consumers.Loaded, run.loaded) || merged.Consumers.Declared != len(run.loaded) {
						t.Errorf("analyze(%s, %s) consumers = %+v, want %+v loaded", c.archive, run.name, merged.Consumers, run.loaded)
					}
					if found := claims(merged); !slices.Equal(found, run.findings) {
						t.Errorf("analyze(%s, %s) findings = %q, want %q", c.archive, run.name, found, run.findings)
					}
					var names []string
					for _, from := range merged.MergedFrom {
						names = append(names, from.Name)
					}
					if merged.Target.Root != run.root || !slices.Equal(names, c.analyzers) {
						t.Errorf("analyze(%s, %s) merged the reports of %q over the root %q, want %q over %q",
							c.archive, run.name, names, merged.Target.Root, c.analyzers, run.root)
					}
				})
			}
		})
	}
}
