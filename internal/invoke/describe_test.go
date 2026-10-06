package invoke_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	spec "github.com/cplieger/deadset-spec/v6"
	"github.com/cplieger/deadset/internal/invoke"
	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/rundir"
	"github.com/cplieger/deadset/internal/verdict"
)

// accepted is the report schema range the handshake tests admit under.
var accepted = []string{report.SchemaVersion}

// digest is a conformance digest of the shape the Contract states.
var digest = "sha256:" + strings.Repeat("0a", 32)

// The members of a describe document the Go analyzer could print, one to an
// index, so a case replaces or drops one by its position.
const (
	memberName = iota
	memberVersion
	memberContractVersion
	memberSchemaVersions
	memberLanguages
	memberConformance
)

// describedMembers is every member of a describe document admitted under
// accepted, as JSON text.
func describedMembers() []string {
	return []string{
		`"name": "deadset-go"`,
		`"version": "1.20.0"`,
		`"contract_version": "3.2.0"`,
		`"schema_versions_accepted": ["` + report.SchemaVersion + `"]`,
		`"languages": ["go"]`,
		`"conformance": {"corpus_version": "1.9.0", "result": "pass", "digest": "` + digest + `"}`,
	}
}

// replaced is members with the member at i replaced by member, or dropped when
// member is empty.
func replaced(members []string, i int, member string) []string {
	out := slices.Clone(members)
	if member == "" {
		return slices.Delete(out, i, i+1)
	}
	out[i] = member
	return out
}

// document is the describe document holding members, in order.
func document(members []string) []byte {
	return []byte("{\n  " + strings.Join(members, ",\n  ") + "\n}\n")
}

// describer writes a fake analyzer whose describe verb prints printed to
// stdout, a line to stderr, and exits with exit.
func describer(t *testing.T, exit int, printed []byte) fake {
	t.Helper()

	dir := t.TempDir()
	written := filepath.Join(dir, "described.json")
	if err := os.WriteFile(written, printed, 0o600); err != nil {
		t.Fatalf("Setup: write %s: %v", written, err)
	}
	return script(t, dir, "cat '"+written+"'\necho 'a diagnostic' >&2\nexit "+strconv.Itoa(exit)+"\n")
}

// kept is the files of the provider entry named name in a fresh run directory.
func kept(t *testing.T, name string) rundir.Entry {
	t.Helper()

	dir, err := rundir.Create(filepath.Join(t.TempDir(), "run"))
	if err != nil {
		t.Fatalf("Setup: create the run directory: %v", err)
	}
	entry, err := dir.Entry(name)
	if err != nil {
		t.Fatalf("Setup: Entry(%q): %v", name, err)
	}
	return entry
}

// handshakeRefusal is the *invoke.HandshakeError err carries, after checking
// that err satisfies errors.Is(err, want) and that the verdict ends the run
// with the failure code.
func handshakeRefusal(t *testing.T, err, want error) *invoke.HandshakeError {
	t.Helper()

	refused, ok := errors.AsType[*invoke.HandshakeError](err)
	if !ok {
		t.Fatalf("Describe() = %v, want an *invoke.HandshakeError", err)
	}
	if !errors.Is(err, want) {
		t.Errorf("Describe() = %v, want an error satisfying errors.Is(err, %v)", err, want)
	}
	if code := verdict.ForError(err); code != verdict.Failure {
		t.Errorf("verdict.ForError(%v) = %d, want %d", err, code, verdict.Failure)
	}
	return refused
}

// TestDescribeAdmitsAnAnalyzerWithAPassAndAnAcceptedVersion pins the admitted
// handshake: the describe verb run alone in the request's directory, the
// description read member for member, the printed document kept byte for byte
// at the entry's describe file without the analyzer's stderr, which goes to
// the diagnostics, and an analyzer admitted when one of several versions it
// reads is accepted.
func TestDescribeAdmitsAnAnalyzerWithAPassAndAnAcceptedVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		versions string
		want     []string
	}{
		{name: "one-version", versions: `["` + report.SchemaVersion + `"]`, want: []string{report.SchemaVersion}},
		{name: "one-of-several", versions: `["5.0.0", "` + report.SchemaVersion + `"]`, want: []string{"5.0.0", report.SchemaVersion}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			printed := document(replaced(describedMembers(), memberSchemaVersions, `"schema_versions_accepted": `+c.versions))
			analyzer := describer(t, 0, printed)
			req := request(t, "deadset-go", analyzer.command)
			var diagnostics bytes.Buffer
			req.Diagnostics = &diagnostics
			entry := kept(t, req.Analyzer)

			got, err := invoke.Describe(t.Context(), &req, entry, accepted)
			if err != nil {
				t.Fatalf("Describe(an analyzer with a pass reading %s) = %v, want it admitted", c.versions, err)
			}
			want := &invoke.Description{
				Conformance:            &report.Conformance{CorpusVersion: "1.9.0", Result: report.ResultPass, Digest: digest},
				Name:                   "deadset-go",
				Version:                "1.20.0",
				ContractVersion:        "3.2.0",
				SchemaVersionsAccepted: c.want,
				Languages:              []string{"go"},
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Describe() = %+v, want %+v", got, want)
			}

			if keptBytes, err := os.ReadFile(entry.Describe()); err != nil || !bytes.Equal(keptBytes, printed) {
				t.Errorf("Describe() kept %q at %s (%v), want the bytes the analyzer printed, %q", keptBytes, entry.Describe(), err, printed)
			}
			if diagnostics.String() != "a diagnostic\n" {
				t.Errorf("Describe() copied %q to the diagnostics, want the analyzer's stderr, %q", diagnostics.String(), "a diagnostic\n")
			}
			if args, _ := os.ReadFile(analyzer.args); string(args) != "describe\n" {
				t.Errorf("Describe() ran the analyzer with %q, want the describe verb alone", args)
			}
			if ranIn, _ := os.ReadFile(analyzer.dir); strings.TrimSuffix(string(ranIn), "\n") != req.Dir {
				t.Errorf("Describe() ran the analyzer in %q, want %q", ranIn, req.Dir)
			}
		})
	}
}

// TestDescribeRefusesACommandItCannotRun pins step 1 of the handshake: a
// command that is not an absolute path is refused before anything runs, and
// an absolute path naming no file or a file that cannot be executed ends the
// run with the failure code naming the entry and its command, and nothing is
// kept.
func TestDescribeRefusesACommandItCannotRun(t *testing.T) {
	t.Parallel()

	notExecutable := filepath.Join(t.TempDir(), "analyzer")
	if err := os.WriteFile(notExecutable, []byte("#!/bin/sh\nexit 0\n"), 0o600); err != nil {
		t.Fatalf("Setup: write %s: %v", notExecutable, err)
	}
	for name, c := range map[string]struct {
		command string
		want    error
	}{
		"absent-path":    {command: filepath.Join(t.TempDir(), "absent"), want: fs.ErrNotExist},
		"not-executable": {command: notExecutable, want: fs.ErrPermission},
		"a-name":         {command: "deadset-go", want: invoke.ErrUnresolvedCommand},
		"relative-path":  {command: "bin/deadset-go", want: invoke.ErrUnresolvedCommand},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := request(t, "deadset-third-party", c.command)
			entry := kept(t, req.Analyzer)
			got, err := invoke.Describe(t.Context(), &req, entry, accepted)
			if got != nil {
				t.Errorf("Describe(command %s) = %+v, want no description", c.command, got)
			}
			refused := handshakeRefusal(t, err, c.want)
			if refused.Exit != -1 {
				t.Errorf("Describe(command %s) refused with exit %d, want -1", c.command, refused.Exit)
			}
			for _, named := range []string{"deadset-third-party", c.command} {
				if !strings.Contains(err.Error(), named) {
					t.Errorf("Describe(command %s) = %q, want the message to name %q", c.command, err, named)
				}
			}
			if _, err := os.Stat(entry.Describe()); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("after Describe(command %s), Stat(%s) = %v, want nothing kept", c.command, entry.Describe(), err)
			}
		})
	}
}

// TestDescribeRefusesAnAnalyzerDescribingItselfUnderAnotherName pins that the
// description must name the provider entry it was run for: an entry whose
// command runs a different analyzer ends the run with the failure code, naming
// the entry and the name the analyzer gave.
func TestDescribeRefusesAnAnalyzerDescribingItselfUnderAnotherName(t *testing.T) {
	t.Parallel()

	req := request(t, "deadset-go-fork", describer(t, 0, document(describedMembers())).command)
	got, err := invoke.Describe(t.Context(), &req, kept(t, req.Analyzer), accepted)
	if got != nil {
		t.Errorf("Describe(an entry named deadset-go-fork running deadset-go) = %+v, want no description", got)
	}
	handshakeRefusal(t, err, invoke.ErrDescribedName)
	for _, named := range []string{"deadset-go-fork", `"deadset-go"`} {
		if !strings.Contains(err.Error(), named) {
			t.Errorf("Describe(an entry named deadset-go-fork running deadset-go) = %q, want the message to name %s", err, named)
		}
	}
}

// TestDescribeRefusesAnAnalyzerWithNoConformancePass pins that an analyzer
// recording a failing result, or no conformance record at all, ends the run
// with the failure code naming the entry and, where it records one, the
// result and the corpus version.
func TestDescribeRefusesAnAnalyzerWithNoConformancePass(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		member string
		named  []string
	}{
		{
			name:   "failing",
			member: `"conformance": {"corpus_version": "1.9.0", "result": "fail", "digest": "` + digest + `"}`,
			named:  []string{"deadset-go", `"fail"`, "1.9.0"},
		},
		{name: "absent", member: "", named: []string{"deadset-go", "no conformance record"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			req := request(t, "deadset-go", describer(t, 0, document(replaced(describedMembers(), memberConformance, c.member))).command)
			got, err := invoke.Describe(t.Context(), &req, kept(t, req.Analyzer), accepted)
			if got != nil {
				t.Errorf("Describe(a %s conformance record) = %+v, want no description", c.name, got)
			}
			handshakeRefusal(t, err, invoke.ErrNoConformancePass)
			for _, named := range c.named {
				if !strings.Contains(err.Error(), named) {
					t.Errorf("Describe(a %s conformance record) = %q, want the message to name %q", c.name, err, named)
				}
			}
		})
	}
}

// TestDescribeRefusesASchemaVersionOutsideTheRange pins that an analyzer
// reading no version the run accepts ends the run with the failure code,
// naming the entry, the versions the analyzer reads and the range the run
// accepts.
func TestDescribeRefusesASchemaVersionOutsideTheRange(t *testing.T) {
	t.Parallel()

	printed := document(replaced(describedMembers(), memberSchemaVersions, `"schema_versions_accepted": ["5.0.0", "6.1.0"]`))
	req := request(t, "deadset-go", describer(t, 0, printed).command)
	got, err := invoke.Describe(t.Context(), &req, kept(t, req.Analyzer), accepted)
	if got != nil {
		t.Errorf("Describe(an analyzer reading 5.0.0 and 6.1.0) = %+v, want no description", got)
	}
	handshakeRefusal(t, err, invoke.ErrSchemaVersion)
	for _, named := range []string{"deadset-go", "5.0.0, 6.1.0", report.SchemaVersion} {
		if !strings.Contains(err.Error(), named) {
			t.Errorf("Describe(an analyzer reading 5.0.0 and 6.1.0) = %q, want the message to name %q", err, named)
		}
	}
}

// TestDescribeRefusesADescribeThatDoesNotExitClean pins that a describe verb
// exiting other than 0 ends the run with the failure code, its own usage code
// included, whatever it printed, and that what it printed is still kept.
func TestDescribeRefusesADescribeThatDoesNotExitClean(t *testing.T) {
	t.Parallel()

	printed := document(describedMembers())
	for _, exit := range []int{1, 2, 3} {
		t.Run(strconv.Itoa(exit), func(t *testing.T) {
			t.Parallel()

			req := request(t, "deadset-go", describer(t, exit, printed).command)
			entry := kept(t, req.Analyzer)
			got, err := invoke.Describe(t.Context(), &req, entry, accepted)
			if got != nil {
				t.Errorf("Describe(a describe exiting %d) = %+v, want no description", exit, got)
			}
			if refused := handshakeRefusal(t, err, invoke.ErrDescribeExited); refused.Exit != exit {
				t.Errorf("Describe(a describe exiting %d) refused with exit %d, want %d", exit, refused.Exit, exit)
			}
			if keptBytes, _ := os.ReadFile(entry.Describe()); !bytes.Equal(keptBytes, printed) {
				t.Errorf("Describe(a describe exiting %d) kept %q, want the bytes it printed", exit, keptBytes)
			}
		})
	}
}

// TestDescribeRefusesADocumentItCannotRead pins the closed-key read of a
// describe document: each refusal names the JSON Pointer of the value at
// fault and ends the run with the failure code.
func TestDescribeRefusesADocumentItCannotRead(t *testing.T) {
	t.Parallel()

	valid := describedMembers()
	conformance := func(members string) []string {
		return replaced(valid, memberConformance, `"conformance": {`+members+`}`)
	}
	cases := []struct {
		name    string
		pointer string
		reason  string
		printed []byte
	}{
		{name: "undeclared", printed: document(append(slices.Clone(valid), `"schema_version": "6.0.0"`)), pointer: "/schema_version", reason: "declares no such member"},
		{name: "case-variant", printed: document(replaced(valid, memberName, `"Name": "deadset-go"`)), pointer: "/Name", reason: "declares no such member"},
		{name: "named-twice", printed: document(append(slices.Clone(valid), `"version": "1.21.0"`)), pointer: "/version", reason: "named twice"},
		{name: "required-absent", printed: document(replaced(valid, memberContractVersion, "")), pointer: "/contract_version", reason: "required member is absent"},
		{name: "null-string", printed: document(replaced(valid, memberName, `"name": null`)), pointer: "/name", reason: "want a string"},
		{name: "null-array", printed: document(replaced(valid, memberLanguages, `"languages": null`)), pointer: "/languages", reason: "want an array"},
		{name: "null-object", printed: document(replaced(valid, memberConformance, `"conformance": null`)), pointer: "/conformance", reason: "want an object"},
		{name: "null-element", printed: document(replaced(valid, memberSchemaVersions, `"schema_versions_accepted": ["6.0.0", null]`)), pointer: "/schema_versions_accepted/1", reason: "want a string"},
		{name: "not-a-string", printed: document(replaced(valid, memberVersion, `"version": 1`)), pointer: "/version", reason: "want a string"},
		{name: "not-an-array", printed: document(replaced(valid, memberLanguages, `"languages": "go"`)), pointer: "/languages", reason: "want an array"},
		{name: "empty-string", printed: document(replaced(valid, memberName, `"name": ""`)), pointer: "/name", reason: "want a non-empty string"},
		{name: "empty-array", printed: document(replaced(valid, memberLanguages, `"languages": []`)), pointer: "/languages", reason: "want at least one element"},
		{
			name:    "conformance-undeclared",
			printed: document(conformance(`"corpus_version": "1.9.0", "result": "pass", "digest": "` + digest + `", "fixtures": 21`)),
			pointer: "/conformance/fixtures",
			reason:  "declares no such member",
		},
		{
			name:    "conformance-digest-absent",
			printed: document(conformance(`"corpus_version": "1.9.0", "result": "pass"`)),
			pointer: "/conformance/digest",
			reason:  "required member is absent",
		},
		{name: "not-an-object", printed: []byte(`["deadset-go"]`), reason: "want an object"},
		{name: "trailing", printed: append(document(valid), "{}\n"...), reason: "nothing after it"},
		{name: "truncated", printed: document(valid)[:40], reason: "describe printed no document"},
		{name: "empty", printed: nil, reason: "want an object"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			req := request(t, "deadset-go", describer(t, 0, c.printed).command)
			got, err := invoke.Describe(t.Context(), &req, kept(t, req.Analyzer), accepted)
			if got != nil {
				t.Errorf("Describe(a %s document) = %+v, want no description", c.name, got)
			}
			handshakeRefusal(t, err, invoke.ErrDescription)
			if c.pointer != "" && !strings.Contains(err.Error(), c.pointer+": ") {
				t.Errorf("Describe(a %s document) = %q, want the message to name the value at %s", c.name, err, c.pointer)
			}
			if !strings.Contains(err.Error(), c.reason) {
				t.Errorf("Describe(a %s document) = %q, want the message to say %q", c.name, err, c.reason)
			}
		})
	}
}

// The describe documents the Contract publishes read as it states: a document
// recording a conformance pass admitted member for member, one recording no
// conformance run refused as having no pass, and each refused document refused
// naming the value its schema refuses, or the member the closed key list does
// not declare.
func TestDescribeReadsThePublishedDescribeDocuments(t *testing.T) {
	t.Parallel()

	t.Run("conformance-recorded", func(t *testing.T) {
		t.Parallel()

		printed := publishedExample(t, "examples/describe/conformance-recorded.json")
		req := request(t, "deadset-go", describer(t, 0, printed).command)
		got, err := invoke.Describe(t.Context(), &req, kept(t, req.Analyzer), report.SchemaVersions)
		if err != nil {
			t.Fatalf("Describe(conformance-recorded.json) = %v, want the analyzer admitted", err)
		}
		want := &invoke.Description{
			Conformance: &report.Conformance{
				CorpusVersion: "3.0.0", Result: report.ResultPass,
				Digest: "sha256:cbf2fb667d665d638407a6248aadf286ce96fe7b1627aa081e856846871a6e66",
			},
			Name: "deadset-go", Version: "1.21.0", ContractVersion: "5.3.0",
			SchemaVersionsAccepted: []string{"7.0.0"}, Languages: []string{"go"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Describe(conformance-recorded.json) = %+v, want %+v", got, want)
		}
	})
	t.Run("conformance-not-recorded", func(t *testing.T) {
		t.Parallel()

		printed := publishedExample(t, "examples/describe/conformance-not-recorded.json")
		req := request(t, "deadset-ts", describer(t, 0, printed).command)
		_, err := invoke.Describe(t.Context(), &req, kept(t, req.Analyzer), report.SchemaVersions)
		handshakeRefusal(t, err, invoke.ErrNoConformancePass)
	})

	for _, row := range describeNegatives(t) {
		t.Run(strings.TrimSuffix(row.file, ".json"), func(t *testing.T) {
			t.Parallel()

			printed := publishedExample(t, "examples/negatives/"+row.file)
			req := request(t, "deadset-go", describer(t, 0, printed).command)
			_, err := invoke.Describe(t.Context(), &req, kept(t, req.Analyzer), report.SchemaVersions)
			handshakeRefusal(t, err, invoke.ErrDescription)
			if !strings.Contains(err.Error(), row.pointer+": ") {
				t.Errorf("Describe(%s) = %q, want the message to name the value at %s", row.file, err, row.pointer)
			}
		})
	}
}

// describeNegative is one refused describe document and the JSON Pointer its
// refusal names.
type describeNegative struct {
	file, pointer string
}

// describeNegatives is every refused describe document the Contract
// publishes. A document refused for a member the schema does not declare is
// refused naming that member.
func describeNegatives(t *testing.T) []describeNegative {
	t.Helper()

	var index struct {
		Negatives []struct {
			File         string `json:"file"`
			Schema       string `json:"schema"`
			Constraint   string `json:"constraint"`
			InstancePath string `json:"instance_path"`
		} `json:"negatives"`
	}
	if err := json.Unmarshal(publishedExample(t, "examples/negatives/index.json"), &index); err != nil {
		t.Fatalf("Setup: decode examples/negatives/index.json: %v", err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	body, err := spec.Contract.ReadFile("contract/describe.schema.json")
	if err != nil || json.Unmarshal(body, &schema) != nil {
		t.Fatalf("Setup: read contract/describe.schema.json: %v", err)
	}
	var held []describeNegative
	for _, row := range index.Negatives {
		if row.Schema != "contract/describe.schema.json" {
			continue
		}
		pointer := row.InstancePath
		if row.Constraint == "additionalProperties" {
			var members map[string]json.RawMessage
			if err := json.Unmarshal(publishedExample(t, "examples/negatives/"+row.File), &members); err != nil {
				t.Fatalf("Setup: decode %s: %v", row.File, err)
			}
			for name := range members {
				if _, declared := schema.Properties[name]; !declared {
					pointer += "/" + name
				}
			}
		}
		held = append(held, describeNegative{file: row.File, pointer: pointer})
	}
	if len(held) == 0 {
		t.Fatal("Setup: examples/negatives/index.json names no refused describe document")
	}
	return held
}

// publishedExample is one document of the Contract's examples.
func publishedExample(t *testing.T, name string) []byte {
	t.Helper()

	body, err := spec.Examples.ReadFile(name)
	if err != nil {
		t.Fatalf("Setup: read %s: %v", name, err)
	}
	return body
}
