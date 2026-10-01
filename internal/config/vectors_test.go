package config_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	spec "github.com/cplieger/deadset-spec/v3"
	"github.com/cplieger/deadset/internal/config"
)

// usageExitCode is the exit code every refusal this package makes maps to.
const usageExitCode = 2

// publishedCases are the configuration cases the pinned Contract publishes. The
// suite requires the directory to hold exactly these, so a case a Contract release
// adds fails here rather than going unrun.
var publishedCases = []string{
	"array-spanning-lines",
	"duplicated-key",
	"missing-target-kind",
	"provenance-on-input",
	"quoted-key-with-a-dot",
	"resolved-configuration-round-trip",
	"template-delimiters-configured",
	"template-delimiters-half",
	"typescript-matrix-declared",
	"unimplemented-key",
}

// contractVersion is the version of the pinned Contract, which the published cases
// name as the version the product implements.
func contractVersion(t *testing.T) string {
	t.Helper()

	data, err := spec.Contract.ReadFile("contract/contract.json")
	if err != nil {
		t.Fatalf("Setup: read contract/contract.json: %v", err)
	}
	var contract struct {
		ContractVersion string `json:"contract_version"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatalf("Setup: decode contract/contract.json: %v", err)
	}
	return contract.ContractVersion
}

// caseFile reads one file of a published case, or nil when the case has none.
func caseFile(t *testing.T, dir, name string) []byte {
	t.Helper()

	data, err := spec.Vectors.ReadFile(path.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("Setup: read %s: %v", path.Join(dir, name), err)
	}
	return data
}

// compact returns a JSON document with its insignificant space removed, so two
// documents compare equal exactly when they hold the same members in the same
// order with the same values spelled the same way.
func compact(t *testing.T, what string, document []byte) string {
	t.Helper()

	var b bytes.Buffer
	if err := json.Compact(&b, document); err != nil {
		t.Fatalf("compact %s: %v\n%s", what, err, document)
	}
	return b.String()
}

func TestPublishedConfigurationVectors(t *testing.T) {
	t.Parallel()

	entries, err := fs.ReadDir(spec.Vectors, "vectors/config")
	if err != nil {
		t.Fatalf("Setup: read vectors/config: %v", err)
	}
	var found []string
	for _, entry := range entries {
		if entry.IsDir() {
			found = append(found, entry.Name())
		}
	}
	if !slices.Equal(found, publishedCases) {
		t.Fatalf("vectors/config holds %v, want the cases this suite runs %v", found, publishedCases)
	}

	version := contractVersion(t)
	for _, name := range publishedCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := path.Join("vectors/config", name)
			in := &config.Inputs{
				ContractVersion: version,
				Repository:      config.Document{Path: "repository.json", Data: caseFile(t, dir, "repository.json")},
				Central:         config.Document{Path: "central.json", Data: caseFile(t, dir, "central.json")},
				Flags:           caseFile(t, dir, "flags.json"),
			}
			resolved, refused := caseFile(t, dir, "expected.json"), caseFile(t, dir, "expected-error.json")
			if (resolved == nil) == (refused == nil) {
				t.Fatalf("case %s carries expected.json %t and expected-error.json %t, want exactly one",
					name, resolved != nil, refused != nil)
			}

			r, err := config.Resolve(in)
			if refused != nil {
				assertRefused(t, name, refused, err)
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%s) = error %v, want the resolved configuration", name, err)
			}
			assertResolved(t, name, resolved, r, version)
		})
	}
}

// assertRefused checks a refusal against the case's expected-error.json: the exit
// code it maps to and the key or field its message names.
func assertRefused(t *testing.T, name string, expected []byte, err error) {
	t.Helper()

	var want struct {
		Names    string `json:"names"`
		ExitCode int    `json:"exit_code"`
	}
	if decodeErr := json.Unmarshal(expected, &want); decodeErr != nil {
		t.Fatalf("Setup: decode %s/expected-error.json: %v", name, decodeErr)
	}
	refusal, isRefusal := errors.AsType[*config.Error](err)
	if !isRefusal {
		t.Fatalf("Resolve(%s) = error %v, want a *config.Error naming %q", name, err, want.Names)
	}
	if want.ExitCode != usageExitCode {
		t.Errorf("case %s expects exit code %d, and every *config.Error exits with %d", name, want.ExitCode, usageExitCode)
	}
	if refusal.Key != want.Names {
		t.Errorf("Resolve(%s) refused naming %q (%s), want %q", name, refusal.Key, refusal, want.Names)
	}
	if !strings.Contains(refusal.Error(), want.Names) {
		t.Errorf("Resolve(%s) = %q, want the message to name %q", name, refusal, want.Names)
	}
}

// assertResolved checks the printed configuration against the case's
// expected.json member by member, in order, and checks that the printed output
// read back as a repository configuration resolves to the same values.
func assertResolved(t *testing.T, name string, expected []byte, r *config.Resolved, version string) {
	t.Helper()

	var printed bytes.Buffer
	if err := r.Print(&printed); err != nil {
		t.Fatalf("Print(%s) = error %v", name, err)
	}
	if got, want := compact(t, "the printed configuration", printed.Bytes()), compact(t, "expected.json", expected); got != want {
		t.Errorf("Resolve(%s) printed\n%s\nwant\n%s", name, printed.String(), expected)
	}

	back, err := config.Resolve(&config.Inputs{
		ContractVersion: version,
		Repository:      config.Document{Path: "printed.json", Data: printed.Bytes()},
	})
	if err != nil {
		t.Fatalf("Resolve(the printed configuration of %s) = error %v, want it to read back", name, err)
	}
	var reprinted bytes.Buffer
	if err := back.Print(&reprinted); err != nil {
		t.Fatalf("Print(the printed configuration of %s read back) = error %v", name, err)
	}
	if got, want := withoutProvenance(t, reprinted.Bytes()), withoutProvenance(t, printed.Bytes()); !reflect.DeepEqual(got, want) {
		t.Errorf("the printed configuration of %s read back resolves to\n%s\nwant the values it was printed with\n%s",
			name, reprinted.String(), printed.String())
	}
}

// withoutProvenance decodes a printed configuration and drops its provenance
// object, which names the sources of one resolution rather than its values.
func withoutProvenance(t *testing.T, printed []byte) map[string]any {
	t.Helper()

	var document map[string]any
	if err := json.Unmarshal(printed, &document); err != nil {
		t.Fatalf("decode a printed configuration: %v\n%s", err, printed)
	}
	delete(document, "provenance")
	return document
}

// refusedDocument is one row of the Contract's index of refused documents: the
// file, the schema that refuses it and the instance path the refusal names.
type refusedDocument struct {
	File         string `json:"file"`
	Schema       string `json:"schema"`
	InstancePath string `json:"instance_path"`
}

// dottedPath spells a JSON Pointer the way a refusal names a key: its member names
// joined by dots, and an array index in brackets after the array it indexes.
func dottedPath(pointer string) string {
	var spelled strings.Builder
	for segment := range strings.SplitSeq(strings.TrimPrefix(pointer, "/"), "/") {
		if segment != "" && strings.Trim(segment, "0123456789") == "" {
			spelled.WriteString("[" + segment + "]")
			continue
		}
		if spelled.Len() > 0 {
			spelled.WriteString(".")
		}
		spelled.WriteString(segment)
	}
	return spelled.String()
}

// Every configuration document the Contract publishes as refused is refused here,
// naming the place in the document the schema's own refusal names.
func TestPublishedRefusedConfigurations(t *testing.T) {
	t.Parallel()

	data, err := spec.Examples.ReadFile("examples/negatives/index.json")
	if err != nil {
		t.Fatalf("Setup: read examples/negatives/index.json: %v", err)
	}
	var index struct {
		Negatives []refusedDocument `json:"negatives"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatalf("Setup: decode examples/negatives/index.json: %v", err)
	}
	refused := slices.DeleteFunc(index.Negatives, func(row refusedDocument) bool {
		return row.Schema != "contract/config.schema.json"
	})
	if len(refused) == 0 {
		t.Fatal("examples/negatives/index.json names no refused configuration document, so this test pins nothing")
	}

	version := contractVersion(t)
	for _, row := range refused {
		t.Run(strings.TrimSuffix(row.File, ".json"), func(t *testing.T) {
			t.Parallel()

			document, err := spec.Examples.ReadFile("examples/negatives/" + row.File)
			if err != nil {
				t.Fatalf("Setup: read examples/negatives/%s: %v", row.File, err)
			}
			_, err = config.Resolve(&config.Inputs{
				ContractVersion: version,
				Repository:      config.Document{Path: row.File, Data: document},
			})
			refusal, isRefusal := errors.AsType[*config.Error](err)
			if !isRefusal {
				t.Fatalf("Resolve(%s) = error %v, want a *config.Error", row.File, err)
			}
			if want := dottedPath(row.InstancePath); refusal.Key != want {
				t.Errorf("Resolve(%s) refused naming %q (%s), want %q, the instance path %q the schema refuses",
					row.File, refusal.Key, refusal, want, row.InstancePath)
			}
		})
	}
}
