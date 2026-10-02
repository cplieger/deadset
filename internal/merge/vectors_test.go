package merge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"

	spec "github.com/cplieger/deadset-spec/v4"
	"github.com/cplieger/deadset/internal/config"
	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/verdict"
)

const vectorsDir = "vectors/merge"

// refusedCases are the published cases the merge refuses, each with the
// reason its refusal names: every admission refusal, and the pending finding
// no report resolves.
var refusedCases = map[string]error{
	"configuration-built-and-not-built": ErrEntryState,
	"configuration-entries-differ":      ErrEntryIdentity,
	"conformance-not-passed":            ErrConformance,
	"consumer-entries-differ":           ErrEntryIdentity,
	"consumer-loaded-and-unavailable":   ErrEntryState,
	"findings-omitted":                  ErrOmitted,
	"one-analyzer-name-twice":           ErrAnalyzerName,
	"one-artifact-run-twice":            ErrAnalyzerName,
	"pending-pair-unevaluated":          ErrUnresolvedEdge,
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
// exit code, and a case that produces none ends in its typed refusal, whose
// exit code is the case's.
func TestMergeReproducesEveryPublishedVector(t *testing.T) {
	t.Parallel()

	for _, name := range caseNames(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := answer(readCase(t, name), name); err != nil {
				t.Error(err)
			}
		})
	}
}

// answer is nil when the merge answers the published case c named name as
// the case states: its merged report byte for byte and its exit code, or its
// typed refusal and that refusal's exit code. Otherwise it says how the
// answer differs.
func answer(c vectorCase, name string) error {
	merged, err := Merge(c.inputs, c.accepted, &c.caller)
	if c.expected == nil {
		want, known := refusedCases[name]
		switch {
		case !known:
			return fmt.Errorf("case %s holds no expected.json and names no refusal this test expects", name)
		case merged != nil || !errors.Is(err, want):
			return fmt.Errorf("Merge(%s) = %v, %v, want no report and an error wrapping %v", name, merged, err, want)
		case verdict.ForError(err) != c.exit:
			return fmt.Errorf("ForError(Merge(%s)) = %d, want expected_exit %d", name, verdict.ForError(err), c.exit)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("Merge(%s) = %w, want a report", name, err)
	}
	var written bytes.Buffer
	if err := report.Encode(&written, merged); err != nil {
		return fmt.Errorf("report.Encode(Merge(%s)) = %w", name, err)
	}
	if !bytes.Equal(written.Bytes(), c.expected) {
		return fmt.Errorf("Merge(%s) =\n%s\nwant expected.json\n%s", name, written.Bytes(), c.expected)
	}
	if got := verdict.Code(merged, c.failOn, verdict.On); got != c.exit {
		return fmt.Errorf("Code(Merge(%s), %s, On) = %d, want expected_exit %d", name, c.failOn, got, c.exit)
	}
	return nil
}

// TestMergeRefusesThePublishedCasesNamingWhatFailed pins what each published
// refusal names: the analyzers, and what the rule read.
func TestMergeRefusesThePublishedCasesNamingWhatFailed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		named []string
		check func(error) bool
	}{
		{
			name: "schema-version-out-of-range", named: []string{"deadset-go", "1.0.0", "6.0.0, 6.1.0"},
			check: func(err error) bool {
				refused, ok := errors.AsType[*AdmissionError](err)
				return ok && refused.Analyzer == "deadset-go" && refused.SchemaVersion == "1.0.0" &&
					slices.Equal(refused.Accepted, []string{"6.0.0", "6.1.0"})
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
			name: "pending-pair-unevaluated", named: []string{"wire/ServerEvent", "provides", "go://example.com/app/internal/wire#ServerEvent", "deadset-go"},
			check: func(err error) bool {
				refused, ok := errors.AsType[*UnresolvedError](err)
				return ok && refused.Edge == "wire/ServerEvent" && refused.Side == report.SideProvides &&
					refused.Symbol == "go://example.com/app/internal/wire#ServerEvent" && refused.Analyzer == "deadset-go" &&
					slices.Equal(refused.Searched, []string{"deadset-go"})
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
	if want := slices.Sorted(maps.Keys(refusedCases)); !slices.Equal(slices.Sorted(slices.Values(named)), want) {
		t.Errorf("the refusals this test pins are %q, want every refused case %q", named, want)
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
// one. caller.json names this module's contract version and a schema version it
// accepts, so a merging product passing its own versions writes the case's
// bytes.
func readCase(t *testing.T, name string) vectorCase {
	t.Helper()

	dir := path.Join(vectorsDir, name)
	c := vectorCase{accepted: acceptedVersions(t, path.Join(dir, "accepted.txt"))}
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
	if !slices.Contains(report.SchemaVersions, caller.SchemaVersion) || caller.ContractVersion != report.ContractVersion {
		t.Fatalf("Setup: %s/caller.json writes schema %s and contract %s, want one of this module's %q and %s",
			dir, caller.SchemaVersion, caller.ContractVersion, report.SchemaVersions, report.ContractVersion)
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

// acceptedVersions is the schema versions an accepted.txt names, in its
// order: one line, the versions separated by single spaces.
func acceptedVersions(t *testing.T, name string) []string {
	t.Helper()

	line, found := strings.CutSuffix(string(readVector(t, name)), "\n")
	if !found || line == "" || strings.Contains(line, "\n") {
		t.Fatalf("Setup: %s holds %q, want one line ending in a line feed", name, line)
	}
	versions := strings.Split(line, " ")
	if slices.Contains(versions, "") {
		t.Fatalf("Setup: %s names %q, want versions separated by single spaces", name, versions)
	}
	return versions
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
