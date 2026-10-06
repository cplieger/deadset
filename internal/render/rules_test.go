package render

import (
	"bytes"
	"slices"
	"testing"

	spec "github.com/cplieger/deadset-spec/v6"
)

// The embedded vocabulary is the Contract's issue-kind vocabulary byte for
// byte, so every rule the SARIF rendering writes is the Contract's.
func TestTheEmbeddedVocabularyIsTheContracts(t *testing.T) {
	t.Parallel()

	published, err := spec.Contract.ReadFile("contract/kinds.json")
	if err != nil {
		t.Fatalf("Setup: read contract/kinds.json: %v", err)
	}
	if !bytes.Equal(vocabulary, published) {
		t.Errorf("internal/render/kinds.json differs from the Contract's contract/kinds.json; copy the pinned module's file over it")
	}
}

// A first sentence ends at the first full stop a space follows or that ends
// the text, so a full stop inside a token does not end it.
func TestFirstSentence(t *testing.T) {
	t.Parallel()

	for text, want := range map[string]string{
		"An export. Another sentence.":      "An export.",
		"Names go.mod and stops. Then more": "Names go.mod and stops.",
		"One sentence.":                     "One sentence.",
		"No full stop":                      "No full stop",
	} {
		if got := firstSentence(text); got != want {
			t.Errorf("firstSentence(%q) = %q, want %q", text, got, want)
		}
	}
}

// A run's rules are the live kinds of its languages in bytewise order of
// code, each carrying the kind's rule, its precondition after a blank line,
// and the levels and precision its defaults map to; a run of every language
// lists every live kind.
func TestRulesFor(t *testing.T) {
	t.Parallel()

	goRules, err := rulesFor([]string{"go"})
	if err != nil {
		t.Fatalf("rulesFor(go) = %v", err)
	}
	all, err := rulesFor(nil)
	if err != nil {
		t.Fatalf("rulesFor(nil) = %v", err)
	}
	kinds, err := liveKinds()
	if err != nil {
		t.Fatalf("liveKinds() = %v", err)
	}
	ids := func(rules []sarifRule) []string {
		held := make([]string, len(rules))
		for i := range rules {
			held[i] = rules[i].ID
		}
		return held
	}
	if !slices.IsSorted(ids(all)) || len(all) != len(kinds) {
		t.Errorf("rulesFor(nil) = %q, want all %d live kinds in order of code", ids(all), len(kinds))
	}
	if slices.Contains(ids(goRules), "DS1104") || !slices.Contains(ids(goRules), "DS1102") {
		t.Errorf("rulesFor(go) = %q, want DS1102, which applies to Go, and not DS1104, which applies to TypeScript alone", ids(goRules))
	}
	at := slices.Index(ids(goRules), "DS1102")
	rule := goRules[at]
	want := "DS1102 unnecessary-exposure warning/warning very-high"
	if got := rule.ID + " " + rule.Name + " " + rule.DefaultConfiguration.Level + "/" + rule.Properties.Problem.Severity +
		" " + rule.Properties.Precision; got != want {
		t.Errorf("rulesFor(go) DS1102 = %q, want %q", got, want)
	}
	if rule.FullDescription.Text == "" || rule.Help.Text == rule.FullDescription.Text ||
		!bytes.HasPrefix([]byte(rule.Help.Text), []byte(rule.FullDescription.Text+"\n\n")) {
		t.Errorf("rulesFor(go) DS1102 help = %q, want the rule %q, a blank line and the precondition", rule.Help.Text, rule.FullDescription.Text)
	}
}
