package config

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/cplieger/deadset-spec/v8/vocab"
)

// severityKey is the spelling a severity key takes: an issue-kind code, or a
// two-digit range prefix naming a whole family.
var severityKey = regexp.MustCompile(`^DS\d{2}(\d{2})?$`)

// checkCodeNames refuses a severity key that is neither a code nor a family
// prefix, as a key the closed key list does not declare. A value that is not an
// object at all is the severity check's to refuse.
func checkCodeNames(value json.RawMessage, label string) error {
	var keys map[string]json.RawMessage
	if !decoded(value, &keys) {
		return nil
	}
	for _, key := range sortedKeys(keys) {
		if !severityKey.MatchString(key) {
			return unimplemented(label, joinKey(severitySection, key),
				"a severity key is one issue-kind code or one two-digit family prefix")
		}
	}
	return nil
}

// checkSeverity accepts the severity object: each key names at least one issue
// kind the Contract declares and none whose severity it fixes, and each value is a
// severity. A key naming a fixed kind, or a family whose range holds one, is not a
// setting, so it is refused as an unimplemented key, and so is a key naming no kind
// at all.
func checkSeverity(value json.RawMessage) *rejection {
	var keys map[string]json.RawMessage
	if !decoded(value, &keys) {
		return reject("want an object")
	}
	for _, key := range sortedKeys(keys) {
		named := vocab.CodesOf(key)
		if fixed := fixedOf(named); len(fixed) > 0 {
			return &rejection{
				at: "." + key, undeclared: true,
				detail: "the Contract fixes the severity of " + spellCodes(fixed) + ", which this key names",
			}
		}
		if len(named) == 0 {
			return &rejection{at: "." + key, undeclared: true, detail: "it names no issue kind the Contract declares"}
		}
		if rejected := oneOf(vocab.Severities()...)(keys[key]); rejected != nil {
			return rejected.under("." + key)
		}
	}
	return nil
}

// Severity returns the severity the resolved configuration sets for the issue
// kind code: its key for the code, then its key for the code's family. It
// reports false when the configuration names neither, which leaves the kind at
// its default.
func (r *Resolved) Severity(code string) (vocab.Severity, bool) {
	keys := []string{code}
	if len(code) > vocab.FamilyKeyLength {
		keys = append(keys, vocab.FamilyOf(vocab.Code(code)))
	}
	for _, key := range keys {
		var set vocab.Severity
		if value, named := r.severity[key]; named && decoded(value, &set) {
			return set, true
		}
	}
	return "", false
}

// fixedOf returns the codes among named whose severity the Contract fixes.
func fixedOf(named []vocab.Code) []string {
	var fixed []string
	for _, code := range named {
		if kind, _ := vocab.KindOf(code); kind.Fixed {
			fixed = append(fixed, string(code))
		}
	}
	return fixed
}

// spellCodes lists codes as a refusal names them: one on its own, several joined
// by commas and a final and.
func spellCodes(codes []string) string {
	if len(codes) < 2 {
		return strings.Join(codes, "")
	}
	return strings.Join(codes[:len(codes)-1], ", ") + " and " + codes[len(codes)-1]
}
