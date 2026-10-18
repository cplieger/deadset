package run

import (
	"slices"
	"testing"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/plan"
	"github.com/cplieger/deadset/v2/internal/source"
)

// A dropped consumer is named on the outcome of each analyzer that left out a
// language it holds, and on no other.
func TestDropWithNamesAConsumerOnEachOutcomeThatLeftOutALanguageOfIt(t *testing.T) {
	t.Parallel()

	outcomes := []source.Outcome{
		{Analyzer: "deadset-go"},
		{Analyzer: "deadset-ts", Omitted: true, LeftOut: []vocab.Language{vocab.TS}},
		{Analyzer: "proto", Omitted: true, LeftOut: []vocab.Language{vocab.Proto}},
	}
	dropWith(outcomes, []plan.Dropped{
		{Path: "/w/web", Languages: []vocab.Language{vocab.TS}},
		{Path: "/w/schema", Languages: []vocab.Language{vocab.TS, vocab.Proto}},
	})
	want := [][]string{nil, {"/w/web", "/w/schema"}, {"/w/schema"}}
	for i := range outcomes {
		if !slices.Equal(outcomes[i].Dropped, want[i]) {
			t.Errorf("dropWith named %q on %s, want %q", outcomes[i].Dropped, outcomes[i].Analyzer, want[i])
		}
	}
}
