package config_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/deadset/internal/config"
)

// A flag outranks the repository configuration, which outranks the central
// configuration, which outranks the default, and the provenance names the winner.
func TestResolve_ranksFlagThenRepositoryThenCentralThenDefault(t *testing.T) {
	t.Parallel()

	flags := []byte(`{"reporters.fail_on": "allow"}`)
	repository := config.Document{Path: "repo/deadset.json", Data: []byte(`{"target": {"kind": "library"}, "reporters": {"fail_on": "warn"}}`)}
	central := config.Document{Path: "central.json", Data: []byte(`{"target": {"kind": "library"}, "reporters": {"fail_on": "deny"}}`)}
	bare := config.Document{Path: "repo/deadset.json", Data: []byte(`{"target": {"kind": "library"}}`)}
	cases := []struct {
		in         config.Inputs
		name       string
		provenance string
		want       config.Severity
	}{
		{
			name: "flag", want: config.Allow, provenance: "flag: --fail-on",
			in: config.Inputs{Flags: flags, Repository: repository, Central: central},
		},
		{
			name: "repository", want: config.Warn, provenance: "repository: repo/deadset.json",
			in: config.Inputs{Repository: repository, Central: central},
		},
		{
			name: "central", want: config.Deny, provenance: "central: central.json",
			in: config.Inputs{Repository: bare, Central: central},
		},
		{
			name: "default", want: config.Deny, provenance: "default",
			in: config.Inputs{Repository: bare},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			in := c.in
			in.ContractVersion = testContractVersion
			r, err := config.Resolve(&in)
			if err != nil {
				t.Fatalf("Resolve(%s) = error %v", c.name, err)
			}
			if r.Config.Reporters.FailOn != c.want {
				t.Errorf("Resolve(%s).Config.Reporters.FailOn = %q, want %q", c.name, r.Config.Reporters.FailOn, c.want)
			}
			if got := printed(t, r).Provenance["reporters.fail_on"]; got != c.provenance {
				t.Errorf("Resolve(%s) provenance of reporters.fail_on = %q, want %q", c.name, got, c.provenance)
			}
		})
	}
}

// The severity object resolves one code at a time, and an empty object is a value
// its source supplied: the provenance names that source for the object as a whole.
func TestResolve_severityResolvesPerCode(t *testing.T) {
	t.Parallel()

	r, err := config.Resolve(&config.Inputs{
		ContractVersion: testContractVersion,
		Repository:      config.Document{Path: "deadset.json", Data: []byte(`{"target": {"kind": "library"}, "severity": {"DS1101": "allow"}}`)},
		Central:         config.Document{Path: "central.json", Data: []byte(`{"severity": {"DS1101": "deny", "DS18": "warn"}}`)},
	})
	if err != nil {
		t.Fatalf("Resolve(per-code severity) = error %v", err)
	}
	got := printed(t, r)
	if want := map[string]any{"DS1101": "allow", "DS18": "warn"}; !reflect.DeepEqual(got.Values["severity"], want) {
		t.Errorf("Resolve(per-code severity) severity = %v, want %v", got.Values["severity"], want)
	}
	if got.Provenance["severity.DS1101"] != "repository: deadset.json" || got.Provenance["severity.DS18"] != "central: central.json" {
		t.Errorf("Resolve(per-code severity) provenance = %v, want DS1101 from the repository and DS18 from the central configuration",
			got.Provenance)
	}
	if _, held := got.Provenance["severity"]; held {
		t.Errorf("Resolve(per-code severity) provenance names the severity object as well as its codes: %v", got.Provenance)
	}

	empty, err := config.Resolve(fromRepository(`{"target": {"kind": "library"}, "severity": {}}`))
	if err != nil {
		t.Fatalf("Resolve(an empty severity object) = error %v", err)
	}
	if got := printed(t, empty).Provenance["severity"]; got != "repository: deadset.json" {
		t.Errorf("Resolve(an empty severity object) provenance of severity = %q, want the repository configuration", got)
	}
}

// A document naming a key the closed key list does not declare, or naming one key
// twice, is refused naming the key, so a typo is never a setting silently ignored.
func TestResolve_refusesAKeyOutsideTheKeyList(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		document string
		at       string
	}{
		{name: "nested-key", document: `{"reporters": {"fail_under": "warn"}}`, at: "reporters.fail_under"},
		{name: "top-level-key", document: `{"provider": {}}`, at: "provider"},
		{name: "setting-written-as-one-key", document: `{"analysis": {"matrix.complete": true}}`, at: "analysis.matrix.complete"},
		{name: "key-in-the-go-section", document: `{"go": {"test_files": []}}`, at: "go.test_files"},
		{name: "member-of-the-delimiter-pair", document: `{"analysis": {"template_delimiters": {"left": "<", "right": ">", "middle": "|"}}}`, at: "analysis.template_delimiters.middle"},
		{name: "member-of-a-matrix-entry", document: `{"analysis": {"configurations": [{"id": "a", "os": "linux", "arch": "amd64", "cgo": true}]}}`, at: "analysis.configurations[0].cgo"},
		{name: "duplicated-section", document: `{"target": {"kind": "library"}, "target": {"kind": "application"}}`, at: "target"},
		{name: "duplicated-nested-key", document: `{"target": {"kind": "library", "kind": "application"}}`, at: "target.kind"},
		{name: "duplicated-member-of-an-entry", document: `{"analysis": {"configurations": [{"id": "a", "os": "linux", "arch": "amd64", "os": "darwin"}]}}`, at: "analysis.configurations[0].os"},
		{name: "duplicated-severity-code", document: `{"severity": {"DS1101": "warn", "DS1101": "deny"}}`, at: "severity.DS1101"},
		{name: "severity-key-spelling", document: `{"severity": {"DS1": "warn"}}`, at: "severity.DS1"},
		{name: "severity-code-the-contract-fixes", document: `{"severity": {"DS1703": "warn"}}`, at: "severity.DS1703"},
		{name: "severity-family-holding-a-fixed-code", document: `{"severity": {"DS17": "allow"}}`, at: "severity.DS17"},
		{name: "severity-code-of-no-kind", document: `{"severity": {"DS2999": "warn"}}`, at: "severity.DS2999"},
		{name: "severity-family-of-no-kind", document: `{"severity": {"DS29": "warn"}}`, at: "severity.DS29"},
		{name: "severity-code-retired", document: `{"severity": {"DS1401": "warn"}}`, at: "severity.DS1401"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Resolve(fromRepository(c.document))
			refusal, isRefusal := errors.AsType[*config.Error](err)
			if !isRefusal {
				t.Fatalf("Resolve(%s) = error %v, want a *config.Error naming %q", c.document, err, c.at)
			}
			if refusal.Key != c.at || !strings.Contains(refusal.Message, c.at) {
				t.Errorf("Resolve(%s) refused naming %q (%s), want %q", c.document, refusal.Key, refusal, c.at)
			}
		})
	}
}

// A value outside what its key declares is refused naming the place in the value:
// a build matrix entry is exactly one of its two shapes, the delimiter pair names
// both delimiters, and every other value is of its declared type and set.
func TestResolve_refusesAValueOutsideItsDeclaration(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		document string
		at       string
	}{
		{name: "entry-of-both-shapes", document: `{"analysis": {"configurations": [{"id": "a", "os": "linux", "arch": "amd64", "project": "tsconfig.json"}]}}`, at: "analysis.configurations[0]"},
		{name: "entry-of-neither-shape", document: `{"analysis": {"configurations": [{"id": "a"}]}}`, at: "analysis.configurations[0]"},
		{name: "platform-without-its-architecture", document: `{"analysis": {"configurations": [{"id": "a", "os": "linux"}]}}`, at: "analysis.configurations[0].arch"},
		{name: "project-leaving-the-target-root", document: `{"analysis": {"configurations": [{"id": "a", "project": "../tsconfig.json"}]}}`, at: "analysis.configurations[0].project"},
		{name: "project-without-its-identifier", document: `{"analysis": {"configurations": [{"id": "a", "project": "tsconfig.json"}, {"project": "b/tsconfig.json"}]}}`, at: "analysis.configurations[1].id"},
		{name: "half-a-delimiter-pair", document: `{"analysis": {"template_delimiters": {"right": "]]"}}}`, at: "analysis.template_delimiters.left"},
		{name: "value-outside-its-set", document: `{"analysis": {"min_confidence": "likely"}}`, at: "analysis.min_confidence"},
		{name: "null-value", document: `{"analysis": {"template_dirs": null}}`, at: "analysis.template_dirs"},
		{name: "value-of-another-type", document: `{"consumers": {"complete": "yes"}}`, at: "consumers.complete"},
		{name: "count-below-its-minimum", document: `{"reporters": {"max_findings": -1}}`, at: "reporters.max_findings"},
		{name: "fractional-count", document: `{"reporters": {"max_findings": 1.5}}`, at: "reporters.max_findings"},
		{name: "list-shorter-than-its-minimum", document: `{"reporters": {"formats": []}}`, at: "reporters.formats"},
		{name: "list-entry-outside-its-set", document: `{"analysis": {"languages": ["go", "rust"]}}`, at: "analysis.languages[1]"},
		{name: "list-entry-repeated", document: `{"roots": {"patterns": ["go://a#B", "go://a#B"]}}`, at: "roots.patterns[1]"},
		{name: "exemption-class-spelling", document: `{"exemptions": {"disabled": ["Template_Field"]}}`, at: "exemptions.disabled[0]"},
		{name: "component-extension-repeated", document: `{"ts": {"component_extensions": [".vue", ".vue"]}}`, at: "ts.component_extensions[1]"},
		{name: "severity-outside-its-set", document: `{"severity": {"DS1101": "error"}}`, at: "severity.DS1101"},
		{name: "contract-version-spelling", document: `{"contract_version": "3.0"}`, at: "contract_version"},
		{name: "provider-member-undeclared", document: `{"providers": {"analyzers": [{"name": "a", "languages": ["go"], "command": "a", "argument": 0}]}}`, at: "providers.analyzers[0].argument"},
		{name: "provider-name-spelling", document: `{"providers": {"analyzers": [{"name": "Deadset_Go", "languages": ["go"], "command": "a"}]}}`, at: "providers.analyzers[0].name"},
		{name: "provider-claiming-no-language", document: `{"providers": {"analyzers": [{"name": "a", "languages": [], "command": "a"}]}}`, at: "providers.analyzers[0].languages"},
		{name: "provider-artifact-half-named", document: `{"providers": {"analyzers": [{"name": "a", "languages": ["go"], "command": "a", "source": "go:example.com/a", "version": "1.0.0"}]}}`, at: "providers.analyzers[0].digest"},
		{name: "provider-digest-spelling", document: `{"providers": {"analyzers": [{"name": "a", "languages": ["go"], "command": "a", "source": "go:example.com/a", "version": "1.0.0", "digest": "sha256:AB"}]}}`, at: "providers.analyzers[0].digest"},
		{name: "provenance-value-spelling", document: `{"provenance": {"target.kind": "the repository"}}`, at: "provenance.target.kind"},
		{name: "document-of-another-type", document: `[]`, at: ""},
		{name: "second-value", document: `{} {}`, at: ""},
		{name: "empty-document", document: ``, at: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Resolve(fromRepository(c.document))
			refusal, isRefusal := errors.AsType[*config.Error](err)
			if !isRefusal {
				t.Fatalf("Resolve(%s) = error %v, want a *config.Error naming %q", c.document, err, c.at)
			}
			if refusal.Key != c.at || !strings.Contains(refusal.Message, c.at) {
				t.Errorf("Resolve(%s) refused naming %q (%s), want %q", c.document, refusal.Key, refusal, c.at)
			}
		})
	}
}

// The target kind has no default and is never inferred, so a run whose sources
// supply none is refused naming the field, whatever languages are in scope, and a
// run whose central configuration alone supplies it resolves.
func TestResolve_requiresTheTargetKind(t *testing.T) {
	t.Parallel()

	refused := []struct {
		in   *config.Inputs
		name string
	}{
		{name: "no-source", in: &config.Inputs{ContractVersion: testContractVersion, Repository: config.Document{Path: "deadset.json"}}},
		{name: "language-in-scope", in: fromRepository(`{"analysis": {"languages": ["go"]}}`)},
		{name: "both-documents-silent", in: &config.Inputs{
			ContractVersion: testContractVersion,
			Flags:           []byte(`{"analysis.languages": ["go", "ts"]}`),
			Repository:      config.Document{Path: "deadset.json", Data: []byte(`{"analysis": {"min_confidence": "certain"}}`)},
			Central:         config.Document{Path: "central.json", Data: []byte(`{"reporters": {"fail_on": "warn"}}`)},
		}},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Resolve(c.in)
			refusal, isRefusal := errors.AsType[*config.Error](err)
			if !isRefusal {
				t.Fatalf("Resolve(%s) = error %v, want a *config.Error naming target.kind", c.name, err)
			}
			if refusal.Key != "target.kind" || !strings.Contains(refusal.Message, "target.kind") {
				t.Errorf("Resolve(%s) refused naming %q (%s), want target.kind", c.name, refusal.Key, refusal)
			}
		})
	}

	r, err := config.Resolve(&config.Inputs{
		ContractVersion: testContractVersion,
		Repository:      config.Document{Path: "deadset.json"},
		Central:         config.Document{Path: "central.json", Data: []byte(`{"target": {"kind": "application"}}`)},
	})
	if err != nil {
		t.Fatalf("Resolve(the central configuration alone supplying the target kind) = error %v, want it to resolve", err)
	}
	if got := printed(t, r).Provenance["target.kind"]; got != "central: central.json" {
		t.Errorf("Resolve(the central configuration alone) provenance of target.kind = %q, want the central configuration", got)
	}
}

// A configuration naming no Contract version is read as written against the one the
// product implements, and one naming a version keeps it.
func TestResolve_contractVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, document, want string
	}{
		{name: "absent", document: `{"target": {"kind": "library"}}`, want: testContractVersion},
		{name: "named", document: `{"contract_version": "2.4.0", "target": {"kind": "library"}}`, want: "2.4.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			r, err := config.Resolve(fromRepository(c.document))
			if err != nil {
				t.Fatalf("Resolve(%s) = error %v", c.document, err)
			}
			if got := printed(t, r).Values["contract_version"]; got != c.want {
				t.Errorf("Resolve(%s) contract_version = %v, want %q", c.document, got, c.want)
			}
		})
	}
}

// The value of a key of a language's own section that the key list gives no
// check passes through unread: the analyzer of that language reads and checks it.
func TestResolve_passesALanguageSectionThroughUnread(t *testing.T) {
	t.Parallel()

	r, err := config.Resolve(fromRepository(`{"target": {"kind": "library"}, "ts": {"test_files": [], "entry_files": [7]}}`))
	if err != nil {
		t.Fatalf("Resolve(a ts section its analyzer would refuse) = error %v, want the section passed through", err)
	}
	want := map[string]any{
		"test_files": []any{}, "entry_files": []any{float64(7)},
		"component_extensions": []any{".vue", ".svelte", ".astro"}, "disabled_conventions": []any{},
		"injection_registrations": []any{}, "lifecycle_contracts": []any{}, "serializers": []any{},
	}
	if got := printed(t, r).Values["ts"]; !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve(a ts section its analyzer would refuse) ts = %v, want it as written %v", got, want)
	}
}

// The settings the orchestrator reads come out of the resolution typed.
func TestResolve_readsTheOrchestratorSettings(t *testing.T) {
	t.Parallel()

	r, err := config.Resolve(fromRepository(`{
		"target": {"kind": "library"},
		"analysis": {"languages": ["ts", "go"]},
		"reporters": {"formats": ["sarif", "text"], "sort": "size", "cascade": "full", "max_findings": 25, "fail_on": "warn"}
	}`))
	if err != nil {
		t.Fatalf("Resolve(orchestrator settings) = error %v", err)
	}
	if want := []string{"ts", "go"}; !slices.Equal(r.Config.Languages, want) {
		t.Errorf("Resolve(orchestrator settings).Config.Languages = %v, want %v", r.Config.Languages, want)
	}
	want := config.Reporters{
		Formats: []config.Format{config.FormatSARIF, config.FormatText}, Sort: config.SortSize,
		Cascade: config.CascadeFull, MaxFindings: 25, FailOn: config.Warn,
	}
	if !reflect.DeepEqual(r.Config.Reporters, want) {
		t.Errorf("Resolve(orchestrator settings).Config.Reporters = %+v, want %+v", r.Config.Reporters, want)
	}
}
