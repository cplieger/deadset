package pkgjson_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cplieger/deadset/v2/internal/pkgjson"
)

// A package.json names its package by a non-empty string name member, and
// every other manifest, an absent one included, names none.
func TestNameReadsTheNameMember(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, file string
		want       string
		named      bool
	}{
		{name: "named", file: `{"name": "@example/web", "version": "1.0.0"}`, want: "@example/web", named: true},
		{name: "last-of-two", file: `{"name": "first", "name": "second"}`, want: "second", named: true},
		{name: "absent-file"},
		{name: "no-name", file: `{"version": "1.0.0"}`},
		{name: "empty-name", file: `{"name": ""}`},
		{name: "number-name", file: `{"name": 7}`},
		{name: "null-name", file: `{"name": null}`},
		{name: "other-case", file: `{"Name": "@example/web"}`},
		{name: "array", file: `["@example/web"]`},
		{name: "not-json", file: `{"name": "@example/web"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if c.file != "" {
				if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(c.file), 0o600); err != nil {
					t.Fatalf("Setup: write package.json: %v", err)
				}
			}
			got, named := pkgjson.Name(dir)
			if got != c.want || named != c.named {
				t.Errorf("Name over %q = %q, %t, want %q, %t", c.file, got, named, c.want, c.named)
			}
		})
	}
}

// No directory above the one named is read.
func TestNameReadsNoParent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name": "@example/root"}`), 0o600); err != nil {
		t.Fatalf("Setup: write package.json: %v", err)
	}
	below := filepath.Join(dir, "below")
	if err := os.Mkdir(below, 0o750); err != nil {
		t.Fatalf("Setup: create %s: %v", below, err)
	}
	if got, named := pkgjson.Name(below); named {
		t.Errorf("Name(%s) = %q, true, want no name", below, got)
	}
}
