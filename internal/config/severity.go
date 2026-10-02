package config

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
)

// issueCodes are the codes of the issue kinds the Contract declares, and fixedCodes
// those among them whose severity the Contract fixes. A test pins both equal to the
// Contract's issue-kind vocabulary.
var (
	issueCodes = []string{
		"DS1001", "DS1002", "DS1003", "DS1004", "DS1005", "DS1006",
		"DS1101", "DS1102", "DS1103", "DS1104",
		"DS1201", "DS1203", "DS1204",
		"DS1301", "DS1302", "DS1303",
		"DS1501", "DS1502",
		"DS1601", "DS1605",
		"DS1701", "DS1702", "DS1703", "DS1704", "DS1705",
		"DS1801", "DS1802", "DS1803", "DS1805", "DS1807", "DS1809",
	}
	fixedCodes = []string{"DS1703", "DS1704"}
)

// severityKey is the spelling a severity key takes: an issue-kind code, or a
// two-digit range prefix naming a whole family.
var severityKey = regexp.MustCompile(`^DS\d{2}(\d{2})?$`)

// familyKeyLength is the length of a severity key naming a family: the prefix and
// two digits, where a code carries four.
const familyKeyLength = 4

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
		named := codesNamed(key)
		if fixed := slices.DeleteFunc(slices.Clone(named), isUnfixed); len(fixed) > 0 {
			return &rejection{
				at: "." + key, undeclared: true,
				detail: "the Contract fixes the severity of " + spellCodes(fixed) + ", which this key names",
			}
		}
		if len(named) == 0 {
			return &rejection{at: "." + key, undeclared: true, detail: "it names no issue kind the Contract declares"}
		}
		if rejected := oneOf(Allow, Warn, Deny)(keys[key]); rejected != nil {
			return rejected.under("." + key)
		}
	}
	return nil
}

// Severity returns the severity the resolved configuration sets for the issue
// kind code: its key for the code, then its key for the code's family. It
// reports false when the configuration names neither, which leaves the kind at
// its default.
func (r *Resolved) Severity(code string) (Severity, bool) {
	keys := []string{code}
	if len(code) > familyKeyLength {
		keys = append(keys, code[:familyKeyLength])
	}
	for _, key := range keys {
		var set Severity
		if value, named := r.severity[key]; named && decoded(value, &set) {
			return set, true
		}
	}
	return "", false
}

// codesNamed returns the codes one severity key names: the code itself, or every
// code of the family its two-digit prefix names.
func codesNamed(key string) []string {
	return slices.DeleteFunc(slices.Clone(issueCodes), func(code string) bool {
		return code != key && (len(key) != familyKeyLength || !strings.HasPrefix(code, key))
	})
}

// isUnfixed reports whether the Contract leaves the severity of code to the
// configuration.
func isUnfixed(code string) bool { return !slices.Contains(fixedCodes, code) }

// spellCodes lists codes as a refusal names them: one on its own, several joined
// by commas and a final and.
func spellCodes(codes []string) string {
	if len(codes) < 2 {
		return strings.Join(codes, "")
	}
	return strings.Join(codes[:len(codes)-1], ", ") + " and " + codes[len(codes)-1]
}
