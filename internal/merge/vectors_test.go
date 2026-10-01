package merge

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	spec "github.com/cplieger/deadset-spec/v3"
	"github.com/cplieger/deadset/internal/report"
)

const vectorsDir = "vectors/merge"

// failureExit is the code of the failure row of contract/exit-codes.json, with
// which a run ends when the merge returns no report.
const failureExit = 3

// pendingCases are the published cases whose inputs carry a dead edge
// evaluation, which this merge carries unresolved.
var pendingCases = map[string]bool{
	"edge-absent-on-every-side": true,
	"pending-pair-dead":         true,
	"pending-pair-live":         true,
	"pending-pair-unevaluated":  true,
}

// admissionCases are the published cases the admission step refuses, each with
// the reason its refusal names.
var admissionCases = map[string]error{
	"conformance-not-passed":      ErrConformance,
	"schema-version-out-of-range": ErrSchemaVersion,
}

// vectorCase is one case directory of vectors/merge, decoded.
type vectorCase struct {
	expected []byte
	self     report.Analyzer
	inputs   []Input
	accepted []string
	exit     int
}

// TestMergeReproducesEveryPublishedVector merges every published case that
// produces a report and compares the encoded report with expected.json byte
// for byte, and runs every case that produces none to its typed refusal.
func TestMergeReproducesEveryPublishedVector(t *testing.T) {
	t.Parallel()

	for _, name := range caseNames(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if pendingCases[name] {
				t.Skip("the inputs carry a dead edge evaluation, and this merge does not resolve one")
			}
			c := readCase(t, name)
			merged, err := Merge(c.inputs, c.accepted, &c.self)
			if c.expected == nil {
				want, known := admissionCases[name]
				if !known {
					t.Fatalf("case %s holds no expected.json and names no refusal this test expects", name)
				}
				if c.exit != failureExit {
					t.Errorf("case %s expects exit %d with no merged report, want %d", name, c.exit, failureExit)
				}
				if merged != nil || !errors.Is(err, want) {
					t.Errorf("Merge(%s) = %v, %v, want no report and an error wrapping %v", name, merged, err, want)
				}
				return
			}
			if err != nil {
				t.Fatalf("Merge(%s) = %v, want a report", name, err)
			}
			if c.exit == failureExit {
				t.Errorf("case %s holds expected.json and expects exit %d, which a merged report cannot end in", name, c.exit)
			}
			if got := encode(t, merged); !bytes.Equal(got, c.expected) {
				t.Errorf("Merge(%s) =\n%s\nwant expected.json\n%s", name, got, c.expected)
			}
		})
	}
}

// TestMergeRefusesThePublishedAdmissionCasesNamingWhatFailed pins what each
// published admission refusal names: both schema versions, or the analyzer
// whose conformance result is not a pass.
func TestMergeRefusesThePublishedAdmissionCasesNamingWhatFailed(t *testing.T) {
	t.Parallel()

	t.Run("schema-version-out-of-range", func(t *testing.T) {
		t.Parallel()

		_, err := Merge(readCase(t, "schema-version-out-of-range").inputs, []string{"6.0.0"}, &report.Analyzer{})
		refused, ok := errors.AsType[*AdmissionError](err)
		if !ok {
			t.Fatalf("Merge(schema-version-out-of-range) = %v, want an *AdmissionError", err)
		}
		if refused.Analyzer != "deadset-go" || refused.SchemaVersion != "1.0.0" || !slices.Equal(refused.Accepted, []string{"6.0.0"}) {
			t.Errorf("Merge(schema-version-out-of-range) = %#v, want deadset-go's schema version 1.0.0 against [6.0.0]", refused)
		}
		if message := err.Error(); !strings.Contains(message, "1.0.0") || !strings.Contains(message, "6.0.0") {
			t.Errorf("Merge(schema-version-out-of-range) error %q, want both versions named", message)
		}
	})
	t.Run("conformance-not-passed", func(t *testing.T) {
		t.Parallel()

		_, err := Merge(readCase(t, "conformance-not-passed").inputs, []string{"6.0.0"}, &report.Analyzer{})
		refused, ok := errors.AsType[*AdmissionError](err)
		if !ok {
			t.Fatalf("Merge(conformance-not-passed) = %v, want an *AdmissionError", err)
		}
		if refused.Analyzer != "deadset-go" || refused.Result != report.ResultFail {
			t.Errorf("Merge(conformance-not-passed) = %#v, want deadset-go refused on its fail result", refused)
		}
		if message := err.Error(); !strings.Contains(message, "deadset-go") {
			t.Errorf("Merge(conformance-not-passed) error %q, want the analyzer named", message)
		}
	})
}

// TestMergeVectorComparisonFailsOnAOneFieldChange plants one changed field in
// a report merged from each published case and requires the comparison with
// expected.json to fail, so a merge that gets one member wrong cannot pass.
func TestMergeVectorComparisonFailsOnAOneFieldChange(t *testing.T) {
	t.Parallel()

	plants := map[string]func(*report.Report){
		"analyzer version":     func(r *report.Report) { r.Analyzer.Version += "-x" },
		"merged_from digest":   func(r *report.Report) { r.MergedFrom[0].Digest = "sha256:" + strings.Repeat("0", 64) },
		"target identity":      func(r *report.Report) { r.Target.Identity += "x" },
		"a configuration's id": func(r *report.Report) { r.Configurations[0].ID += "x" },
		"a test file rule":     func(r *report.Report) { r.TestFileRules[0].Matched++ },
		"totals.reasons":       func(r *report.Report) { r.Totals.ReasonsRecorded++ },
		"totals.deletable":     func(r *report.Report) { r.Totals.DeletableLines++ },
		"the carried analyzer": func(r *report.Report) {
			switch {
			case len(r.Findings) > 0:
				r.Findings[0].Analyzer += "x"
			case len(r.StaleSuppressions) > 0:
				r.StaleSuppressions[0].Analyzer += "x"
			}
		},
	}
	for _, name := range caseNames(t) {
		if pendingCases[name] {
			continue
		}
		c := readCase(t, name)
		if c.expected == nil {
			continue
		}
		for plant, change := range plants {
			t.Run(name+"_"+strings.ReplaceAll(plant, " ", "_"), func(t *testing.T) {
				t.Parallel()

				merged, err := Merge(c.inputs, c.accepted, &c.self)
				if err != nil {
					t.Fatalf("Merge(%s) = %v, want a report", name, err)
				}
				change(merged)
				if got := encode(t, merged); bytes.Equal(got, c.expected) {
					t.Errorf("Merge(%s) with %s changed encodes to expected.json, want the comparison to fail", name, plant)
				}
			})
		}
	}
}

// TestReadCaseTakesTheCallerFactsFromCallerJSON pins the caller.json source:
// a case whose caller.json carries other facts than its expected.json is read
// with the facts caller.json names.
func TestReadCaseTakesTheCallerFactsFromCallerJSON(t *testing.T) {
	t.Parallel()

	tree := fstest.MapFS{}
	for _, file := range []string{"accepted.txt", "expected_exit", "expected.json", "inputs/00-go.json"} {
		name := path.Join(vectorsDir, "one-report", file)
		tree[name] = &fstest.MapFile{Data: readVector(t, spec.Vectors, name)}
	}
	caller := callerFile{
		Description: "the facts the caller passes",
		Digests:     map[string]string{"00-go.json": digest("b")},
		Analyzer:    self,
	}
	body, err := json.Marshal(caller)
	if err != nil {
		t.Fatalf("Setup: json.Marshal(caller.json) = %v", err)
	}
	tree[path.Join(vectorsDir, "one-report", "caller.json")] = &fstest.MapFile{Data: body}

	c := readCaseFrom(t, tree, "one-report")
	if !reflect.DeepEqual(c.self, self) || c.inputs[0].Digest != digest("b") {
		t.Errorf("readCaseFrom(one-report with caller.json) = %+v and digest %s, want %+v and %s",
			c.self, c.inputs[0].Digest, self, digest("b"))
	}
}

// caseNames is the name of every case directory of vectors/merge.
func caseNames(t *testing.T) []string {
	t.Helper()

	entries, err := fs.ReadDir(spec.Vectors, vectorsDir)
	if err != nil {
		t.Fatalf("Setup: read %s: %v", vectorsDir, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		t.Fatalf("Setup: %s holds no case", vectorsDir)
	}
	return names
}

// readCase decodes one case: its inputs in file order, the accepted schema
// versions, the expected exit code, the expected merged report's bytes where
// the case has one, and the two facts a merge takes from its caller.
func readCase(t *testing.T, name string) vectorCase {
	t.Helper()

	return readCaseFrom(t, spec.Vectors, name)
}

// readCaseFrom is readCase over the vectors tree vectors holds.
func readCaseFrom(t *testing.T, vectors fs.FS, name string) vectorCase {
	t.Helper()

	dir := path.Join(vectorsDir, name)
	c := vectorCase{accepted: lines(t, vectors, path.Join(dir, "accepted.txt"))}
	exit, err := strconv.Atoi(strings.TrimSpace(string(readVector(t, vectors, path.Join(dir, "expected_exit")))))
	if err != nil {
		t.Fatalf("Setup: %s/expected_exit: %v", dir, err)
	}
	c.exit = exit
	if expected, err := fs.ReadFile(vectors, path.Join(dir, "expected.json")); err == nil {
		c.expected = expected
	}
	files, err := fs.Glob(vectors, path.Join(dir, "inputs", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("Setup: %s/inputs holds no report (%v)", dir, err)
	}
	reports := make([]*report.Report, len(files))
	for i, file := range files {
		decoded, err := report.Decode(readVector(t, vectors, file))
		if err != nil {
			t.Fatalf("Setup: report.Decode(%s) = %v", file, err)
		}
		reports[i] = decoded
	}
	self, digests := callerFacts(t, vectors, dir, c.expected, files, reports)
	for i, decoded := range reports {
		c.inputs = append(c.inputs, Input{Report: decoded, Digest: digests[i]})
	}
	c.self = self
	return c
}

// callerFile is a case's caller.json: the merging product's analyzer member,
// and each input's artifact digest by the input's file name.
type callerFile struct {
	Description string            `json:"description"`
	Digests     map[string]string `json:"digests"`
	Analyzer    report.Analyzer   `json:"analyzer"`
}

// callerFacts is the merging product's analyzer member and each input's
// artifact digest, which no input report holds. They come from the case's
// caller.json where it has one, and otherwise from its expected.json, which
// states what the caller passed; a case with neither ends in admission, which
// reads neither.
func callerFacts(t *testing.T, vectors fs.FS, dir string, expected []byte, files []string, inputs []*report.Report) (report.Analyzer, []string) {
	t.Helper()

	digests := make([]string, len(inputs))
	if body, err := fs.ReadFile(vectors, path.Join(dir, "caller.json")); err == nil {
		var caller callerFile
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&caller); err != nil {
			t.Fatalf("Setup: decode %s/caller.json: %v", dir, err)
		}
		for i, file := range files {
			if digests[i] = caller.Digests[path.Base(file)]; digests[i] == "" {
				t.Fatalf("Setup: %s/caller.json names no digest for %s", dir, path.Base(file))
			}
		}
		return caller.Analyzer, digests
	}
	if expected == nil {
		return report.Analyzer{}, digests
	}
	merged, err := report.Decode(expected)
	if err != nil {
		t.Fatalf("Setup: report.Decode(%s/expected.json) = %v", dir, err)
	}
	for i, input := range inputs {
		for _, entry := range merged.MergedFrom {
			if entry.Name == input.Analyzer.Name && entry.Version == input.Analyzer.Version {
				digests[i] = entry.Digest
			}
		}
		if digests[i] == "" {
			t.Fatalf("Setup: %s/expected.json names no merged_from entry for %s %s", dir, input.Analyzer.Name, input.Analyzer.Version)
		}
	}
	return merged.Analyzer, digests
}

func readVector(t *testing.T, vectors fs.FS, name string) []byte {
	t.Helper()

	body, err := fs.ReadFile(vectors, name)
	if err != nil {
		t.Fatalf("Setup: read %s: %v", name, err)
	}
	return body
}

// lines is every non-empty line of a vector file.
func lines(t *testing.T, vectors fs.FS, name string) []string {
	t.Helper()

	var held []string
	scanner := bufio.NewScanner(bytes.NewReader(readVector(t, vectors, name)))
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			held = append(held, line)
		}
	}
	return held
}

// encode is a merged report in the encoding expected.json is written in.
func encode(t *testing.T, merged *report.Report) []byte {
	t.Helper()

	var written bytes.Buffer
	if err := report.Encode(&written, merged); err != nil {
		t.Fatalf("report.Encode(the merged report) = %v", err)
	}
	return written.Bytes()
}
