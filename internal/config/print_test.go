package config_test

import (
	"reflect"
	"testing"

	"github.com/cplieger/deadset/internal/config"
)

// mixedTarget is a target holding both languages, with a matrix for each, a
// setting only the orchestrator reads and a setting of each language's section.
const mixedTarget = `{
	"target": {"kind": "application"},
	"analysis": {
		"languages": ["go", "ts"],
		"configurations": [
			{"id": "linux-amd64", "os": "linux", "arch": "amd64"},
			{"id": "tsconfig.json", "project": "tsconfig.json"}
		]
	},
	"ts": {"entry_files": ["src/cli.ts"]}
}`

// Each analyzer is handed the keys every analyzer reads and its own language's
// section, never another language's section or a key the orchestrator alone reads,
// and the build matrix whole, both shapes, for the analysis to read its own.
func TestSplit_handsEachAnalyzerItsOwnSections(t *testing.T) {
	t.Parallel()

	r, err := config.Resolve(fromRepository(mixedTarget))
	if err != nil {
		t.Fatalf("Setup: Resolve(a mixed target) = error %v", err)
	}
	cases := []struct {
		name      string
		languages []string
		held      []string
		withheld  []string
	}{
		{name: "go", languages: []string{"go"}, held: []string{"go"}, withheld: []string{"ts"}},
		{name: "ts", languages: []string{"ts"}, held: []string{"ts"}, withheld: []string{"go"}},
		{name: "both", languages: []string{"go", "ts"}, held: []string{"go", "ts"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			document, err := r.Split(c.languages...)
			if err != nil {
				t.Fatalf("Split(%v) = error %v", c.languages, err)
			}
			got := decodePrinted(t, document)
			for _, section := range c.held {
				if _, held := got.Values[section]; !held {
					t.Errorf("Split(%v) holds no %s section, want it\n%s", c.languages, section, document)
				}
			}
			for _, section := range c.withheld {
				if _, held := got.Values[section]; held {
					t.Errorf("Split(%v) holds the %s section, want it withheld\n%s", c.languages, section, document)
				}
			}
			analysis, isObject := got.Values["analysis"].(map[string]any)
			if !isObject {
				t.Fatalf("Split(%v) holds no analysis section\n%s", c.languages, document)
			}
			if _, held := analysis["languages"]; held {
				t.Errorf("Split(%v) holds analysis.languages, which the orchestrator alone reads\n%s", c.languages, document)
			}
			if _, held := got.Provenance["analysis.languages"]; held {
				t.Errorf("Split(%v) names the source of analysis.languages, which it does not hold", c.languages)
			}
			if entries, _ := analysis["configurations"].([]any); len(entries) != 2 {
				t.Errorf("Split(%v) analysis.configurations = %v, want both entries of the matrix", c.languages, analysis["configurations"])
			}
			if got.Provenance["target.kind"] != "repository: deadset.json" {
				t.Errorf("Split(%v) provenance of target.kind = %q, want the repository configuration", c.languages, got.Provenance["target.kind"])
			}
		})
	}
}

// The orchestrator renders the formats and applies the maximum finding count to the
// merged report, so no analyzer is handed either, whatever the target configures,
// and every other reporter setting reaches each analyzer as resolved.
func TestSplit_withholdsTheReporterSettingsTheMergedReportTakes(t *testing.T) {
	t.Parallel()

	r, err := config.Resolve(fromRepository(`{
		"target": {"kind": "library"},
		"reporters": {"formats": ["sarif", "json"], "max_findings": 5, "sort": "size", "fail_on": "warn"}
	}`))
	if err != nil {
		t.Fatalf("Setup: Resolve(a target configuring its reporters) = error %v", err)
	}
	for _, languages := range [][]string{{"go"}, {"ts"}, {"go", "ts"}} {
		document, err := r.Split(languages...)
		if err != nil {
			t.Fatalf("Split(%v) = error %v", languages, err)
		}
		got := decodePrinted(t, document)
		reporters, isObject := got.Values["reporters"].(map[string]any)
		if !isObject {
			t.Fatalf("Split(%v) holds no reporters section\n%s", languages, document)
		}
		for _, key := range []string{"formats", "max_findings"} {
			if value, held := reporters[key]; held {
				t.Errorf("Split(%v) holds reporters.%s = %v, which the merged report takes\n%s", languages, key, value, document)
			}
			if source, held := got.Provenance["reporters."+key]; held {
				t.Errorf("Split(%v) names the source of reporters.%s, %q, which it does not hold", languages, key, source)
			}
		}
		want := map[string]any{"sort": "size", "cascade": "roots", "fail_on": "warn"}
		if !reflect.DeepEqual(reporters, want) {
			t.Errorf("Split(%v) reporters = %v, want %v", languages, reporters, want)
		}
	}
}

// A split document is a configuration: read back as one, it resolves to the values
// it holds, so an analyzer reading the same closed key list reads it.
func TestSplit_readsBackAsTheValuesItHolds(t *testing.T) {
	t.Parallel()

	r, err := config.Resolve(fromRepository(mixedTarget))
	if err != nil {
		t.Fatalf("Setup: Resolve(a mixed target) = error %v", err)
	}
	document, err := r.Split("ts")
	if err != nil {
		t.Fatalf("Split(ts) = error %v", err)
	}
	back, err := config.Resolve(&config.Inputs{
		ContractVersion: testContractVersion,
		Repository:      config.Document{Path: "config.ts.json", Data: document},
	})
	if err != nil {
		t.Fatalf("Resolve(the split for ts) = error %v, want it to read back\n%s", err, document)
	}
	split, reread := decodePrinted(t, document).Values, printed(t, back).Values
	analysis, isObject := reread["analysis"].(map[string]any)
	if !isObject {
		t.Fatalf("Resolve(the split for ts) holds no analysis section")
	}
	reporters, isObject := reread["reporters"].(map[string]any)
	if !isObject {
		t.Fatalf("Resolve(the split for ts) holds no reporters section")
	}
	// The split withholds these, so they read back as their defaults.
	delete(analysis, "languages")
	delete(reporters, "formats")
	delete(reporters, "max_findings")
	for key, value := range split {
		if !reflect.DeepEqual(reread[key], value) {
			t.Errorf("the split for ts read back holds %s = %v, want %v", key, reread[key], value)
		}
	}
}
