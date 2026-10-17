package config_test

import (
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

	spec "github.com/cplieger/deadset-spec/v8"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
)

// publishedCases are the configuration cases the pinned Contract publishes. The
// suite requires the directory to hold exactly these, so a case a Contract release
// adds fails here rather than going unrun.
var publishedCases = []string{
	"array-spanning-lines",
	"component-extension-without-a-full-stop",
	"component-extensions-configured",
	"duplicated-key",
	"integer-written-with-a-fraction",
	"member-written-as-null",
	"missing-target-kind",
	"provenance-on-input",
	"provider-name-duplicated",
	"quoted-key-with-a-dot",
	"resolved-configuration-round-trip",
	"severity-key-names-no-kind",
	"target-kind-empty-object",
	"target-kind-language-missing",
	"target-kind-language-undeclared",
	"target-kind-language-value-refused",
	"target-kind-member-null",
	"target-kind-missing-before-a-refused-value",
	"target-kind-object-over-a-string",
	"target-kind-of-another-type",
	"target-kind-per-language",
	"target-kind-replaced-whole",
	"target-kind-undeclared-before-missing",
	"template-delimiters-configured",
	"template-delimiters-half",
	"typescript-matrix-declared",
	"unimplemented-key",
}

// caseFlags reads the flags.json of a published case, by key, or none.
func caseFlags(t *testing.T, dir string) map[vocab.Key]json.RawMessage {
	t.Helper()

	data := caseFile(t, dir, "flags.json")
	if data == nil {
		return nil
	}
	var flags map[vocab.Key]json.RawMessage
	if err := json.Unmarshal(data, &flags); err != nil {
		t.Fatalf("Setup: decode %s/flags.json: %v", dir, err)
	}
	return flags
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

	for _, name := range publishedCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := path.Join("vectors/config", name)
			in := &config.Inputs{
				Repository: config.Document{Path: "repository.json", Data: caseFile(t, dir, "repository.json")},
				Central:    config.Document{Path: "central.json", Data: caseFile(t, dir, "central.json")},
				Flags:      caseFlags(t, dir),
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
			assertResolved(t, name, resolved, r)
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
	if want.ExitCode != vocab.ExitUsage {
		t.Errorf("case %s expects exit code %d, and every *config.Error exits with %d", name, want.ExitCode, vocab.ExitUsage)
	}
	if !strings.Contains(refusal.Error(), want.Names) {
		t.Errorf("Resolve(%s) = %q, want the message to name %q", name, refusal, want.Names)
	}
}

// assertResolved checks the printed configuration against the case's
// expected.json member by member, in order, and checks that the printed output
// read back as a repository configuration resolves to the same values.
func assertResolved(t *testing.T, name string, expected []byte, r *config.Resolved) {
	t.Helper()

	var printed bytes.Buffer
	if err := r.Print(&printed); err != nil {
		t.Fatalf("Print(%s) = error %v", name, err)
	}
	if got, want := compact(t, "the printed configuration", printed.Bytes()), compact(t, "expected.json", expected); got != want {
		t.Errorf("Resolve(%s) printed\n%s\nwant\n%s", name, printed.String(), expected)
	}

	back, err := config.Resolve(&config.Inputs{Repository: config.Document{Path: "printed.json", Data: printed.Bytes()}})
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
// a language's own section included, naming the place in the document the
// schema's own refusal names, or the member inside it the closed key list does
// not declare, which is refused by its own name.
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

	schema := configSchemaDocument(t)
	for _, row := range refused {
		t.Run(strings.TrimSuffix(row.File, ".json"), func(t *testing.T) {
			t.Parallel()

			document, err := spec.Examples.ReadFile("examples/negatives/" + row.File)
			if err != nil {
				t.Fatalf("Setup: read examples/negatives/%s: %v", row.File, err)
			}
			_, err = config.Resolve(&config.Inputs{Repository: config.Document{Path: row.File, Data: document}})
			refusal, isRefusal := errors.AsType[*config.Error](err)
			if !isRefusal {
				t.Fatalf("Resolve(%s) = error %v, want a *config.Error", row.File, err)
			}
			if want := refusedKey(t, schema, document, row.InstancePath); !strings.Contains(refusal.Error(), want) {
				t.Errorf("Resolve(%s) = %q, want the message to name %q, for the instance path %q the schema refuses",
					row.File, refusal, want, row.InstancePath)
			}
		})
	}
}

// configSchemaDocument is the pinned Contract's configuration schema, decoded.
func configSchemaDocument(t *testing.T) map[string]any {
	t.Helper()

	data, err := spec.Contract.ReadFile("contract/config.schema.json")
	if err != nil {
		t.Fatalf("Setup: read contract/config.schema.json: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("Setup: decode contract/config.schema.json: %v", err)
	}
	return schema
}

// refusedKey returns the key a refusal of one published refused document names: the
// value the schema refuses, or, where that value is an object carrying one member the
// schema declares nowhere at that place, the member.
func refusedKey(t *testing.T, schema map[string]any, document []byte, pointer string) string {
	t.Helper()

	var value any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatalf("Setup: decode the refused document: %v", err)
	}
	declarations := []map[string]any{schema}
	for segment := range strings.SplitSeq(strings.TrimPrefix(pointer, "/"), "/") {
		if segment == "" {
			continue
		}
		declarations = schemaStep(declarations, segment)
		value = documentStep(value, segment)
	}
	object, isObject := value.(map[string]any)
	if !isObject {
		return dottedPath(pointer)
	}
	declared := map[string]bool{}
	for _, declaration := range declarations {
		for _, shape := range shapesOf(declaration) {
			properties, _ := shape["properties"].(map[string]any)
			for name := range properties {
				declared[name] = true
			}
		}
	}
	var undeclared []string
	for name := range object {
		if !declared[name] {
			undeclared = append(undeclared, name)
		}
	}
	switch len(undeclared) {
	case 0:
		return dottedPath(pointer)
	case 1:
		return dottedPath(pointer + "/" + undeclared[0])
	default:
		t.Fatalf("Setup: the refused value at %q carries %d members its schema does not declare, %v, "+
			"want a document refused for one constraint", pointer, len(undeclared), undeclared)
		return ""
	}
}

// shapesOf returns one schema declaration together with the shapes its oneOf names,
// which between them declare the members a value at that place may carry.
func shapesOf(declaration map[string]any) []map[string]any {
	shapes := []map[string]any{declaration}
	alternatives, _ := declaration["oneOf"].([]any)
	for _, alternative := range alternatives {
		if shape, isObject := alternative.(map[string]any); isObject {
			shapes = append(shapes, shape)
		}
	}
	return shapes
}

// schemaStep returns the declarations of the value one JSON Pointer segment reaches
// from the values the given declarations describe: an array's items for an index,
// and the member a properties object declares for a name.
func schemaStep(declarations []map[string]any, segment string) []map[string]any {
	var next []map[string]any
	for _, declaration := range declarations {
		for _, shape := range shapesOf(declaration) {
			if strings.Trim(segment, "0123456789") == "" {
				if items, isObject := shape["items"].(map[string]any); isObject {
					next = append(next, items)
				}
				continue
			}
			properties, _ := shape["properties"].(map[string]any)
			if member, isObject := properties[segment].(map[string]any); isObject {
				next = append(next, member)
			}
		}
	}
	return next
}

// documentStep returns the value one JSON Pointer segment names inside a decoded
// value, or nil where it names none.
func documentStep(value any, segment string) any {
	switch held := value.(type) {
	case map[string]any:
		return held[segment]
	case []any:
		index, err := strconv.Atoi(segment)
		if err != nil || index < 0 || index >= len(held) {
			return nil
		}
		return held[index]
	default:
		return nil
	}
}
