package config_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/deadset/internal/config"
)

// testDigest is a digest of the spelling the provider list accepts.
const testDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// An installed entry reads as one with no artifact, and an acquirable entry
// carries every member of its artifact.
func TestResolve_readsEachProviderEntry(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		document string
		want     []config.Provider
	}{
		{
			name:     "default",
			document: `{"target": {"kind": "library"}}`,
			want: []config.Provider{
				{Name: "deadset-go", Languages: []string{"go"}, Command: "deadset-go"},
				{Name: "deadset-ts", Languages: []string{"ts"}, Command: "deadset-ts"},
			},
		},
		{
			name: "default-deleted",
			document: `{"target": {"kind": "library"}, "providers": {"analyzers": [
				{"name": "deadset-ts", "languages": ["ts"], "command": "deadset-ts"}]}}`,
			want: []config.Provider{{Name: "deadset-ts", Languages: []string{"ts"}, Command: "deadset-ts"}},
		},
		{
			name:     "every-entry-deleted",
			document: `{"target": {"kind": "library"}, "providers": {"analyzers": []}}`,
			want:     []config.Provider{},
		},
		{
			name: "default-edited-and-one-added",
			document: `{"target": {"kind": "library"}, "providers": {"analyzers": [
				{"name": "deadset-go", "languages": ["go"], "command": "/opt/deadset/bin/deadset-go"},
				{"name": "deadset-ts", "languages": ["ts"], "command": "deadset-ts"},
				{"name": "other-go", "languages": ["go", "ts"], "command": "other-go",
				 "source": "go:example.com/other/cmd/other-go", "version": "2.1.0-rc.1", "digest": "` + testDigest + `"}]}}`,
			want: []config.Provider{
				{Name: "deadset-go", Languages: []string{"go"}, Command: "/opt/deadset/bin/deadset-go"},
				{Name: "deadset-ts", Languages: []string{"ts"}, Command: "deadset-ts"},
				{
					Name: "other-go", Languages: []string{"go", "ts"}, Command: "other-go",
					Artifact: &config.Artifact{Source: "go:example.com/other/cmd/other-go", Version: "2.1.0-rc.1", Digest: testDigest},
				},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			r, err := config.Resolve(fromRepository(c.document))
			if err != nil {
				t.Fatalf("Resolve(%s) = error %v", c.document, err)
			}
			if got := r.Config.Providers; !reflect.DeepEqual(got, c.want) {
				t.Errorf("Resolve(%s).Config.Providers = %s, want %s", c.document, spellProviders(got), spellProviders(c.want))
			}
		})
	}
}

// spellProviders renders a provider list with each artifact spelled out, which
// a pointer printed with %v would hide.
func spellProviders(list []config.Provider) string {
	spelled := make([]string, len(list))
	for i, p := range list {
		spelled[i] = fmt.Sprintf("{%s %q %v", p.Name, p.Command, p.Languages)
		if p.Artifact != nil {
			spelled[i] += fmt.Sprintf(" %+v", *p.Artifact)
		}
		spelled[i] += "}"
	}
	return "[" + strings.Join(spelled, " ") + "]"
}
