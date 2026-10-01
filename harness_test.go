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
)

// harnessDir holds the harness archives and the result recorded over them.
const harnessDir = "testdata/harness"

// resultPath is the recorded result.
const resultPath = harnessDir + "/result.json"

// targetDir is the directory, inside a run's directory, an archive is extracted to.
const targetDir = "target"

// goAnalyzer is the command the harness's Go half runs.
const goAnalyzer = "deadset-go"

// pendingExit is the Contract's exit code for a report holding a pending finding.
const pendingExit = 4

// harnessRecord is the recorded result, field for field.
type harnessRecord struct {
	Description string           `json:"description"`
	Variants    []harnessVariant `json:"variants"`
}

// harnessVariant is one target tree and what each run over it recorded.
type harnessVariant struct {
	Archive   string          `json:"archive"`
	Criterion criterion       `json:"criterion"`
	Go        *analyzerRun    `json:"go"`
	TS        *analyzerRun    `json:"ts"`
	Merged    json.RawMessage `json:"merged"`
}

// criterion is what the merged report of one variant says about the edge's two
// symbols.
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

// recordedFinding is one reported finding, by code and symbol.
type recordedFinding struct {
	Code   string `json:"code"`
	Symbol string `json:"symbol"`
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

// analyzerReport is the part of an analyzer's report the harness records.
type analyzerReport struct {
	Analyzer struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"analyzer"`
	Findings []struct {
		Code   string `json:"code"`
		Symbol struct {
			Ref string `json:"ref"`
		} `json:"symbol"`
	} `json:"findings"`
	EdgeEvaluations []struct {
		Finding *struct {
			Code string `json:"code"`
		} `json:"finding"`
		Edge   string `json:"edge"`
		Side   string `json:"side"`
		Symbol string `json:"symbol"`
		State  string `json:"state"`
	} `json:"edge_evaluations"`
	Totals struct {
		Pending int `json:"pending"`
	} `json:"totals"`
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

// TestHarnessGoHalf runs the Go analyzer alone over every harness variant, the
// way a single-analyzer run sees a mixed target: the Go type is pending, so the
// run exits with the pending code, names the pending count, and reports nothing
// an evaluation holds; and the run matches the one result.json records.
//
// UPDATE_GOLDEN=1 records the runs into result.json instead of comparing them.
func TestHarnessGoHalf(t *testing.T) {
	analyzer, err := exec.LookPath(goAnalyzer)
	if err != nil {
		t.Skipf("the harness's Go half needs %s on PATH: %v", goAnalyzer, err)
	}
	record := readRecord(t)
	update := os.Getenv("UPDATE_GOLDEN") == "1"

	for i := range record.Variants {
		variant := &record.Variants[i]
		t.Run(strings.TrimSuffix(variant.Archive, ".txtar"), func(t *testing.T) {
			base := t.TempDir()
			extract(t, variant.Archive, filepath.Join(base, targetDir))
			got, stderr := runAnalyzer(t, analyzer, base)

			dead := 0
			for _, e := range got.Evaluations {
				if e.State == "dead" {
					dead++
				}
				for _, f := range got.Findings {
					if f.Symbol == e.Symbol {
						t.Errorf("%s analyze over %s reports %s %s, want it only inside the evaluation of edge %s",
							goAnalyzer, variant.Archive, f.Code, f.Symbol, e.Edge)
					}
				}
			}
			if dead == 0 || got.Pending != dead {
				t.Errorf("%s analyze over %s holds %d pending findings and %d dead evaluations, want one pending finding per dead evaluation and at least one",
					goAnalyzer, variant.Archive, got.Pending, dead)
			}
			if got.Exit != pendingExit {
				t.Errorf("%s analyze over %s exited %d, want %d\nstderr: %s", goAnalyzer, variant.Archive, got.Exit, pendingExit, stderr)
			}
			if named := fmt.Sprintf("%d pending finding", got.Pending); !strings.Contains(stderr, named) {
				t.Errorf("%s analyze over %s wrote %q to stderr, want it to name %q", goAnalyzer, variant.Archive, stderr, named)
			}

			if update {
				variant.Go = &got
				return
			}
			if variant.Go == nil {
				t.Fatalf("%s records no Go run for %s (run UPDATE_GOLDEN=1 go test -run TestHarnessGoHalf . to record one)",
					resultPath, variant.Archive)
			}
			// The version names the build that produced the record, so a run of
			// another build is compared on its outcome alone.
			want := *variant.Go
			recorded := want.Version
			want.Version = got.Version
			if got.Version != recorded {
				t.Logf("%s %s ran over %s, and %s records a run of %s", goAnalyzer, got.Version, variant.Archive, resultPath, recorded)
			}
			if gotJSON, wantJSON := compact(t, got), compact(t, want); gotJSON != wantJSON {
				t.Errorf("%s %s analyze over %s = %s\nwant %s, as %s records the run of %s (run UPDATE_GOLDEN=1 go test -run TestHarnessGoHalf . to record the new run)",
					goAnalyzer, got.Version, variant.Archive, gotJSON, wantJSON, resultPath, recorded)
			}
		})
	}

	if update && !t.Failed() {
		writeRecord(t, record)
	}
}

// TestHarnessArchives pins the archives to the record without running an
// analyzer: result.json names every archive in the harness directory once, every
// archive declares one edge, and every symbol the record names is one of that
// edge's two sides.
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
		t.Run(strings.TrimSuffix(variant.Archive, ".txtar"), func(t *testing.T) {
			files := readArchive(t, variant.Archive)
			var edges edgesDocument
			dec := json.NewDecoder(bytes.NewReader(files["deadset-edges.json"]))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&edges); err != nil {
				t.Fatalf("decode deadset-edges.json in %s: %v", variant.Archive, err)
			}
			if len(edges.Edges) != 1 {
				t.Fatalf("%s declares %d edges, want 1", variant.Archive, len(edges.Edges))
			}
			edge := edges.Edges[0]
			sides := []string{edge.Provides, edge.UsedBy}

			for _, symbol := range slices.Concat(variant.Criterion.Reported, variant.Criterion.NotReported) {
				if !slices.Contains(sides, symbol) {
					t.Errorf("%s's criterion names %s, which is neither side of edge %s %q", variant.Archive, symbol, edge.ID, sides)
				}
			}
			for _, run := range []*analyzerRun{variant.Go, variant.TS} {
				if run == nil {
					continue
				}
				for _, e := range run.Evaluations {
					if e.Edge != edge.ID || !slices.Contains(sides, e.Symbol) {
						t.Errorf("%s records the %s evaluation of %s %s, want edge %s and one of %q",
							variant.Archive, run.Analyzer, e.Edge, e.Symbol, edge.ID, sides)
					}
				}
			}
		})
	}
}

// runAnalyzer runs one analyzer's analyze verb as a separate process from base,
// over the target tree under it, and returns what the harness records of the run
// and the run's stderr. The run directory holds both the target and the report,
// because a report names its paths relative to the directory the run starts in.
func runAnalyzer(t *testing.T, analyzer, base string) (analyzerRun, string) {
	t.Helper()

	const reportName = "report.json"
	cmd := exec.CommandContext(t.Context(), analyzer, "analyze", "--target="+targetDir, "--report="+reportName)
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
	var report analyzerReport
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("decode the report %s analyze wrote: %v", analyzer, err)
	}

	run := analyzerRun{
		Analyzer:    report.Analyzer.Name,
		Version:     report.Analyzer.Version,
		Exit:        exit,
		Pending:     report.Totals.Pending,
		Findings:    make([]recordedFinding, 0, len(report.Findings)),
		Evaluations: make([]recordedEvaluation, 0, len(report.EdgeEvaluations)),
	}
	for _, f := range report.Findings {
		run.Findings = append(run.Findings, recordedFinding{Code: f.Code, Symbol: f.Symbol.Ref})
	}
	for _, e := range report.EdgeEvaluations {
		recorded := recordedEvaluation{Edge: e.Edge, Side: e.Side, Symbol: e.Symbol, State: e.State}
		if e.Finding != nil {
			recorded.Code = e.Finding.Code
		}
		run.Evaluations = append(run.Evaluations, recorded)
	}
	return run, stderr.String()
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

// extract writes one archive's files under root.
func extract(t *testing.T, archive, root string) {
	t.Helper()

	for name, body := range readArchive(t, archive) {
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
