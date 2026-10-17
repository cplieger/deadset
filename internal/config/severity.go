package config

import (
	"encoding/json"

	"github.com/cplieger/deadset-spec/v8/vocab"
)

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
		if value, named := r.keyed[vocab.KeySeverity][key]; named && json.Unmarshal(value, &set) == nil {
			return set, true
		}
	}
	return "", false
}
