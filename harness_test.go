package deadset_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/deadset/internal/config"
	"github.com/cplieger/deadset/internal/merge"
	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/verdict"
)

// harnessDir holds the harness archives and the result recorded over them.
const harnessDir = "testdata/harness"

// resultPath is the recorded result.
const resultPath = harnessDir + "/result.json"

// reportsDir holds, per archive, the report each analyzer wrote over it, in a
// directory named for the archive and a file named for the analyzer.
const reportsDir = harnessDir + "/reports"

// targetDir is the directory, inside a run's directory, an archive is extracted to.
const targetDir = "target"

// The commands the harness's two analyzer halves run.
const (
	goAnalyzer = "deadset-go"
	tsAnalyzer = "deadset-ts"
)

// pendingExit is the Contract's exit code for a report holding a pending finding.
const pendingExit = 4

// harnessRecord is the recorded result, field for field.
type harnessRecord struct {
	Description string           `json:"description"`
	Variants    []harnessVariant `json:"variants"`
}

// harnessVariant is one target tree, what each run over it recorded, and
// whether its merged report meets the criterion.
type harnessVariant struct {
	Archive       string       `json:"archive"`
	Criterion     criterion    `json:"criterion"`
	Go            *analyzerRun `json:"go"`
	TS            *analyzerRun `json:"ts"`
	Merged        *mergedRun   `json:"merged"`
	CriterionHeld bool         `json:"criterion_held"`
	// Violations names every way the merged report fell short of the
	// criterion when the verdict was recorded; it is empty when the criterion
	// held.
	Violations []string `json:"violations,omitempty"`
}

// criterion is what the merged report of one variant says about the edge's
// sides and their members.
type criterion struct {
	Reported        []string `json:"reported"`
	NotReported     []string `json:"not_reported"`
	SharedComponent bool     `json:"shared_component"`
}

// analyzerRun is one analyzer run over one variant.
type analyzerRun struct {
	Analyzer    string               `json:"analyzer"`
	Version     string               `json:"version"`
	Exit        int                  `json:"exit"`
	Pending     int                  `json:"pending"`
	Findings    []recordedFinding    `json:"findings"`
	Evaluations []recordedEvaluation `json:"evaluations"`
}

// mergedRun is the merge of a variant's two recorded reports.
type mergedRun struct {
	Exit        int                  `json:"exit"`
	Pending     int                  `json:"pending"`
	Findings    []recordedFinding    `json:"findings"`
	Evaluations []recordedEvaluation `json:"evaluations"`
}

// recordedFinding is one reported finding, by code, symbol and component.
type recordedFinding struct {
	Code      string `json:"code"`
	Symbol    string `json:"symbol"`
	Component string `json:"component"`
}

// recordedEvaluation is one edge evaluation, with the code of the finding it
// carries when its side is dead.
type recordedEvaluation struct {
	Edge   string `json:"edge"`
	Side   string `json:"side"`
	Symbol string `json:"symbol"`
	State  string `json:"state"`
	Code   string `json:"code,omitempty"`
}

// edgesDocument is the edges document an archive declares.
type edgesDocument struct {
	Description string `json:"description"`
	Edges       []struct {
		ID       string `json:"id"`
		Because  string `json:"because"`
		Provides string `json:"provides"`
		UsedBy   string `json:"used_by"`
	} `json:"edges"`
}

// analyzerHalf is one analyzer's half of the harness: the command, the
// variant's recorded run of it, and whether every variant holds a pending
// finding on that analyzer's side.
type analyzerHalf struct {
	command     string
	testName    string
	run         func(*harnessVariant) **analyzerRun
	pendingEach bool
}

var (
	goHalf = analyzerHalf{
		command: goAnalyzer, testName: "TestHarnessGoHalf", pendingEach: true,
		run: func(v *harnessVariant) **analyzerRun { return &v.Go },
	}
	tsHalf = analyzerHalf{
		command: tsAnalyzer, testName: "TestHarnessTSHalf",
		run: func(v *harnessVariant) **analyzerRun { return &v.TS },
	}
)

// TestHarnessGoHalf runs the Go analyzer alone over every harness variant, the
// way a single-analyzer run sees a mixed target: the Go side is pending, so the
// run exits with the pending code, names the pending count, and reports nothing
// an evaluation holds; and the run matches the one result.json and the
// recorded report hold.
//
// UPDATE_GOLDEN=1 records the runs into result.json and reports/ instead of
// comparing them.
func TestHarnessGoHalf(t *testing.T) {
	testAnalyzerHalf(t, &goHalf)
}

// TestHarnessTSHalf runs the TypeScript analyzer alone over every harness
// variant: a pending finding on the TypeScript side makes the run exit with
// the pending code and name the pending count, and the run matches the one
// result.json and the recorded report hold.
//
// UPDATE_GOLDEN=1 records the runs into result.json and reports/ instead of
// comparing them.
func TestHarnessTSHalf(t *testing.T) {
	testAnalyzerHalf(t, &tsHalf)
}

func testAnalyzerHalf(t *testing.T, half *analyzerHalf) {
	t.Helper()

	analyzer, err := exec.LookPath(half.command)
	if err != nil {
		t.Skipf("this half of the harness needs %s on PATH: %v", half.command, err)
	}
	record := readRecord(t)
	update := os.Getenv("UPDATE_GOLDEN") == "1"

	for i := range record.Variants {
		variant := &record.Variants[i]
		t.Run(variantName(variant), func(t *testing.T) {
			base := t.TempDir()
			prepareRun(t, variant.Archive, base)
			got, body, stderr := runAnalyzer(t, analyzer, base)
			checkSingleRun(t, half, variant.Archive, &got, stderr)

			recorded := half.run(variant)
			if update {
				*recorded = &got
				writeReport(t, variant, half.command, body)
				return
			}
			if *recorded == nil {
				t.Fatalf("%s records no %s run for %s (run UPDATE_GOLDEN=1 go test -run %s . to record one)",
					resultPath, half.command, variant.Archive, half.testName)
			}
			compareRun(t, half, variant, &got, body)
		})
	}

	if update && !t.Failed() {
		writeRecord(t, record)
	}
}

// checkSingleRun asserts what a single-analyzer run reports about the edge:
// one pending finding per dead evaluation, each held only inside its
// evaluation, and with a pending finding the pending exit code and the count
// named on stderr.
func checkSingleRun(t *testing.T, half *analyzerHalf, archive string, got *analyzerRun, stderr string) {
	t.Helper()

	dead := 0
	for _, e := range got.Evaluations {
		if e.State == string(report.StateDead) {
			dead++
		}
		for _, f := range got.Findings {
			if f.Symbol == e.Symbol {
				t.Errorf("%s analyze over %s reports %s %s, want it only inside the evaluation of edge %s",
					half.command, archive, f.Code, f.Symbol, e.Edge)
			}
		}
	}
	if got.Pending != dead {
		t.Errorf("%s analyze over %s holds %d pending findings and %d dead evaluations, want one pending finding per dead evaluation",
			half.command, archive, got.Pending, dead)
	}
	if half.pendingEach && dead == 0 {
		t.Errorf("%s analyze over %s holds no dead evaluation, want its side of the edge pending", half.command, archive)
	}
	if got.Pending == 0 {
		return
	}
	if got.Exit != pendingExit {
		t.Errorf("%s analyze over %s exited %d, want %d\nstderr: %s", half.command, archive, got.Exit, pendingExit, stderr)
	}
	if named := fmt.Sprintf("%d pending finding", got.Pending); !strings.Contains(stderr, named) {
		t.Errorf("%s analyze over %s wrote %q to stderr, want it to name %q", half.command, archive, stderr, named)
	}
}

// compareRun compares one run with the run and the report the harness
// records. The version names the build that produced the record, so a run of
// another build is compared on its outcome alone, and its report bytes only
// when the builds agree.
func compareRun(t *testing.T, half *analyzerHalf, variant *harnessVariant, got *analyzerRun, body []byte) {
	t.Helper()

	want := **half.run(variant)
	recorded := want.Version
	want.Version = got.Version
	if got.Version != recorded {
		t.Logf("%s %s ran over %s, and %s records a run of %s", half.command, got.Version, variant.Archive, resultPath, recorded)
	}
	if gotJSON, wantJSON := compact(t, got), compact(t, &want); gotJSON != wantJSON {
		t.Errorf("%s %s analyze over %s = %s\nwant %s, as %s records the run of %s (run UPDATE_GOLDEN=1 go test -run %s . to record the new run)",
			half.command, got.Version, variant.Archive, gotJSON, wantJSON, resultPath, recorded, half.testName)
	}
	if got.Version != recorded {
		return
	}
	path := reportPath(variant, half.command)
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (run UPDATE_GOLDEN=1 go test -run %s . to record it): %v", path, half.testName, err)
	}
	if !bytes.Equal(body, committed) {
		t.Errorf("%s %s analyze over %s wrote a report that differs from %s (run UPDATE_GOLDEN=1 go test -run %s . to record the new report)\ngot:\n%s",
			half.command, got.Version, variant.Archive, path, half.testName, body)
	}
}

// TestHarnessMerge merges every variant's two recorded reports once, the way
// the orchestrator merges them, holds the merged report to the variant's
// criterion (see checkCriterion), and matches the merged run and the verdict
// to the ones result.json records.
//
// UPDATE_GOLDEN=1 records the merged runs and the verdicts into result.json
// instead of comparing them.
func TestHarnessMerge(t *testing.T) {
	record := readRecord(t)
	update := os.Getenv("UPDATE_GOLDEN") == "1"
	conformance, err := merge.Conformance()
	if err != nil {
		t.Fatalf("Setup: merge.Conformance() = %v", err)
	}
	caller := &merge.Caller{
		SchemaVersion:   report.SchemaVersion,
		ContractVersion: report.ContractVersion,
		Name:            "deadset",
		Version:         "0.0.0",
		Conformance:     conformance,
	}

	for i := range record.Variants {
		variant := &record.Variants[i]
		t.Run(variantName(variant), func(t *testing.T) {
			inputs := []merge.Input{
				{Report: readReport(t, variant, goAnalyzer), Digest: recordedDigest},
				{Report: readReport(t, variant, tsAnalyzer), Digest: recordedDigest},
			}
			merged, err := merge.Merge(inputs, report.SchemaVersions, caller)
			if err != nil {
				t.Fatalf("merge.Merge(%s's reports) = %v, want a merged report", variant.Archive, err)
			}
			run := project(merged)
			got := mergedRun{
				Findings:    run.Findings,
				Evaluations: run.Evaluations,
				Exit:        verdict.Code(merged, config.Deny, verdict.On),
				Pending:     run.Pending,
			}
			violations := checkCriterion(variant, &got)
			held := len(violations) == 0

			if update {
				// A no-go is recorded, not refused: the record exists for it.
				for _, v := range violations {
					t.Logf("the merge of %s's reports %s", variant.Archive, v)
				}
				variant.Merged = &got
				variant.CriterionHeld = held
				variant.Violations = violations
				return
			}
			for _, v := range violations {
				t.Errorf("the merge of %s's reports %s", variant.Archive, v)
			}
			if variant.Merged == nil {
				t.Fatalf("%s records no merged run for %s (run UPDATE_GOLDEN=1 go test -run TestHarnessMerge . to record one)",
					resultPath, variant.Archive)
			}
			if gotJSON, wantJSON := compact(t, &got), compact(t, variant.Merged); gotJSON != wantJSON {
				t.Errorf("the merge of %s's reports = %s\nwant %s, as %s records it (run UPDATE_GOLDEN=1 go test -run TestHarnessMerge . to record the new merge)",
					variant.Archive, gotJSON, wantJSON, resultPath)
			}
			if !slices.Equal(violations, variant.Violations) {
				t.Errorf("the merge of %s's reports falls short in %q, and %s records %q (run UPDATE_GOLDEN=1 go test -run TestHarnessMerge . to record the verdict)",
					variant.Archive, violations, resultPath, variant.Violations)
			}
			if held != variant.CriterionHeld {
				t.Errorf("the criterion over %s held = %t, and %s records %t (run UPDATE_GOLDEN=1 go test -run TestHarnessMerge . to record the verdict)",
					variant.Archive, held, resultPath, variant.CriterionHeld)
			}
		})
	}

	if update && !t.Failed() {
		writeRecord(t, record)
	}
}

// checkCriterion is every way the merge falls short of the variant's
// criterion, each as a clause naming what was found: a reported symbol with no
// finding, a not-reported symbol with one, reported symbols split across
// components where the criterion shares one, a pending finding left in the
// merged report, or a recorded Go run that did not exit with the pending code.
func checkCriterion(variant *harnessVariant, got *mergedRun) []string {
	var violations []string
	components := map[string][]string{}
	for _, f := range got.Findings {
		components[f.Symbol] = append(components[f.Symbol], f.Component)
	}
	for _, symbol := range variant.Criterion.NotReported {
		if found, ok := components[symbol]; ok {
			violations = append(violations, fmt.Sprintf("reports %s in %q, want no finding for it", symbol, found))
		}
	}
	shared := map[string]bool{}
	for _, symbol := range variant.Criterion.Reported {
		found, ok := components[symbol]
		if !ok {
			violations = append(violations, fmt.Sprintf("reports no finding for %s, want one", symbol))
		}
		for _, c := range found {
			shared[c] = true
		}
	}
	if variant.Criterion.SharedComponent && len(shared) != 1 {
		violations = append(violations, fmt.Sprintf("reports %q in %d components, want one component", variant.Criterion.Reported, len(shared)))
	}
	if got.Pending != 0 {
		violations = append(violations, fmt.Sprintf("holds %d pending findings, want none", got.Pending))
	}
	if variant.Go == nil || variant.Go.Exit != pendingExit || variant.Go.Pending == 0 {
		violations = append(violations, fmt.Sprintf("follows a %s run that did not exit %d with a pending finding, as %s records it", goAnalyzer, pendingExit, resultPath))
	}
	return violations
}

// TestHarnessArchives pins the archives and the recorded reports to the
// record without running an analyzer: result.json names every archive in the
// harness directory once, every archive declares one edge, every symbol the
// record names is one of that edge's two sides or a member of one, and every
// recorded run is the run its recorded report reads as.
func TestHarnessArchives(t *testing.T) {
	record := readRecord(t)

	present, err := filepath.Glob(filepath.Join(harnessDir, "*.txtar"))
	if err != nil {
		t.Fatalf("Setup: list the archives in %s: %v", harnessDir, err)
	}
	for i, p := range present {
		present[i] = filepath.Base(p)
	}
	slices.Sort(present)
	named := make([]string, 0, len(record.Variants))
	for i := range record.Variants {
		named = append(named, record.Variants[i].Archive)
	}
	slices.Sort(named)
	if !slices.Equal(present, named) {
		t.Errorf("%s holds the archives %q, and %s names %q, want the same set", harnessDir, present, resultPath, named)
	}

	for i := range record.Variants {
		variant := &record.Variants[i]
		t.Run(variantName(variant), func(t *testing.T) {
			edge := readEdge(t, variant.Archive)
			sides := []string{edge.provides, edge.usedBy}
			for _, symbol := range slices.Concat(variant.Criterion.Reported, variant.Criterion.NotReported) {
				if !slices.ContainsFunc(sides, func(side string) bool { return symbol == side || strings.HasPrefix(symbol, side+".") }) {
					t.Errorf("%s's criterion names %s, which is neither side of edge %s %q nor a member of one", variant.Archive, symbol, edge.id, sides)
				}
			}
			for _, half := range []*analyzerHalf{&goHalf, &tsHalf} {
				run := *half.run(variant)
				if run == nil {
					continue
				}
				for _, e := range run.Evaluations {
					if e.Edge != edge.id || !slices.Contains(sides, e.Symbol) {
						t.Errorf("%s records the %s evaluation of %s %s, want edge %s and one of %q",
							variant.Archive, run.Analyzer, e.Edge, e.Symbol, edge.id, sides)
					}
				}
				read := project(readReport(t, variant, half.command))
				read.Exit = run.Exit
				if gotJSON, wantJSON := compact(t, &read), compact(t, run); gotJSON != wantJSON {
					t.Errorf("%s reads as %s\nwant %s, the %s run %s records over %s",
						reportPath(variant, half.command), gotJSON, wantJSON, half.command, resultPath, variant.Archive)
				}
			}
		})
	}
}

// declaredEdge is the one edge an archive declares.
type declaredEdge struct {
	id, provides, usedBy string
}

// readEdge decodes an archive's edges document, which declares one edge.
func readEdge(t *testing.T, archive string) declaredEdge {
	t.Helper()

	files := readArchive(t, archive)
	var edges edgesDocument
	dec := json.NewDecoder(bytes.NewReader(files["deadset-edges.json"]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&edges); err != nil {
		t.Fatalf("decode deadset-edges.json in %s: %v", archive, err)
	}
	if len(edges.Edges) != 1 {
		t.Fatalf("%s declares %d edges, want 1", archive, len(edges.Edges))
	}
	edge := edges.Edges[0]
	return declaredEdge{id: edge.ID, provides: edge.Provides, usedBy: edge.UsedBy}
}

// scopeName is the scope document, inside a run's directory, every analyzer
// run reads.
const scopeName = "scope.json"

// prepareRun extracts an archive into base's target directory and writes the
// scope document beside it. The scope names the target by its Go module path,
// the one identity both analyzers' reports of a mixed target carry, so the two
// reports name one target and merge.
func prepareRun(t *testing.T, archive, base string) {
	t.Helper()

	files := readArchive(t, archive)
	extract(t, archive, files, filepath.Join(base, targetDir))
	module := ""
	for line := range strings.Lines(string(files["go.mod"])) {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			module = strings.TrimSpace(name)
		}
	}
	if module == "" {
		t.Fatalf("Setup: %s's go.mod names no module", archive)
	}
	scope, err := json.Marshal(map[string]any{"target": map[string]string{"id": module, "path": targetDir}})
	if err != nil {
		t.Fatalf("Setup: encode the scope document: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, scopeName), scope, 0o600); err != nil {
		t.Fatalf("Setup: write the scope document: %v", err)
	}
}

// runAnalyzer runs one analyzer's analyze verb as a separate process from base,
// over the target tree and the scope document under it, and returns what the
// harness records of the run, the report it wrote and the run's stderr. The
// run directory holds the target, the scope and the report, because a report
// names its paths relative to the directory the run starts in.
func runAnalyzer(t *testing.T, analyzer, base string) (analyzerRun, []byte, string) {
	t.Helper()

	const reportName = "report.json"
	cmd := exec.CommandContext(t.Context(), analyzer, "analyze",
		"--target="+targetDir, "--scope="+scopeName, "--report="+reportName)
	cmd.Dir = base
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	exit := 0
	if err := cmd.Run(); err != nil {
		var exited *exec.ExitError
		if !errors.As(err, &exited) {
			t.Fatalf("Setup: run %s analyze in %s: %v", analyzer, base, err)
		}
		exit = exited.ExitCode()
	}

	body, err := os.ReadFile(filepath.Join(base, reportName))
	if err != nil {
		t.Fatalf("read the report %s analyze wrote (exit %d): %v\nstderr: %s", analyzer, exit, err, stderr.String())
	}
	decoded, err := report.Decode(body)
	if err != nil {
		t.Fatalf("decode the report %s analyze wrote: %v", analyzer, err)
	}
	run := project(decoded)
	run.Exit = exit
	return run, body, stderr.String()
}

// project is what the harness records of a report: the analyzer, the pending
// count, and the findings and edge evaluations by code and symbol. The exit
// code is the run's and is not in the report.
func project(r *report.Report) analyzerRun {
	run := analyzerRun{
		Analyzer:    r.Analyzer.Name,
		Version:     r.Analyzer.Version,
		Pending:     r.Totals.Pending,
		Findings:    make([]recordedFinding, 0, len(r.Findings)),
		Evaluations: make([]recordedEvaluation, 0, len(r.EdgeEvaluations)),
	}
	for i := range r.Findings {
		f := &r.Findings[i]
		run.Findings = append(run.Findings, recordedFinding{Code: f.Code, Symbol: f.Symbol.Ref, Component: f.Component.ID})
	}
	for i := range r.EdgeEvaluations {
		e := &r.EdgeEvaluations[i]
		recorded := recordedEvaluation{Edge: e.Edge, Side: string(e.Side), Symbol: e.Symbol, State: string(e.State)}
		if e.Finding != nil {
			recorded.Code = e.Finding.Code
		}
		run.Evaluations = append(run.Evaluations, recorded)
	}
	return run
}

// recordedDigest stands for the digest of an analyzer artifact in a merge of
// recorded reports, where no artifact is at hand: the merged report names it
// in merged_from, and nothing the harness records reads it.
var recordedDigest = "sha256:" + strings.Repeat("0", 64)

// variantName is the subtest name of a variant, its archive without the
// extension.
func variantName(variant *harnessVariant) string {
	return strings.TrimSuffix(variant.Archive, ".txtar")
}

// reportPath is the recorded report of one analyzer over one variant.
func reportPath(variant *harnessVariant, analyzer string) string {
	return filepath.Join(reportsDir, variantName(variant), analyzer+".json")
}

// readReport decodes the recorded report of one analyzer over one variant
// through the closed-key decode the orchestrator reads every report with.
func readReport(t *testing.T, variant *harnessVariant, analyzer string) *report.Report {
	t.Helper()

	path := reportPath(variant, analyzer)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Setup: read %s (run UPDATE_GOLDEN=1 go test . with %s on PATH to record it): %v", path, analyzer, err)
	}
	r, err := report.Decode(body)
	if err != nil {
		t.Fatalf("Setup: decode %s: %v", path, err)
	}
	return r
}

// writeReport records the report one analyzer wrote over one variant.
func writeReport(t *testing.T, variant *harnessVariant, analyzer string, body []byte) {
	t.Helper()

	path := reportPath(variant, analyzer)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create the directory of %s: %v", path, err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// readRecord decodes result.json, refusing a member the record does not declare.
func readRecord(t *testing.T) harnessRecord {
	t.Helper()

	body, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("Setup: read %s: %v", resultPath, err)
	}
	var record harnessRecord
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&record); err != nil {
		t.Fatalf("Setup: decode %s: %v", resultPath, err)
	}
	return record
}

// writeRecord writes result.json in the field order of the record's types.
func writeRecord(t *testing.T, record harnessRecord) {
	t.Helper()

	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(record); err != nil {
		t.Fatalf("encode %s: %v", resultPath, err)
	}
	if err := os.WriteFile(resultPath, body.Bytes(), 0o644); err != nil {
		t.Fatalf("write %s: %v", resultPath, err)
	}
}

// compact is a value's JSON encoding, which is how two runs are compared and
// printed.
func compact(t *testing.T, v any) string {
	t.Helper()

	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode %+v: %v", v, err)
	}
	return string(body)
}

// extract writes the files of one archive under root.
func extract(t *testing.T, archive string, files map[string][]byte, root string) {
	t.Helper()

	for name, body := range files {
		local := filepath.FromSlash(name)
		if !filepath.IsLocal(local) {
			t.Fatalf("Setup: %s names the file %q outside the directory it is extracted to", archive, name)
		}
		p := filepath.Join(root, local)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatalf("Setup: create the directory of %s: %v", p, err)
		}
		if err := os.WriteFile(p, body, 0o600); err != nil {
			t.Fatalf("Setup: write %s: %v", p, err)
		}
	}
}

// readArchive reads one harness archive into its files, keyed by name.
func readArchive(t *testing.T, archive string) map[string][]byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(harnessDir, archive))
	if err != nil {
		t.Fatalf("Setup: read %s: %v", archive, err)
	}
	files, err := parseTxtar(data)
	if err != nil {
		t.Fatalf("Setup: parse %s: %v", archive, err)
	}
	return files
}

// parseTxtar reads a txtar archive into its files, keyed by name. A marker line,
// "-- NAME --" with the space around NAME trimmed, opens each file, whose
// content runs to the next marker; the text before the first marker is the
// archive's comment.
func parseTxtar(data []byte) (map[string][]byte, error) {
	files := map[string][]byte{}
	current := ""
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		if name, ok := txtarMarker(line); ok {
			if _, written := files[name]; written {
				return nil, fmt.Errorf("the file %q is written twice", name)
			}
			files[name] = []byte{}
			current = name
			continue
		}
		if current != "" {
			files[current] = append(files[current], line...)
		}
	}
	return files, nil
}

// txtarMarker returns the file name a marker line opens, and whether the line is
// one.
func txtarMarker(line []byte) (string, bool) {
	text := strings.TrimSuffix(string(line), "\n")
	inner, opened := strings.CutPrefix(text, "-- ")
	inner, closed := strings.CutSuffix(inner, " --")
	name := strings.TrimSpace(inner)
	return name, opened && closed && name != ""
}
