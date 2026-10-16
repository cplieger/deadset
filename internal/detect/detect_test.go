package detect_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/detect"
)

// tree writes files, keyed by slash-separated path, under a fresh directory and
// returns that directory.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatalf("Setup: create the directory of %s: %v", name, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("Setup: write %s: %v", name, err)
		}
	}
	return root
}

// goProgram is Go source text, which several trees carry inside another
// language's file.
const goProgram = "package main\n\nfunc main() {}\n"

func TestLanguages(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		files map[string]string
		want  []vocab.Language
	}{
		"go module": {
			files: map[string]string{"go.mod": "module example.com/app\n", "main.go": goProgram},
			want:  []vocab.Language{vocab.Go},
		},
		"go source without a module file": {
			files: map[string]string{"tool.go": goProgram},
			want:  []vocab.Language{vocab.Go},
		},
		"module file below the root": {
			files: map[string]string{"services/api/go.mod": "module example.com/api\n"},
			want:  []vocab.Language{vocab.Go},
		},
		"typescript project file alone": {
			files: map[string]string{"tsconfig.json": "{}"},
			want:  []vocab.Language{vocab.TS},
		},
		"named typescript project file": {
			files: map[string]string{"web/tsconfig.app.json": "{}"},
			want:  []vocab.Language{vocab.TS},
		},
		"manifest with typescript sources and no compiler configuration": {
			files: map[string]string{"package.json": "{}", "src/index.mts": "export {};\n"},
		},
		"mixed": {
			files: map[string]string{
				"server/go.mod": "module example.com/server\n", "server/main.go": goProgram,
				"web/package.json": `{"name": "@example/web"}`, "web/tsconfig.json": "{}", "web/src/app.ts": "export const app = 1;\n",
			},
			want: []vocab.Language{vocab.Go, vocab.TS},
		},
		"go text inside documents, archives and a typescript string literal": {
			files: map[string]string{
				"tsconfig.json": "{}", "src/gen.ts": "export const program = `" + goProgram + "`;\n",
				"docs/usage.md":         "```go\n" + goProgram + "```\n",
				"fixtures/module.txtar": "-- go.mod --\nmodule example.com/fixture\n-- main.go --\n" + goProgram,
			},
			want: []vocab.Language{vocab.TS},
		},
		"directories the go row skips": {
			files: map[string]string{
				"tsconfig.json":                 "{}",
				"vendor/example.com/lib/lib.go": "package lib\n",
				"testdata/fixture/go.mod":       "module example.com/fixture\n",
				".cache/build/main.go":          goProgram,
				"_tools/mod/go.mod":             "module example.com/tools\n",
				"node_modules/flatted/x.go":     "package flatted\n",
			},
			want: []vocab.Language{vocab.TS},
		},
		"typescript configurations the go row's skips do not hide": {
			files: map[string]string{".config/tsconfig.json": "{}", "testdata/web/tsconfig.json": "{}", "_app/tsconfig.json": "{}"},
			want:  []vocab.Language{vocab.TS},
		},
		"directories the typescript row skips": {
			files: map[string]string{"node_modules/lib/tsconfig.json": "{}", ".git/tsconfig.json": "{}"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := detect.Languages(tree(t, tc.files))
			if err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("Languages(%v) = %q, %v, want %q", tc.files, got, err, tc.want)
			}
		})
	}
}

// A root is read whatever its name, and through a link.
func TestLanguagesReadsTheRootItself(t *testing.T) {
	t.Parallel()

	base := tree(t, map[string]string{"_tools/go.mod": "module example.com/tools\n"})
	link := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(filepath.Join(base, "_tools"), link); err != nil {
		t.Fatalf("Setup: link %s: %v", link, err)
	}
	for _, root := range []string{filepath.Join(base, "_tools"), link} {
		if got, err := detect.Languages(root); err != nil || !slices.Equal(got, []vocab.Language{vocab.Go}) {
			t.Errorf("Languages(%s) = %q, %v, want go", root, got, err)
		}
	}
	if _, err := detect.Languages(filepath.Join(base, "absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Languages(an absent root) = %v, want an error matching fs.ErrNotExist", err)
	}
}
