// Package reads decides which languages this build of deadset reads.
package reads

import (
	"slices"

	"github.com/cplieger/deadset-spec/v8/vocab"
)

// inProcess is the languages.json runner of a language whose reader links
// into the orchestrator.
const inProcess = "in-process"

// Language reports whether this build reads language: an analyzer process reads
// it, since this build links no in-process reader.
func Language(language vocab.Language) bool {
	row, _ := vocab.LanguageRowOf(language)
	return row.Runner != inProcess
}

// Rows is the languages.json row of every language this build reads, in the
// file's order.
func Rows() []vocab.LanguageRow {
	return slices.DeleteFunc(vocab.LanguageRows(), func(row vocab.LanguageRow) bool { return !Language(row.Language) })
}
