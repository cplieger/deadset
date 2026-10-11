package rundir_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	spec "github.com/cplieger/deadset-spec/v7"
	"github.com/cplieger/deadset/internal/rundir"
)

// scopeSchema is the Contract's schema of the scope document.
const scopeSchema = "contract/scope.schema.json"

// schemaKeywords is every keyword the scope schema check reads. A schema node
// using another fails the check's setup, so a later schema cannot add a rule
// the check silently ignores.
var schemaKeywords = []string{
	"$schema", "title", "description", "type", "additionalProperties", "required", "properties", "minLength", "enum", "items",
}

// publishedScopeSchema is the scope schema of the pinned Contract.
func publishedScopeSchema(t *testing.T) map[string]any {
	t.Helper()

	var schema map[string]any
	if err := json.Unmarshal(readContract(t, spec.Contract, scopeSchema), &schema); err != nil {
		t.Fatalf("Setup: decode %s: %v", scopeSchema, err)
	}
	return schema
}

// readContract is one file of a tree the Contract publishes.
func readContract(t *testing.T, tree fs.FS, name string) []byte {
	t.Helper()

	body, err := fs.ReadFile(tree, name)
	if err != nil {
		t.Fatalf("Setup: read %s: %v", name, err)
	}
	return body
}

// fataler is what the schema check needs of a test, so a property can run it.
type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

// violations is every rule of the schema node that value breaks, each starting
// with the JSON Pointer of the value the rule refuses and a colon; a member an
// object lacks or must not carry is refused at the object.
func violations(t fataler, node map[string]any, value any, at string) []string {
	t.Helper()

	for keyword := range node {
		if !slices.Contains(schemaKeywords, keyword) {
			t.Fatalf("Setup: %s uses the keyword %q at %q, which this check does not read", scopeSchema, keyword, at)
		}
	}
	switch node["type"] {
	case "object":
		return objectViolations(t, node, value, at)
	case "array":
		elements, isArray := value.([]any)
		if !isArray {
			return []string{at + ": want an array"}
		}
		items, _ := node["items"].(map[string]any)
		var found []string
		for i, element := range elements {
			found = append(found, violations(t, items, element, fmt.Sprintf("%s/%d", at, i))...)
		}
		return found
	case "string":
		return stringViolations(node, value, at)
	default:
		t.Fatalf("Setup: %s declares the type %v at %q, which this check does not read", scopeSchema, node["type"], at)
		return nil
	}
}

// objectViolations is every rule of the object schema node that value breaks.
func objectViolations(t fataler, node map[string]any, value any, at string) []string {
	t.Helper()

	object, isObject := value.(map[string]any)
	if !isObject {
		return []string{at + ": want an object"}
	}
	var found []string
	required, _ := node["required"].([]any)
	for _, name := range required {
		if _, present := object[name.(string)]; !present {
			found = append(found, fmt.Sprintf("%s: the required member %s is absent", at, name))
		}
	}
	properties, _ := node["properties"].(map[string]any)
	for _, name := range slices.Sorted(maps.Keys(object)) {
		child, declared := properties[name].(map[string]any)
		switch {
		case declared:
			found = append(found, violations(t, child, object[name], at+"/"+name)...)
		case node["additionalProperties"] == false:
			found = append(found, fmt.Sprintf("%s: the schema declares no member %s", at, name))
		}
	}
	return found
}

// stringViolations is every rule of the string schema node that value breaks.
func stringViolations(node map[string]any, value any, at string) []string {
	text, isString := value.(string)
	if !isString {
		return []string{at + ": want a string"}
	}
	var found []string
	if least, bounded := node["minLength"].(float64); bounded && utf8.RuneCountInString(text) < int(least) {
		found = append(found, fmt.Sprintf("%s: %q is shorter than %v", at, text, least))
	}
	if enum, closed := node["enum"].([]any); closed && !slices.Contains(enum, any(text)) {
		found = append(found, fmt.Sprintf("%s: %q is not one of %v", at, text, enum))
	}
	return found
}

// decoded is the JSON document data holds, as encoding/json reads it into any.
func decoded(t fataler, name string, data []byte) any {
	t.Helper()

	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return document
}

// TestTheScopeSchemaCheckReadsThePublishedDocuments pins the check the writer's
// documents are held to against the Contract's own judgement: it admits every
// scope document the Contract publishes as an example and refuses every one it
// publishes as a negative, once, at the value the negatives index names.
func TestTheScopeSchemaCheckReadsThePublishedDocuments(t *testing.T) {
	t.Parallel()

	schema := publishedScopeSchema(t)
	examples, err := fs.Glob(spec.Examples, "examples/scope/*.json")
	if err != nil || len(examples) == 0 {
		t.Fatalf("Setup: list the scope examples: %v, %d found", err, len(examples))
	}
	for _, name := range examples {
		if found := violations(t, schema, decoded(t, name, readContract(t, spec.Examples, name)), ""); len(found) > 0 {
			t.Errorf("the check refuses the example %s: %q, want it admitted", name, found)
		}
	}

	var index struct {
		Negatives []struct {
			File         string `json:"file"`
			Schema       string `json:"schema"`
			InstancePath string `json:"instance_path"`
		} `json:"negatives"`
	}
	if err := json.Unmarshal(readContract(t, spec.Examples, "examples/negatives/index.json"), &index); err != nil {
		t.Fatalf("Setup: decode the negatives index: %v", err)
	}
	refused := 0
	for _, negative := range index.Negatives {
		if negative.Schema != scopeSchema {
			continue
		}
		refused++
		name := path.Join("examples/negatives", negative.File)
		found := violations(t, schema, decoded(t, name, readContract(t, spec.Examples, name)), "")
		if len(found) != 1 || !strings.HasPrefix(found[0], negative.InstancePath+": ") {
			t.Errorf("the check refuses the negative %s with %q, want one refusal at %q", name, found, negative.InstancePath)
		}
	}
	if refused == 0 {
		t.Errorf("the negatives index names no document of %s, want the check held to at least one", scopeSchema)
	}
}

// root is the working directory the scope tests name, an absolute path.
const root = "/src/run"

// TestWriteScopeWritesTheDocumentTheSchemaStates writes scopes of every shape
// and reads each back: the document validates against the published schema,
// names the target with the role target and each consumer with the role
// consumer in order, carries an id or a workspace only where the scope has
// one, and names every path as the absolute path inside the working directory.
func TestWriteScopeWritesTheDocumentTheSchemaStates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want  any
		scope rundir.Scope
		name  string
	}{
		{
			name:  "target-alone",
			scope: rundir.Scope{Root: root, Target: rundir.Module{Path: "."}},
			want:  map[string]any{"target": map[string]any{"role": "target", "path": root}},
		},
		{
			name: "target-and-consumers",
			scope: rundir.Scope{
				Root:      root,
				Workspace: "go.work",
				Target:    rundir.Module{ID: "example.com/app", Path: "app"},
				Consumers: []rundir.Module{{ID: "example.com/consumer", Path: "consumers/one"}, {Path: "tool/./cmd/../"}},
			},
			want: map[string]any{
				"target":    map[string]any{"id": "example.com/app", "role": "target", "path": root + "/app"},
				"workspace": root + "/go.work",
				"consumers": []any{
					map[string]any{"id": "example.com/consumer", "role": "consumer", "path": root + "/consumers/one"},
					map[string]any{"role": "consumer", "path": root + "/tool"},
				},
			},
		},
	}
	schema := publishedScopeSchema(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			dir, _ := created(t)
			if err := dir.WriteScope(&c.scope); err != nil {
				t.Fatalf("WriteScope(%+v) = %v, want it written", c.scope, err)
			}
			written, err := os.ReadFile(dir.Scope())
			if err != nil {
				t.Fatalf("read %s: %v", dir.Scope(), err)
			}
			got := decoded(t, dir.Scope(), written)
			if found := violations(t, schema, got, ""); len(found) > 0 {
				t.Errorf("WriteScope(%+v) wrote\n%s\nwhich %s refuses: %q", c.scope, written, scopeSchema, found)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("WriteScope(%+v) wrote %v, want %v", c.scope, got, c.want)
			}
		})
	}
}

// TestAnEntrysScopeIsTheDocumentTheRunsWouldBe pins that the scope document an
// entry reads on its own is the one the run's scope document would be for the
// same scope, byte for byte, written beside it under the entry's name.
func TestAnEntrysScopeIsTheDocumentTheRunsWouldBe(t *testing.T) {
	t.Parallel()

	scope := rundir.Scope{
		Root:      root,
		Workspace: "go.work",
		Target:    rundir.Module{ID: "example.com/app", Path: "app"},
		Consumers: []rundir.Module{{Path: "consumers/one"}},
	}
	dir, path := created(t)
	e, err := dir.Entry("deadset-go")
	if err != nil {
		t.Fatalf("Setup: Entry(deadset-go): %v", err)
	}
	if err := dir.WriteScope(&scope); err != nil {
		t.Fatalf("WriteScope(%+v) = %v", scope, err)
	}
	if err := e.WriteScope(&scope); err != nil {
		t.Fatalf("Entry(deadset-go).WriteScope(%+v) = %v", scope, err)
	}
	if want := path + "/scope.deadset-go.json"; e.Scope() != want {
		t.Errorf("Entry(deadset-go).Scope() = %q, want %q", e.Scope(), want)
	}
	run, err := os.ReadFile(dir.Scope())
	if err != nil {
		t.Fatalf("read %s: %v", dir.Scope(), err)
	}
	own, err := os.ReadFile(e.Scope())
	if err != nil {
		t.Fatalf("read %s: %v", e.Scope(), err)
	}
	if string(own) != string(run) {
		t.Errorf("Entry(deadset-go).WriteScope(%+v) wrote\n%s\nwant the run's document\n%s", scope, own, run)
	}
}

// TestWriteScopeRefusesAPathOutsideTheWorkingDirectory pins that a scope
// naming anything but a local path inside an absolute working directory is
// refused before any document is written.
func TestWriteScopeRefusesAPathOutsideTheWorkingDirectory(t *testing.T) {
	t.Parallel()

	target := rundir.Module{Path: "app"}
	cases := map[string]rundir.Scope{
		"relative-root":       {Root: "src/run", Target: target},
		"empty-root":          {Target: target},
		"empty-target":        {Root: root, Target: rundir.Module{ID: "example.com/app"}},
		"absolute-target":     {Root: root, Target: rundir.Module{Path: "/src/app"}},
		"climbing-target":     {Root: root, Target: rundir.Module{Path: "../app"}},
		"parent-target":       {Root: root, Target: rundir.Module{Path: ".."}},
		"leaving-workspace":   {Root: root, Target: target, Workspace: "app/../../go.work"},
		"absolute-consumer":   {Root: root, Target: target, Consumers: []rundir.Module{{Path: "consumer"}, {Path: "/src/tool"}}},
		"empty-consumer-path": {Root: root, Target: target, Consumers: []rundir.Module{{ID: "example.com/tool"}}},
	}
	for name, scope := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir, _ := created(t)
			if err := dir.WriteScope(&scope); !errors.Is(err, rundir.ErrScopePath) {
				t.Errorf("WriteScope(%+v) = %v, want an error satisfying errors.Is(err, ErrScopePath)", scope, err)
			}
			if _, err := os.Stat(dir.Scope()); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("after a refused WriteScope(%+v), Stat(%s) = %v, want no document", scope, dir.Scope(), err)
			}
		})
	}
}
