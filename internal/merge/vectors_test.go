package merge

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"

	spec "github.com/cplieger/deadset-spec/v3"
	"github.com/cplieger/deadset/internal/config"
	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/verdict"
)

const vectorsDir = "vectors/merge"

// pendingCases are the published cases whose inputs carry a dead or absent
// edge evaluation, which the resolution step and the stale-edge step decide.
// This merge carries every evaluation unresolved, so it does not run them.
var pendingCases = map[string]bool{
	"edge-absent-on-every-side":          true,
	"pending-member-beside-a-stale-edge": true,
	"pending-member-on-a-second-edge":    true,
	"pending-pair-absent":                true,
	"pending-pair-dead":                  true,
	"pending-pair-live":                  true,
	"pending-pair-unevaluated":           true,
}

// admissionCases are the published cases the admission step refuses, each with
// the reason its refusal names.
var admissionCases = map[string]error{
	"configuration-built-and-not-built": ErrEntryState,
	"configuration-entries-differ":      ErrEntryIdentity,
	"conformance-not-passed":            ErrConformance,
	"consumer-entries-differ":           ErrEntryIdentity,
	"consumer-loaded-and-unavailable":   ErrEntryState,
	"findings-omitted":                  ErrOmitted,
	"one-analyzer-name-twice":           ErrAnalyzerName,
	"one-artifact-run-twice":            ErrAnalyzerName,
	"schema-version-out-of-range":       ErrSchemaVersion,
	"targets-differ":                    ErrTarget,
}

// vectorCase is one case directory of vectors/merge, decoded.
type vectorCase struct {
	expected []byte
	caller   Caller
	failOn   config.Severity
	inputs   []Input
	accepted []string
	exit     int
}

// TestMergeReproducesEveryPublishedVector merges every published case and runs
// the verdict over the merged report under the caller's failing severity: a
// case that produces a report matches expected.json byte for byte and its
// exit code, and a case that produces none ends in its typed refusal.
func TestMergeReproducesEveryPublishedVector(t *testing.T) {
	t.Parallel()

	for _, name := range caseNames(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := readCase(t, name)
			if unresolved := carriesUnresolvedEvaluation(c.inputs); unresolved != pendingCases[name] {
				t.Fatalf("case %s carries a dead or absent evaluation %t, and pendingCases names it %t",
					name, unresolved, pendingCases[name])
			}
			if pendingCases[name] {
				t.Skip("the inputs carry a dead or absent edge evaluation, and this merge resolves none")
			}
			merged, err := Merge(c.inputs, c.accepted, &c.caller)
			if c.expected == nil {
				want, known := admissionCases[name]
				if !known {
					t.Fatalf("case %s holds no expected.json and names no refusal this test expects", name)
				}
				if c.exit != verdict.Failure {
					t.Errorf("case %s expects exit %d with no merged report, want %d", name, c.exit, verdict.Failure)
				}
				if merged != nil || !errors.Is(err, want) {
					t.Errorf("Merge(%s) = %v, %v, want no report and an error wrapping %v", name, merged, err, want)
				}
				return
			}
			if err != nil {
				t.Fatalf("Merge(%s) = %v, want a report", name, err)
			}
			if got := encode(t, merged); !bytes.Equal(got, c.expected) {
				t.Errorf("Merge(%s) =\n%s\nwant expected.json\n%s", name, got, c.expected)
			}
			if got := verdict.Code(merged, c.failOn, verdict.On); got != c.exit {
				t.Errorf("Code(Merge(%s), %s, On) = %d, want expected_exit %d", name, c.failOn, got, c.exit)
			}
		})
	}
}

// carriesUnresolvedEvaluation reports whether any input holds an edge
// evaluation whose state is dead or absent.
func carriesUnresolvedEvaluation(inputs []Input) bool {
	for _, in := range inputs {
		for _, e := range in.Report.EdgeEvaluations {
			if e.State != report.StateLive {
				return true
			}
		}
	}
	return false
}

// TestMergeRefusesThePublishedAdmissionCasesNamingWhatFailed pins what each
// published admission refusal names: the analyzers, and what the rule read.
func TestMergeRefusesThePublishedAdmissionCasesNamingWhatFailed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		named []string
		check func(error) bool
	}{
		{
			name: "schema-version-out-of-range", named: []string{"deadset-go", "1.0.0", "6.0.0"},
			check: func(err error) bool {
				refused, ok := errors.AsType[*AdmissionError](err)
				return ok && refused.Analyzer == "deadset-go" && refused.SchemaVersion == "1.0.0" &&
					slices.Equal(refused.Accepted, []string{"6.0.0"})
			},
		},
		{
			name: "conformance-not-passed", named: []string{"deadset-go"},
			check: func(err error) bool {
				refused, ok := errors.AsType[*AdmissionError](err)
				return ok && refused.Analyzer == "deadset-go" && refused.Result == report.ResultFail
			},
		},
		{
			name: "findings-omitted", named: []string{"deadset-go", "1"},
			check: func(err error) bool {
				refused, ok := errors.AsType[*AdmissionError](err)
				return ok && refused.Analyzer == "deadset-go" && refused.Omitted == 1
			},
		},
		{
			name: "targets-differ", named: []string{"deadset-go", "deadset-ts"},
			check: func(err error) bool {
				refused, ok := errors.AsType[*TargetError](err)
				return ok && refused.Analyzers == [2]string{"deadset-go", "deadset-ts"} && refused.Targets[0] != refused.Targets[1]
			},
		},
		{
			name: "configuration-entries-differ", named: []string{"deadset-go", "example-go", "configurations"},
			check: entryRefusal(ErrEntryIdentity, "configurations", "configurations"),
		},
		{
			name: "consumer-entries-differ", named: []string{"consumers.loaded"},
			check: entryRefusal(ErrEntryIdentity, "consumers.loaded", "consumers.loaded"),
		},
		{
			name: "configuration-built-and-not-built", named: []string{"configurations", "configurations_not_built"},
			check: func(err error) bool {
				return entryRefusal(ErrEntryState, "configurations", "configurations_not_built")(err) ||
					entryRefusal(ErrEntryState, "configurations_not_built", "configurations")(err)
			},
		},
		{
			name: "consumer-loaded-and-unavailable", named: []string{"consumers.loaded", "consumers.unavailable"},
			check: func(err error) bool {
				return entryRefusal(ErrEntryState, "consumers.loaded", "consumers.unavailable")(err) ||
					entryRefusal(ErrEntryState, "consumers.unavailable", "consumers.loaded")(err)
			},
		},
		{
			name: "one-analyzer-name-twice", named: []string{"deadset-go"},
			check: func(err error) bool {
				refused, ok := errors.AsType[*NameError](err)
				return ok && refused.Name == "deadset-go" && refused.Versions[0] != refused.Versions[1] &&
					refused.Digests[0] != refused.Digests[1]
			},
		},
		{
			name: "one-artifact-run-twice", named: []string{"deadset-go"},
			check: func(err error) bool {
				refused, ok := errors.AsType[*NameError](err)
				return ok && refused.Name == "deadset-go" && refused.Digests[0] == refused.Digests[1]
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := readCase(t, tc.name)
			_, err := Merge(c.inputs, c.accepted, &c.caller)
			if !tc.check(err) {
				t.Fatalf("Merge(%s) = %#v, want the refusal the case states", tc.name, err)
			}
			for _, part := range tc.named {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("Merge(%s) error %q, want it to name %q", tc.name, err, part)
				}
			}
		})
	}
	var named []string
	for _, tc := range cases {
		named = append(named, tc.name)
	}
	if want := slices.Sorted(maps.Keys(admissionCases)); !slices.Equal(slices.Sorted(slices.Values(named)), want) {
		t.Errorf("the refusals this test pins are %q, want every admission case %q", named, want)
	}
}

// entryRefusal is whether an error is an [*EntryError] for reason, naming the
// two arrays in that order.
func entryRefusal(reason error, first, second string) func(error) bool {
	return func(err error) bool {
		refused, ok := errors.AsType[*EntryError](err)
		return ok && errors.Is(err, reason) && refused.Arrays == [2]string{first, second} && refused.ID != ""
	}
}

// TestMergeVectorComparisonFailsOnAOneFieldChange plants one changed field in
// a report merged from each published case and requires the comparison with
// expected.json to fail, so a merge that gets one member wrong cannot pass.
func TestMergeVectorComparisonFailsOnAOneFieldChange(t *testing.T) {
	t.Parallel()

	plants := map[string]func(*report.Report) bool{
		"analyzer version":     func(r *report.Report) bool { r.Analyzer.Version += "-x"; return true },
		"merged_from digest":   func(r *report.Report) bool { r.MergedFrom[0].Digest = "sha256:" + strings.Repeat("0", 64); return true },
		"target identity":      func(r *report.Report) bool { r.Target.Identity += "x"; return true },
		"a configuration's id": func(r *report.Report) bool { r.Configurations[0].ID += "x"; return true },
		"a test file rule":     func(r *report.Report) bool { r.TestFileRules[0].Matched++; return true },
		"totals.reasons":       func(r *report.Report) bool { r.Totals.ReasonsRecorded++; return true },
		"totals.deletable":     func(r *report.Report) bool { r.Totals.DeletableLines++; return true },
		"the carried analyzer": func(r *report.Report) bool {
			switch {
			case len(r.Findings) > 0:
				r.Findings[0].Analyzer += "x"
			case len(r.StaleSuppressions) > 0:
				r.StaleSuppressions[0].Analyzer += "x"
			default:
				return false
			}
			return true
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

				merged, err := Merge(c.inputs, c.accepted, &c.caller)
				if err != nil {
					t.Fatalf("Merge(%s) = %v, want a report", name, err)
				}
				if !change(merged) {
					t.Skipf("case %s carries no record whose %s changes", name, plant)
				}
				if got := encode(t, merged); bytes.Equal(got, c.expected) {
					t.Errorf("Merge(%s) with %s changed encodes to expected.json, want the comparison to fail", name, plant)
				}
			})
		}
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

// readCase decodes one case: its inputs in file order, each with the digest
// caller.json gives it, the accepted schema versions, the caller's facts, the
// expected exit code, and the expected merged report's bytes where the case has
// one. The two versions caller.json names are the ones this module writes, so a
// merging product passing its own versions writes the case's bytes.
func readCase(t *testing.T, name string) vectorCase {
	t.Helper()

	dir := path.Join(vectorsDir, name)
	c := vectorCase{accepted: lines(t, path.Join(dir, "accepted.txt"))}
	exit, err := strconv.Atoi(strings.TrimSpace(string(readVector(t, path.Join(dir, "expected_exit")))))
	if err != nil {
		t.Fatalf("Setup: %s/expected_exit: %v", dir, err)
	}
	c.exit = exit
	if expected, err := fs.ReadFile(spec.Vectors, path.Join(dir, "expected.json")); err == nil {
		c.expected = expected
	}

	var caller callerFile
	decoder := json.NewDecoder(bytes.NewReader(readVector(t, path.Join(dir, "caller.json"))))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&caller); err != nil {
		t.Fatalf("Setup: decode %s/caller.json: %v", dir, err)
	}
	if caller.SchemaVersion != report.SchemaVersion || caller.ContractVersion != report.ContractVersion {
		t.Fatalf("Setup: %s/caller.json writes schema %s and contract %s, want this module's %s and %s",
			dir, caller.SchemaVersion, caller.ContractVersion, report.SchemaVersion, report.ContractVersion)
	}
	c.caller = Caller{
		SchemaVersion:   caller.SchemaVersion,
		ContractVersion: caller.ContractVersion,
		Name:            caller.Analyzer.Name,
		Version:         caller.Analyzer.Version,
		Conformance:     caller.Analyzer.Conformance,
	}
	c.failOn = caller.FailOn

	files, err := fs.Glob(spec.Vectors, path.Join(dir, "inputs", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("Setup: %s/inputs holds no report (%v)", dir, err)
	}
	if named := slices.Sorted(maps.Keys(caller.Digests)); len(named) != len(files) {
		t.Fatalf("Setup: %s/caller.json names digests for %q, want one per input of %q", dir, named, files)
	}
	for _, file := range files {
		decoded, err := report.Decode(readVector(t, file))
		if err != nil {
			t.Fatalf("Setup: report.Decode(%s) = %v", file, err)
		}
		digest := caller.Digests[path.Base(file)]
		if digest == "" {
			t.Fatalf("Setup: %s/caller.json names no digest for %s", dir, path.Base(file))
		}
		c.inputs = append(c.inputs, Input{Report: decoded, Digest: digest})
	}
	return c
}

// callerFile is a case's caller.json.
type callerFile struct {
	Digests         map[string]string `json:"digests"`
	SchemaVersion   string            `json:"schema_version"`
	ContractVersion string            `json:"contract_version"`
	FailOn          config.Severity   `json:"fail_on"`
	Analyzer        struct {
		Name        string             `json:"name"`
		Version     string             `json:"version"`
		Conformance report.Conformance `json:"conformance"`
	} `json:"analyzer"`
}

func readVector(t *testing.T, name string) []byte {
	t.Helper()

	body, err := fs.ReadFile(spec.Vectors, name)
	if err != nil {
		t.Fatalf("Setup: read %s: %v", name, err)
	}
	return body
}

// lines is every non-empty line of a vector file.
func lines(t *testing.T, name string) []string {
	t.Helper()

	var held []string
	scanner := bufio.NewScanner(bytes.NewReader(readVector(t, name)))
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
