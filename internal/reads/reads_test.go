package reads_test

import (
	"slices"
	"testing"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/reads"
)

// This build reads every language an analyzer process reads and none read in
// process.
func TestReadsEveryProcessLanguageAndNoInProcessOne(t *testing.T) {
	t.Parallel()

	for _, row := range vocab.LanguageRows() {
		if got, want := reads.Language(row.Language), row.Runner == "process"; got != want {
			t.Errorf("Language(%s), runner %s = %t, want %t", row.Language, row.Runner, got, want)
		}
	}
}

// Rows holds the row of each language this build reads, in languages.json's
// order, and no other.
func TestRowsAreTheRowsOfTheLanguagesThisBuildReads(t *testing.T) {
	t.Parallel()

	var want, got []vocab.Language
	for _, row := range vocab.LanguageRows() {
		if reads.Language(row.Language) {
			want = append(want, row.Language)
		}
	}
	for _, row := range reads.Rows() {
		got = append(got, row.Language)
	}
	if !slices.Equal(got, want) || len(got) == 0 {
		t.Errorf("Rows() = %v, want %v", got, want)
	}
}
