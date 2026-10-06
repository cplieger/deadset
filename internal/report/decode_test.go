package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	spec "github.com/cplieger/deadset-spec/v6"
)

// TestDecodeReadsEveryExampleReportBack decodes every report the Contract
// publishes as an example or as the round of a baseline vector, and encodes
// it again, which must give the same document: the documents are written in
// another layout, so the two are compared as JSON values.
func TestDecodeReadsEveryExampleReportBack(t *testing.T) {
	t.Parallel()

	documents := map[string]fs.FS{}
	for _, name := range glob(t, spec.Examples, "examples/reports/*.json") {
		documents[name] = spec.Examples
	}
	for _, name := range glob(t, spec.Vectors, "vectors/baseline/*/report.json") {
		documents[name] = spec.Vectors
	}
	for name, tree := range documents {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			body := read(t, tree, name)
			decoded, err := Decode(body)
			if err != nil {
				t.Fatalf("Decode(%s) = %v, want a report", name, err)
			}
			var encoded bytes.Buffer
			if err := Encode(&encoded, decoded); err != nil {
				t.Fatalf("Encode(Decode(%s)) = %v", name, err)
			}
			if got, want := value(t, encoded.Bytes()), value(t, body); !reflect.DeepEqual(got, want) {
				t.Errorf("Encode(Decode(%s)) =\n%s\nwant the document it was decoded from", name, encoded.Bytes())
			}
		})
	}
}

// TestDecodeReadsEveryMergeVectorBackByteForByte decodes every report a merge
// vector holds, its inputs and its expected merged report alike, and encodes
// it again, which must give the file's own bytes: the vectors are written in
// the encoding a merged report is compared in, except that an input may hold
// U+2028 and U+2029 unescaped, which the encoding writes as escapes.
func TestDecodeReadsEveryMergeVectorBackByteForByte(t *testing.T) {
	t.Parallel()

	names := append(glob(t, spec.Vectors, "vectors/merge/*/inputs/*.json"),
		glob(t, spec.Vectors, "vectors/merge/*/expected.json")...)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			body := read(t, spec.Vectors, name)
			decoded, err := Decode(body)
			if err != nil {
				t.Fatalf("Decode(%s) = %v, want a report", name, err)
			}
			var encoded bytes.Buffer
			if err := Encode(&encoded, decoded); err != nil {
				t.Fatalf("Encode(Decode(%s)) = %v", name, err)
			}
			want := strings.NewReplacer("\u2028", `\u2028`, "\u2029", `\u2029`).Replace(string(body))
			if encoded.String() != want {
				t.Errorf("Encode(Decode(%s)) =\n%s\nwant the file's bytes, its line and paragraph separators escaped\n%s",
					name, encoded.Bytes(), want)
			}
		})
	}
}

// TestEncodeWritesEveryCharacterStrictJSONLeavesUnescapedAsItIs pins that a
// character encoding/json escapes by default, in a member a type writes by
// itself and in one it does not, is written as the document wrote it.
func TestEncodeWritesEveryCharacterStrictJSONLeavesUnescapedAsItIs(t *testing.T) {
	t.Parallel()

	original := string(read(t, spec.Vectors, "vectors/merge/one-report/inputs/00-go.json"))
	edited := strings.ReplaceAll(original, "linux-amd64", "linux<amd64>&")
	edited = strings.Replace(edited, `"message": "unexported function`, `"message": "<T> & unexported function`, 1)
	if strings.Count(edited, "<") != 3 {
		t.Fatalf("Setup: the edit placed %d angle brackets, want 3", strings.Count(edited, "<"))
	}

	decoded, err := Decode([]byte(edited))
	if err != nil {
		t.Fatalf("Decode(edited one-report input) = %v", err)
	}
	var encoded bytes.Buffer
	if err := Encode(&encoded, decoded); err != nil {
		t.Fatalf("Encode(Decode(edited one-report input)) = %v", err)
	}
	if encoded.String() != edited {
		t.Errorf("Encode(Decode(edited one-report input)) =\n%s\nwant the edited bytes\n%s", encoded.String(), edited)
	}
}

// TestDecodeReadsEveryExampleFindingBack holds every finding the Contract
// publishes as an example to the finding schema, and encodes it again.
func TestDecodeReadsEveryExampleFindingBack(t *testing.T) {
	t.Parallel()

	for _, name := range glob(t, spec.Examples, "examples/findings/*.json") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			body := read(t, spec.Examples, name)
			decoded, err := decodeFinding(body)
			if err != nil {
				t.Fatalf("decodeFinding(%s) = %v, want a finding", name, err)
			}
			encoded, err := marshal(decoded)
			if err != nil {
				t.Fatalf("marshal(decodeFinding(%s)) = %v", name, err)
			}
			if got, want := value(t, encoded), value(t, body); !reflect.DeepEqual(got, want) {
				t.Errorf("marshal(decodeFinding(%s)) =\n%s\nwant the document it was decoded from", name, encoded)
			}
		})
	}
}

// TestDecodeRefusesEveryNegative decodes every refused document the Contract
// publishes against the report or the finding schema. Each must be refused at
// the value the index names or a member beneath it, for the reason the
// constraint the index names states.
func TestDecodeRefusesEveryNegative(t *testing.T) {
	t.Parallel()

	var index struct {
		Negatives []struct {
			File         string `json:"file"`
			Schema       string `json:"schema"`
			Constraint   string `json:"constraint"`
			InstancePath string `json:"instance_path"`
		} `json:"negatives"`
	}
	if err := json.Unmarshal(read(t, spec.Examples, "examples/negatives/index.json"), &index); err != nil {
		t.Fatalf("Setup: decode examples/negatives/index.json: %v", err)
	}
	decoders := map[string]func([]byte) error{
		"contract/report.schema.json": func(body []byte) error {
			_, err := Decode(body)
			return err
		},
		"contract/finding.schema.json": func(body []byte) error {
			_, err := decodeFinding(body)
			return err
		},
	}
	reasons := map[string]error{
		"required":             errMissing,
		"additionalProperties": errUndeclared,
		"not":                  errForbidden,
		"oneOf":                errShape,
		"pattern":              errValue,
		"enum":                 errValue,
		"const":                errValue,
		"minimum":              errValue,
		"minItems":             errValue,
		"maxItems":             errValue,
	}

	tested := 0
	for _, row := range index.Negatives {
		decode, ours := decoders[row.Schema]
		if !ours {
			continue
		}
		tested++
		t.Run(row.File, func(t *testing.T) {
			t.Parallel()

			err := decode(read(t, spec.Examples, "examples/negatives/"+row.File))
			var refused *Error
			if !errors.As(err, &refused) {
				t.Fatalf("decoding %s = %v, want an *Error refusing %s at %q", row.File, err, row.Constraint, row.InstancePath)
			}
			if refused.Pointer != row.InstancePath && !strings.HasPrefix(refused.Pointer, row.InstancePath+"/") {
				t.Errorf("decoding %s refused %q, want %q or a member beneath it: %v",
					row.File, refused.Pointer, row.InstancePath, err)
			}
			if want, known := reasons[row.Constraint]; !known || !errors.Is(err, want) {
				t.Errorf("decoding %s = %v, want the refusal %s states (%v)", row.File, err, row.Constraint, want)
			}
		})
	}
	if tested == 0 {
		t.Fatal("Setup: the negatives index names no document against the report or the finding schema")
	}
}

// TestDecodeRefusesWhatEncodingJSONAdmits pins the refusals a closed key list
// makes that encoding/json does not: a member spelled in another case, a
// member named twice, a null, an absent required member, and an optional
// member present with no value. Each case edits one valid report.
func TestDecodeRefusesWhatEncodingJSONAdmits(t *testing.T) {
	t.Parallel()

	valid := read(t, spec.Vectors, "vectors/merge/one-report/inputs/00-go.json")
	for name, tt := range map[string]struct {
		old, new    string
		wantPointer string
		wantReason  error
	}{
		"a member spelled in another case": {
			old: `"findings": [`, new: `"Findings": [`,
			wantPointer: "/Findings", wantReason: errUndeclared,
		},
		"a member named twice": {
			old: `"findings": [`, new: `"findings": [], "findings": [`,
			wantPointer: "/findings", wantReason: errDuplicate,
		},
		"a required array that is null": {
			old: `"excluded_by_cgo": []`, new: `"excluded_by_cgo": null`,
			wantPointer: "/excluded_by_cgo", wantReason: errNull,
		},
		"an array element that is null": {
			old: `"languages": [`, new: `"languages": [null, `,
			wantPointer: "/analyzer/languages/0", wantReason: errNull,
		},
		"an absent required member": {
			old: `"excluded_by_cgo": [],`, new: ``,
			wantPointer: "/excluded_by_cgo", wantReason: errMissing,
		},
		"an optional member present with no value": {
			old: `"parent": "go://example.com/app/internal/store#"`, new: `"parent": ""`,
			wantPointer: "/findings/0/symbol/parent", wantReason: errValue,
		},
		"a value of the wrong type": {
			old: `"test_only": false`, new: `"test_only": "false"`,
			wantPointer: "/findings/0/test_only", wantReason: errValue,
		},
		"a configuration of neither shape": {
			old: `"os": "linux",`, new: ``,
			wantPointer: "/configurations/0/os", wantReason: errMissing,
		},
		"an error member on a configuration the analysis ran": {
			old: `"tags": []`, new: `"tags": [], "error": "go: no Go files"`,
			wantPointer: "/configurations/0/error", wantReason: errUndeclared,
		},
		"a configuration repeated": {
			old: `"configurations": [`, new: `"configurations": [{"id": "linux-amd64", "os": "linux", "arch": "amd64", "tags": []}, `,
			wantPointer: "/configurations/1", wantReason: errValue,
		},
		"a whole document that is not an object": {
			old: string(valid), new: `null`,
			wantPointer: "", wantReason: errValue,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			edited := strings.Replace(string(valid), tt.old, tt.new, 1)
			if edited == string(valid) {
				t.Fatalf("Setup: %q is not in the report", tt.old)
			}
			_, err := Decode([]byte(edited))
			var refused *Error
			if !errors.As(err, &refused) {
				t.Fatalf("Decode(%s) = %v, want an *Error at %q", name, err, tt.wantPointer)
			}
			if refused.Pointer != tt.wantPointer || !errors.Is(err, tt.wantReason) {
				t.Errorf("Decode(%s) = %v (at %q), want %v at %q", name, err, refused.Pointer, tt.wantReason, tt.wantPointer)
			}
		})
	}
}

// TestEncodeRefusesAReportDecodeWouldRefuse pins that Encode holds a report to
// the rules Decode applies and writes nothing when it refuses one.
func TestEncodeRefusesAReportDecodeWouldRefuse(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		edit        func(*Report)
		wantPointer string
		wantReason  error
	}{
		"a required array left nil": {
			edit:        func(r *Report) { r.StaleSuppressions = nil },
			wantPointer: "/stale_suppressions", wantReason: errNull,
		},
		"a configuration of both shapes": {
			edit:        func(r *Report) { r.Configurations[0].Project = "tsconfig.json" },
			wantPointer: "/configurations/0", wantReason: errShape,
		},
		"a dead evaluation with no pending finding": {
			edit: func(r *Report) {
				r.EdgeEvaluations = []EdgeEvaluation{{Edge: "wire/ServerEvent", Side: SideProvides, Symbol: "go://example.com/app#ServerEvent", State: StateDead}}
			},
			wantPointer: "/edge_evaluations/0/finding", wantReason: errMissing,
		},
		"a finding that names a relation its code forbids": {
			edit:        func(r *Report) { r.Findings[0].Code = "DS1301" },
			wantPointer: "/findings/0/liveness_relation", wantReason: errForbidden,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			decoded, err := Decode(read(t, spec.Vectors, "vectors/merge/one-report/inputs/00-go.json"))
			if err != nil {
				t.Fatalf("Setup: Decode(one-report input) = %v", err)
			}
			tt.edit(decoded)
			var written bytes.Buffer
			err = Encode(&written, decoded)
			var refused *Error
			if !errors.As(err, &refused) {
				t.Fatalf("Encode(%s) = %v, want an *Error at %q", name, err, tt.wantPointer)
			}
			if refused.Pointer != tt.wantPointer || !errors.Is(err, tt.wantReason) {
				t.Errorf("Encode(%s) = %v (at %q), want %v at %q", name, err, refused.Pointer, tt.wantReason, tt.wantPointer)
			}
			if written.Len() != 0 {
				t.Errorf("Encode(%s) wrote %d bytes, want none on a refusal", name, written.Len())
			}
		})
	}
}

// decodeFinding holds one finding document to the finding schema, the way a
// report's decode holds each finding it carries.
func decodeFinding(data []byte) (*Finding, error) {
	var read Finding
	if err := decodeObject(data, reflect.ValueOf(&read).Elem()); err != nil {
		return nil, err
	}
	if err := read.validate(); err != nil {
		return nil, err
	}
	return &read, nil
}

// glob is every file of tree that pattern matches, which must be one at least.
func glob(t *testing.T, tree fs.FS, pattern string) []string {
	t.Helper()

	names, err := fs.Glob(tree, pattern)
	if err != nil || len(names) == 0 {
		t.Fatalf("Setup: glob %s = %q, %v; want one file at least", pattern, names, err)
	}
	return names
}

// read is the bytes of one file of tree.
func read(t *testing.T, tree fs.FS, name string) []byte {
	t.Helper()

	body, err := fs.ReadFile(tree, name)
	if err != nil {
		t.Fatalf("Setup: read %s: %v", name, err)
	}
	return body
}

// value is the JSON value body holds, for comparing two documents whatever
// their layout.
func value(t *testing.T, body []byte) any {
	t.Helper()

	var held any
	if err := json.Unmarshal(body, &held); err != nil {
		t.Fatalf("Setup: decode %s: %v", body, err)
	}
	return held
}
