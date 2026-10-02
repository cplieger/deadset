package config

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	spec "github.com/cplieger/deadset-spec/v4"
)

// schemaKey is one key of the Contract's configuration schema, with the members of
// its schema object this test reads.
type schemaKey struct {
	Properties       json.RawMessage            `json:"properties"`
	PatternProps     map[string]json.RawMessage `json:"patternProperties"`
	Default          json.RawMessage            `json:"default"`
	Owner            string                     `json:"x-owner"`
	Pattern          string                     `json:"pattern"`
	Setting          bool                       `json:"x-setting"`
	IgnoredByResolve bool                       `json:"x-ignored-by-resolution"`
}

// orderedMembers returns an object's member names in the order the document
// writes them, with each member's value.
func orderedMembers(t *testing.T, object json.RawMessage) ([]string, map[string]json.RawMessage) {
	t.Helper()

	values := make(map[string]json.RawMessage)
	if err := json.Unmarshal(object, &values); err != nil {
		t.Fatalf("Setup: decode a schema object: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(object))
	var names []string
	depth := 0
	for {
		token, err := dec.Token()
		if err != nil {
			break
		}
		switch token {
		case json.Delim('{'), json.Delim('['):
			depth++
			continue
		case json.Delim('}'), json.Delim(']'):
			depth--
			continue
		}
		if name, isName := token.(string); isName && depth == 1 {
			names = append(names, name)
			var skipped json.RawMessage
			if err := dec.Decode(&skipped); err != nil {
				t.Fatalf("Setup: read the schema member %q: %v", name, err)
			}
		}
	}
	return names, values
}

// schemaOwner is how the schema's x-owner spells a key's owner, for a key named
// name.
func schemaOwner(o owner, name string) string {
	switch o {
	case everyProduct:
		return "product"
	case everyAnalyzer:
		return "analyzer"
	case orchestrator:
		return "orchestrator"
	case languageAnalyzer:
		return name + "-analyzer"
	default:
		return "unknown"
	}
}

// configSchema reads the pinned Contract's configuration schema.
func configSchema(t *testing.T) schemaKey {
	t.Helper()

	data, err := spec.Contract.ReadFile("contract/config.schema.json")
	if err != nil {
		t.Fatalf("Setup: read contract/config.schema.json: %v", err)
	}
	var root schemaKey
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("Setup: decode contract/config.schema.json: %v", err)
	}
	return root
}

// The closed key list is the Contract's: the same keys in the same order, each of
// the same kind, owned by the same product and defaulting to the same value. A
// Contract release that adds, moves or redefaults a key fails here rather than
// being silently unimplemented.
func TestKeyList_matchesTheContractSchema(t *testing.T) {
	t.Parallel()

	const version = "3.2.0"
	keys := keyList(version)
	compareSection(t, &keys, configSchema(t), "", false)
}

// compareSection compares one section of the key list with the schema object
// declaring it. inLanguage marks a language's own section, whose keys pass through.
func compareSection(t *testing.T, n *node, declared schemaKey, at string, inLanguage bool) {
	t.Helper()

	names, properties := orderedMembers(t, declared.Properties)
	got := make([]string, len(n.children))
	for i := range n.children {
		got[i] = n.children[i].name
	}
	if !slices.Equal(got, names) {
		t.Fatalf("keyList section %q declares %v, want the schema's keys in its order %v", at, got, names)
	}
	for i := range n.children {
		key := &n.children[i]
		path := joinKey(at, key.name)
		var schema schemaKey
		if err := json.Unmarshal(properties[key.name], &schema); err != nil {
			t.Fatalf("Setup: decode the schema of %s: %v", path, err)
		}
		if want := schemaOwner(key.owner, key.name); schema.Owner != want && !inLanguage {
			t.Errorf("keyList %s is owned by %q, want the schema's %q", path, want, schema.Owner)
		}
		if want := schemaKind(&schema, inLanguage); key.kind != want {
			t.Errorf("keyList %s is of kind %d, want %d from its schema", path, key.kind, want)
		}
		if key.kind == section {
			compareSection(t, key, schema, path, inLanguage || key.owner == languageAnalyzer)
			continue
		}
		compareDefault(t, path, key.fallback, schema.Default)
	}
}

// schemaKind is the kind of key a schema object declares.
func schemaKind(schema *schemaKey, inLanguage bool) nodeKind {
	switch {
	case schema.IgnoredByResolve:
		return annotations
	case schema.PatternProps != nil:
		return codes
	case schema.Properties != nil && !schema.Setting:
		return section
	case inLanguage:
		return passThrough
	default:
		return setting
	}
}

// compareDefault compares a key's documented default with the schema's. A key the
// schema gives no default has none here, except contract_version, whose default is
// the version the product implements.
func compareDefault(t *testing.T, path string, fallback, declared json.RawMessage) {
	t.Helper()

	if declared == nil {
		if fallback != nil && path != "contract_version" {
			t.Errorf("keyList %s defaults to %s, want no default, as the schema declares none", path, fallback)
		}
		return
	}
	var got, want any
	if err := json.Unmarshal(fallback, &got); err != nil {
		t.Fatalf("keyList %s default %s does not decode: %v", path, fallback, err)
	}
	if err := json.Unmarshal(declared, &want); err != nil {
		t.Fatalf("Setup: decode the schema default of %s: %v", path, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("keyList %s defaults to %s, want the schema's %s", path, fallback, declared)
	}
}

// schemaAt returns the schema object of the key at a dotted path.
func schemaAt(t *testing.T, path string) (schemaKey, bool) {
	t.Helper()

	current := configSchema(t)
	for name := range strings.SplitSeq(path, ".") {
		_, properties := orderedMembers(t, current.Properties)
		value, declared := properties[name]
		if !declared {
			return schemaKey{}, false
		}
		current = schemaKey{}
		if err := json.Unmarshal(value, &current); err != nil {
			t.Fatalf("Setup: decode the schema of %s: %v", path, err)
		}
	}
	return current, true
}

// Every flag supplies a setting the schema declares, so a flag name mapped to a
// misspelled path cannot ship.
func TestSettingFlags_supplyDeclaredSettings(t *testing.T) {
	t.Parallel()

	for _, f := range settingFlags {
		schema, declared := schemaAt(t, f.path)
		if !declared {
			t.Errorf("flag --%s supplies %q, which the schema does not declare", f.name, f.path)
			continue
		}
		if schemaKind(&schema, false) != setting {
			t.Errorf("flag --%s supplies %q, which the schema declares as a section rather than a setting", f.name, f.path)
		}
	}
}

// The issue-kind vocabulary a severity key is checked against is the Contract's.
func TestIssueCodes_matchTheContract(t *testing.T) {
	t.Parallel()

	data, err := spec.Contract.ReadFile("contract/kinds.json")
	if err != nil {
		t.Fatalf("Setup: read contract/kinds.json: %v", err)
	}
	var vocabulary struct {
		Kinds []struct {
			Code  string `json:"code"`
			Fixed bool   `json:"fixed"`
		} `json:"kinds"`
	}
	if err := json.Unmarshal(data, &vocabulary); err != nil {
		t.Fatalf("Setup: decode contract/kinds.json: %v", err)
	}
	var live, fixed []string
	for _, kind := range vocabulary.Kinds {
		live = append(live, kind.Code)
		if kind.Fixed {
			fixed = append(fixed, kind.Code)
		}
	}
	if !slices.Equal(issueCodes, live) {
		t.Errorf("issueCodes = %v, want the Contract's codes %v", issueCodes, live)
	}
	if !slices.Equal(fixedCodes, fixed) {
		t.Errorf("fixedCodes = %v, want the codes whose severity the Contract fixes %v", fixedCodes, fixed)
	}
}

// schemaString returns the string the pinned schema holds at the end of steps, each a
// member name or an array index.
func schemaString(t *testing.T, steps ...any) string {
	t.Helper()

	data, err := spec.Contract.ReadFile("contract/config.schema.json")
	if err != nil {
		t.Fatalf("Setup: read contract/config.schema.json: %v", err)
	}
	var current any
	if err := json.Unmarshal(data, &current); err != nil {
		t.Fatalf("Setup: decode contract/config.schema.json: %v", err)
	}
	for _, step := range steps {
		switch at := step.(type) {
		case string:
			object, isObject := current.(map[string]any)
			if !isObject {
				t.Fatalf("Setup: the schema holds no object at %v", steps)
			}
			current = object[at]
		case int:
			array, isArray := current.([]any)
			if !isArray || at >= len(array) {
				t.Fatalf("Setup: the schema holds no array entry at %v", steps)
			}
			current = array[at]
		}
	}
	value, isString := current.(string)
	if !isString {
		t.Fatalf("Setup: the schema holds no string at %v", steps)
	}
	return value
}

// onlyPatternProperty returns the one pattern an open object of the schema admits
// its member names by.
func onlyPatternProperty(t *testing.T, name string) string {
	t.Helper()

	declared, _ := schemaAt(t, name)
	if len(declared.PatternProps) != 1 {
		t.Fatalf("Setup: the schema of %s admits %d member patterns, want one", name, len(declared.PatternProps))
	}
	for pattern := range declared.PatternProps {
		return pattern
	}
	return ""
}

// Each spelling the key list checks a string against agrees with the schema's
// pattern for that key on every sample, the schema's pattern compiled as written.
func TestPatterns_agreeWithTheSchema(t *testing.T) {
	t.Parallel()

	cases := []struct {
		ours    *regexp.Regexp
		name    string
		schema  string
		samples []string
	}{
		{
			name: "severity-key", ours: severityKey, schema: onlyPatternProperty(t, "severity"),
			samples: []string{"DS10", "DS1001", "DS1", "DS100", "DS10010", "ds10", "DS\u0661\u0660", "DS10\n", "XDS10"},
		},
		{
			name: "provenance-key", ours: provenanceKey, schema: onlyPatternProperty(t, "provenance"),
			samples: []string{"target.kind", "severity.DS1101", "Target.kind", "a..b", "analysis_x", "a.B9_", "", "a."},
		},
		{
			name: "provenance-value", ours: provenanceValue,
			schema:  schemaString(t, "properties", "provenance", "patternProperties", onlyPatternProperty(t, "provenance"), "pattern"),
			samples: []string{"default", "repository: deadset.json", "flag: --fail-on", "central:x", "defaults", "repository: ", "env: x", "central: a\rb", "flag: a\nb"},
		},
		{
			name: "analyzer-name", ours: analyzerName, schema: providerPattern(t, 0, "name"),
			samples: []string{"deadset-go", "deadset", "example2-go", "Deadset", "-go", "go-", "a--b", "9go", "a_b", "a-9"},
		},
		{
			name: "artifact-source", ours: artifactSource, schema: providerPattern(t, 1, "source"),
			samples: []string{"go:example.com/x/cmd/x", "npm:@example/x", "npm:", "pip:x", "go:a b", "go:a\tb", "go:a\u00a0b", "GO:x"},
		},
		{
			name: "artifact-version", ours: artifactVersion, schema: providerPattern(t, 1, "version"),
			samples: []string{"1.4.0", "1.4.0-rc.1", "1.4.0+build.5", "v1.4.0", "1.4", "1.4.0-", "1.4.0\n"},
		},
		{
			name: "artifact-digest", ours: artifactChecksum, schema: providerPattern(t, 1, "digest"),
			samples: []string{"sha256:" + strings.Repeat("4", 64), "sha256:" + strings.Repeat("A", 64), "sha256:" + strings.Repeat("4", 63), "sha512:" + strings.Repeat("4", 64)},
		},
		{
			name: "contract-version", ours: semanticVersion, schema: schemaString(t, "properties", "contract_version", "pattern"),
			samples: []string{"3.0.0", "3.0", "v3.0.0", "3.0.0-rc1", "10.20.30", "3.0.0\n", "\u0663.0.0"},
		},
		{
			name: "exemption-class", ours: exemptionClass,
			schema:  schemaString(t, "properties", "exemptions", "properties", "disabled", "items", "pattern"),
			samples: []string{"template-field", "Template", "a", "-a", "a-", "a_b", "9a", "a9-b"},
		},
		{
			name: "project-path", ours: projectPath,
			schema: schemaString(t, "properties", "analysis", "properties", "configurations", "items", "oneOf", 1,
				"properties", "project", "pattern"),
			samples: []string{
				"tsconfig.json", "packages/app/tsconfig.json", "./tsconfig.json", "../x", "a//b", "a/", "/a",
				".a", "..a", "a\\b", "a/./b", "a/../b", "...", "a\nb",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			schema, err := regexp.Compile(c.schema)
			if err != nil {
				t.Fatalf("Setup: compile the schema's %s pattern %q: %v", c.name, c.schema, err)
			}
			for _, sample := range c.samples {
				if got, want := c.ours.MatchString(sample), schema.MatchString(sample); got != want {
					t.Errorf("%s pattern matches %q = %t, want the schema's %t", c.name, sample, got, want)
				}
			}
		})
	}
}

// providerPattern returns the pattern the schema declares for member of the provider
// entry shape at index of the entry's oneOf.
func providerPattern(t *testing.T, shape int, member string) string {
	t.Helper()

	return schemaString(t, "properties", "providers", "properties", "analyzers", "items", "oneOf", shape,
		"properties", member, "pattern")
}
