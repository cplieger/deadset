package config_test

import (
	"encoding/json"
	"flag"
	"io"
	"reflect"
	"testing"

	"github.com/cplieger/deadset/internal/config"
)

// parseFlags parses args against the setting flags and one flag that supplies no
// setting, the way a verb registers them.
func parseFlags(t *testing.T, args ...string) *flag.FlagSet {
	t.Helper()

	set := flag.NewFlagSet("print-config", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.String("target", ".", "the target root")
	config.RegisterFlags(set)
	if err := set.Parse(args); err != nil {
		t.Fatalf("Setup: parse %v: %v", args, err)
	}
	return set
}

// The flags the command line set supply their settings, a list flag its entries;
// a flag the command line left alone supplies nothing, and so does a flag that
// carries no setting.
func TestFlagSettings_supplyTheSettingsTheCommandLineSet(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want map[string]any
		name string
		args []string
	}{
		{name: "none", args: []string{"--target=src"}},
		{
			name: "string", args: []string{"--fail-on=warn"},
			want: map[string]any{"reporters.fail_on": "warn"},
		},
		{
			name: "list", args: []string{"--formats=text, sarif", "--languages=go"},
			want: map[string]any{"reporters.formats": []any{"text", "sarif"}, "analysis.languages": []any{"go"}},
		},
		{
			name: "empty-list", args: []string{"--languages="},
			want: map[string]any{"analysis.languages": []any{}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			document, err := config.FlagSettings(parseFlags(t, c.args...))
			if err != nil {
				t.Fatalf("FlagSettings(%v) = error %v", c.args, err)
			}
			if c.want == nil {
				if document != nil {
					t.Errorf("FlagSettings(%v) = %s, want nil", c.args, document)
				}
				return
			}
			var got map[string]any
			if err := json.Unmarshal(document, &got); err != nil {
				t.Fatalf("FlagSettings(%v) = %s, which does not decode: %v", c.args, document, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("FlagSettings(%v) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}

// A setting a flag supplies outranks every document, and its provenance names the
// flag as the command line spells it.
func TestFlagSettings_outrankTheDocuments(t *testing.T) {
	t.Parallel()

	flags, err := config.FlagSettings(parseFlags(t, "--min-confidence=certain"))
	if err != nil {
		t.Fatalf("FlagSettings(--min-confidence=certain) = error %v", err)
	}
	in := fromRepository(`{"target": {"kind": "library"}, "analysis": {"min_confidence": "probable"}}`)
	in.Flags = flags
	r, err := config.Resolve(in)
	if err != nil {
		t.Fatalf("Resolve(--min-confidence=certain over a repository configuration) = error %v", err)
	}
	got := printed(t, r)
	analysis, _ := got.Values["analysis"].(map[string]any)
	if analysis["min_confidence"] != "certain" || got.Provenance["analysis.min_confidence"] != "flag: --min-confidence" {
		t.Errorf("Resolve(--min-confidence=certain) min_confidence = %v from %q, want certain from flag: --min-confidence",
			analysis["min_confidence"], got.Provenance["analysis.min_confidence"])
	}
}
