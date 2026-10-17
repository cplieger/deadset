package config_test

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/deadset/v2/internal/config"
)

// parseFlags parses args against the setting flags of the orchestrator's
// print-config verb and one flag that supplies no setting, the way a verb
// registers them.
func parseFlags(t *testing.T, args ...string) *flag.FlagSet {
	t.Helper()

	set := flag.NewFlagSet("print-config", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.String("target", ".", "the target root")
	config.RegisterFlags(set, []string{"target", "fail-on", "formats", "languages", "min-confidence"})
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

			got := map[string]any{}
			for key, value := range config.FlagSettings(parseFlags(t, c.args...)) {
				var decoded any
				if err := json.Unmarshal(value, &decoded); err != nil {
					t.Fatalf("FlagSettings(%v)[%s] = %s, which does not decode: %v", c.args, key, value, err)
				}
				got[string(key)] = decoded
			}
			if c.want == nil {
				c.want = map[string]any{}
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

	flags := config.FlagSettings(parseFlags(t, "--min-confidence=certain"))
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

// parseCount parses args against the one integer option cli.json lists,
// which no verb of the orchestrator takes today.
func parseCount(t *testing.T, args ...string) *flag.FlagSet {
	t.Helper()

	set := flag.NewFlagSet("count", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	config.RegisterFlags(set, []string{"max-findings"})
	if err := set.Parse(args); err != nil {
		t.Fatalf("Setup: parse %v: %v", args, err)
	}
	return set
}

// An integer option written as a whole number supplies that number; written
// otherwise, the configuration schema and its whole-number rule refuse it,
// naming the setting and the flag.
func TestFlagSettings_anIntegerOptionSuppliesTheNumberItWrites(t *testing.T) {
	t.Parallel()

	got := config.FlagSettings(parseCount(t, "--max-findings=5"))
	if value := string(got["reporters.max_findings"]); value != "5" {
		t.Errorf("FlagSettings(--max-findings=5)[reporters.max_findings] = %q, want 5", value)
	}
	cases := []struct {
		name, text, reason string
	}{
		{name: "exponent", text: "1e2", reason: "want a whole number"},
		{name: "fraction", text: "5.0", reason: "want a whole number"},
		{name: "not-a-number", text: "five", reason: "want [integer]"},
		{name: "above-its-maximum", text: "9007199254740992", reason: "above"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			in := fromRepository(`{"target": {"kind": "library"}}`)
			in.Flags = config.FlagSettings(parseCount(t, "--max-findings="+c.text))
			_, err := config.Resolve(in)
			refusal, isRefusal := errors.AsType[*config.Error](err)
			if !isRefusal {
				t.Fatalf("Resolve(--max-findings=%s) = error %v, want a *config.Error", c.text, err)
			}
			for _, want := range []string{"--max-findings", "reporters.max_findings", c.reason} {
				if !strings.Contains(refusal.Message, want) {
					t.Errorf("Resolve(--max-findings=%s) = %q, want the message to name %q", c.text, refusal, want)
				}
			}
		})
	}
}
