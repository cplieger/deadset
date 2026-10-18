package config_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
)

// A flag outranks the repository configuration, which outranks the central
// configuration, which outranks the default, and the provenance names the winner.
func TestResolve_ranksFlagThenRepositoryThenCentralThenDefault(t *testing.T) {
	t.Parallel()

	flags := map[vocab.Key]json.RawMessage{vocab.KeyReportersFailOn: json.RawMessage(`"allow"`)}
	repository := config.Document{Path: "repo/deadset.json", Data: []byte(`{"target": {"kind": "library"}, "reporters": {"fail_on": "warn"}}`)}
	central := config.Document{Path: "central.json", Data: []byte(`{"target": {"kind": "library"}, "reporters": {"fail_on": "deny"}}`)}
	bare := config.Document{Path: "repo/deadset.json", Data: []byte(`{"target": {"kind": "library"}}`)}
	cases := []struct {
		in         config.Inputs
		name       string
		provenance string
		want       vocab.Severity
	}{
		{
			name: "flag", want: vocab.Allow, provenance: "flag: --fail-on",
			in: config.Inputs{Flags: flags, Repository: repository, Central: central},
		},
		{
			name: "repository", want: vocab.Warn, provenance: "repository: repo/deadset.json",
			in: config.Inputs{Repository: repository, Central: central},
		},
		{
			name: "central", want: vocab.Deny, provenance: "central: central.json",
			in: config.Inputs{Repository: bare, Central: central},
		},
		{
			name: "default", want: vocab.Deny, provenance: "default",
			in: config.Inputs{Repository: bare},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			in := c.in
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
		Repository: config.Document{Path: "deadset.json", Data: []byte(`{"target": {"kind": "library"}, "severity": {"DS1501": "allow"}}`)},
		Central:    config.Document{Path: "central.json", Data: []byte(`{"severity": {"DS1501": "deny", "DS14": "warn"}}`)},
	})
	if err != nil {
		t.Fatalf("Resolve(per-code severity) = error %v", err)
	}
	got := printed(t, r)
	if want := map[string]any{"DS1501": "allow", "DS14": "warn"}; !reflect.DeepEqual(got.Values["severity"], want) {
		t.Errorf("Resolve(per-code severity) severity = %v, want %v", got.Values["severity"], want)
	}
	if got.Provenance["severity.DS1501"] != "repository: deadset.json" || got.Provenance["severity.DS14"] != "central: central.json" {
		t.Errorf("Resolve(per-code severity) provenance = %v, want DS1501 from the repository and DS14 from the central configuration",
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
// twice, is refused naming the key, so a typo is never a setting silently ignored;
// a key written twice is named as the strict reader names it, by itself.
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
		{name: "empty-member-of-a-section", document: `{"target": {"kind": "library", "": 1}}`, at: `key "target." is not implemented`},
		{name: "empty-severity-key", document: `{"severity": {"": "warn"}}`, at: `key "severity." is not implemented`},
		{name: "duplicated-section", document: `{"target": {"kind": "library"}, "target": {"kind": "application"}}`, at: "target"},
		{name: "duplicated-nested-key", document: `{"target": {"kind": "library", "kind": "application"}}`, at: `"kind"`},
		{name: "duplicated-member-of-an-entry", document: `{"analysis": {"configurations": [{"id": "a", "os": "linux", "arch": "amd64", "os": "darwin"}]}}`, at: `"os"`},
		{name: "duplicated-severity-code", document: `{"severity": {"DS1501": "warn", "DS1501": "deny"}}`, at: `"DS1501"`},
		{name: "severity-key-spelling", document: `{"severity": {"DS1": "warn"}}`, at: "severity.DS1"},
		{name: "severity-code-the-contract-fixes", document: `{"severity": {"DS9003": "warn"}}`, at: "severity.DS9003"},
		{name: "severity-family-holding-a-fixed-code", document: `{"severity": {"DS90": "allow"}}`, at: "severity.DS90"},
		{name: "severity-code-of-no-kind", document: `{"severity": {"DS2999": "warn"}}`, at: "severity.DS2999"},
		{name: "severity-family-of-no-kind", document: `{"severity": {"DS29": "warn"}}`, at: "severity.DS29"},
		{name: "severity-code-past-a-live-familys-kinds", document: `{"severity": {"DS1806": "warn"}}`, at: "severity.DS1806"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Resolve(fromRepository(c.document))
			refusal, isRefusal := errors.AsType[*config.Error](err)
			if !isRefusal {
				t.Fatalf("Resolve(%s) = error %v, want a *config.Error naming %q", c.document, err, c.at)
			}
			if !strings.Contains(refusal.Message, c.at) {
				t.Errorf("Resolve(%s) = %q, want the message to name %q", c.document, refusal, c.at)
			}
		})
	}
}

// A value outside what its key declares is refused naming the place in the value:
// a build matrix entry is exactly one of its two shapes, the delimiter pair names
// both delimiters, every other value is of its declared type and set, and a key of
// a language's own section is checked as every other key is.
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
		{name: "list-entry-repeated", document: `{"roots": {"patterns": ["go://a#B", "go://a#B"]}}`, at: "roots.patterns"},
		{name: "exemption-class-spelling", document: `{"exemptions": {"disabled": ["Template_Field"]}}`, at: "exemptions.disabled[0]"},
		{name: "component-extension-repeated", document: `{"ts": {"component_extensions": [".vue", ".vue"]}}`, at: "ts.component_extensions"},
		{name: "exemption-class-outside-the-vocabulary", document: `{"exemptions": {"disabled": ["no-such-class"]}}`, at: "exemptions.disabled[0]"},
		{name: "ts-entry-of-no-shape", document: `{"ts": {"serializers": [{"symbol": "go://a#B"}]}}`, at: "ts.serializers[0].symbol"},
		{name: "ts-list-shorter-than-its-minimum", document: `{"ts": {"test_files": [], "entry_files": [7]}}`, at: "ts.entry_files[0]"},
		{name: "integer-with-an-exponent", document: `{"reporters": {"max_findings": 1e2}}`, at: "reporters.max_findings"},
		{name: "provider-name-repeated", document: `{"providers": {"analyzers": [{"name": "a", "languages": ["go"], "command": "a"}, {"name": "a", "languages": ["ts"], "command": "b"}]}}`, at: "providers.analyzers[1].name"},
		{name: "roots-complete-of-another-type", document: `{"ts": {"roots_complete": "yes"}}`, at: "ts.roots_complete"},
		{name: "severity-outside-its-set", document: `{"severity": {"DS1501": "error"}}`, at: "severity.DS1501"},
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
			if !strings.Contains(refusal.Message, c.at) {
				t.Errorf("Resolve(%s) = %q, want the message to name %q", c.document, refusal, c.at)
			}
		})
	}
}

// A target kind written per language is refused naming its first fault: an
// undeclared member, then a language left out, then a language given a value
// that is not a kind, members in ascending order. Each message names the source
// and the path, and a refused value names the value without the kinds admitted.
func TestResolve_refusesATargetKindAtItsFirstFault(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		kind  string
		at    string
		value string
	}{
		{name: "undeclared-members-in-ascending-order", kind: `{"zz": "library", "py": "library", "go": "application"}`, at: "target.kind.py"},
		{name: "undeclared-after-a-refused-value", kind: `{"go": "service", "ts": "library", "py": "library"}`, at: "target.kind.py"},
		{name: "missing-after-a-refused-value", kind: `{"go": "service"}`, at: "target.kind.ts"},
		{name: "refused-values-in-ascending-order", kind: `{"ts": "service", "go": "service"}`, at: "target.kind.go", value: `"service"`},
		{name: "member-of-another-type", kind: `{"go": "application", "ts": 1}`, at: "target.kind.ts"},
		{name: "string-outside-its-set", kind: `"service"`, at: "target.kind", value: `"service"`},
		{name: "number", kind: `1`, at: "target.kind"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Resolve(fromRepository(`{"target": {"kind": ` + c.kind + `}}`))
			refusal, isRefusal := errors.AsType[*config.Error](err)
			if !isRefusal {
				t.Fatalf("Resolve(%s) = error %v, want a *config.Error naming %q", c.kind, err, c.at)
			}
			if named := strings.HasPrefix(refusal.Message, "deadset.json: "+c.at+": ") || strings.Contains(refusal.Message, `"`+c.at+`"`); !named {
				t.Errorf("Resolve(%s) = %q, want the message to name deadset.json and %q", c.kind, refusal, c.at)
			}
			if c.value != "" && (!strings.Contains(refusal.Message, c.value) || strings.Contains(refusal.Message, "library")) {
				t.Errorf("Resolve(%s) = %q, want the message to name %s and no admitted kind", c.kind, refusal, c.value)
			}
		})
	}
}

// Either shape of the target kind is one setting: the value of the
// highest-ranked source naming it stands whole, as that source wrote it, under
// one provenance entry, and every analyzer receives it so.
func TestResolve_readsTheTargetKindInEitherShapeAsOneSetting(t *testing.T) {
	t.Parallel()

	r, err := config.Resolve(&config.Inputs{
		Repository: config.Document{Path: "deadset.json", Data: []byte(`{"target": {"kind": {"ts": "library", "go": "application"}}}`)},
		Central:    config.Document{Path: "central.json", Data: []byte(`{"target": {"kind": "library"}}`)},
	})
	if err != nil {
		t.Fatalf("Resolve(a per-language kind over a string) = error %v", err)
	}
	want := map[string]any{"kind": map[string]any{"ts": "library", "go": "application"}}
	held := printed(t, r)
	if got := held.Values["target"]; !reflect.DeepEqual(got, want) {
		t.Errorf("Print() target = %v, want %v", got, want)
	}
	if got := held.Provenance["target.kind"]; got != "repository: deadset.json" {
		t.Errorf("Print() provenance of target.kind = %q, want the repository", got)
	}
	for name := range held.Provenance {
		if strings.HasPrefix(name, "target.kind.") {
			t.Errorf("Print() carries the provenance entry %q, want one for target.kind alone", name)
		}
	}
	for _, languages := range [][]vocab.Language{{vocab.Go}, {vocab.TS}, {vocab.Go, vocab.TS}} {
		document, err := r.Split(languages...)
		if err != nil {
			t.Fatalf("Split(%q) = error %v", languages, err)
		}
		if got := decodePrinted(t, document).Values["target"]; !reflect.DeepEqual(got, want) {
			t.Errorf("Split(%q) target = %v, want the whole value %v", languages, got, want)
		}
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
		{name: "no-source", in: &config.Inputs{Repository: config.Document{Path: "deadset.json"}}},
		{name: "language-in-scope", in: fromRepository(`{"analysis": {"languages": ["go"]}}`)},
		{name: "both-documents-silent", in: &config.Inputs{
			Flags:      map[vocab.Key]json.RawMessage{vocab.KeyAnalysisLanguages: json.RawMessage(`["go", "ts"]`)},
			Repository: config.Document{Path: "deadset.json", Data: []byte(`{"analysis": {"min_confidence": "certain"}}`)},
			Central:    config.Document{Path: "central.json", Data: []byte(`{"reporters": {"fail_on": "warn"}}`)},
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
			if !strings.Contains(refusal.Message, "target.kind") {
				t.Errorf("Resolve(%s) = %q, want the message to name target.kind", c.name, refusal)
			}
		})
	}

	r, err := config.Resolve(&config.Inputs{
		Repository: config.Document{Path: "deadset.json"},
		Central:    config.Document{Path: "central.json", Data: []byte(`{"target": {"kind": "application"}}`)},
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
		{name: "absent", document: `{"target": {"kind": "library"}}`, want: vocab.ContractVersion},
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
		Formats: []vocab.Format{vocab.FormatSARIF, vocab.FormatText}, BySize: true, MaxFindings: 25, FailOn: vocab.Warn,
	}
	if !reflect.DeepEqual(r.Config.Reporters, want) {
		t.Errorf("Resolve(orchestrator settings).Config.Reporters = %+v, want %+v", r.Config.Reporters, want)
	}
}

// A count is read up to the maximum the configuration schema declares, held as
// the largest int where that is smaller, and a count above it is refused naming
// the setting and the document, the repository's or the central one.
func TestResolve_readsACountUpToItsMaximum(t *testing.T) {
	t.Parallel()

	const maximum int64 = 9007199254740991
	most := fmt.Sprintf(`{"target": {"kind": "library"}, "reporters": {"max_findings": %d}}`, maximum)
	above := fmt.Sprintf(`{"target": {"kind": "library"}, "reporters": {"max_findings": %d}}`, maximum+1)
	held := int(min(maximum, int64(math.MaxInt)))
	cases := []struct {
		in      *config.Inputs
		name    string
		refuses string
	}{
		{name: "repository-at-the-maximum", in: fromRepository(most)},
		{name: "central-at-the-maximum", in: &config.Inputs{Central: config.Document{Path: "central.json", Data: []byte(most)}}},
		{name: "repository-above-it", in: fromRepository(above), refuses: "deadset.json"},
		{name: "central-above-it", in: &config.Inputs{Central: config.Document{Path: "central.json", Data: []byte(above)}}, refuses: "central.json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			r, err := config.Resolve(c.in)
			if c.refuses == "" {
				if err != nil || r.Config.Reporters.MaxFindings != held {
					t.Fatalf("Resolve(%s) = %v, error %v, want max_findings %d", c.name, r, err, held)
				}
				return
			}
			refusal, isRefusal := errors.AsType[*config.Error](err)
			if !isRefusal || !strings.Contains(refusal.Message, c.refuses) || !strings.Contains(refusal.Message, "reporters.max_findings") {
				t.Errorf("Resolve(%s) = error %v, want a *config.Error naming %s and reporters.max_findings", c.name, err, c.refuses)
			}
		})
	}
}

// A setting no option supplies is refused when the command line's settings
// carry it.
func TestResolve_refusesAFlagSettingNoOptionSupplies(t *testing.T) {
	t.Parallel()

	in := fromRepository(`{"target": {"kind": "library"}}`)
	in.Flags = map[vocab.Key]json.RawMessage{vocab.KeyAnalysisTemplateDirs: json.RawMessage(`[]`)}
	_, err := config.Resolve(in)
	refusal, isRefusal := errors.AsType[*config.Error](err)
	if !isRefusal || !strings.Contains(refusal.Message, "analysis.template_dirs") || !strings.Contains(refusal.Message, "no flag supplies it") {
		t.Errorf("Resolve(a flag setting of analysis.template_dirs) = error %v, want a *config.Error naming the key", err)
	}
}
