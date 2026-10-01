package detect_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cplieger/deadset/internal/detect"
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

// mixed is a target holding a Go server and a TypeScript client, each in a
// directory of its own.
var mixed = map[string]string{
	"server/go.mod":           "module example.com/server\n",
	"server/main.go":          goProgram,
	"web/package.json":        `{"name": "@example/web"}`,
	"web/tsconfig.json":       "{}",
	"web/src/app.ts":          "export const app = 1;\n",
	"README.md":               "# example\n",
	"server/internal/wire.go": "package internal\n",
}

func TestLanguages(t *testing.T) {
	tests := map[string]struct {
		files map[string]string
		want  []string
	}{
		"go module": {
			files: map[string]string{"go.mod": "module example.com/app\n", "main.go": goProgram},
			want:  []string{"go"},
		},
		"go source without a module file": {
			files: map[string]string{"tool.go": goProgram},
			want:  []string{"go"},
		},
		"module file below the root": {
			files: map[string]string{"services/api/go.mod": "module example.com/api\n"},
			want:  []string{"go"},
		},
		"typescript project file alone": {
			files: map[string]string{"tsconfig.json": "{}"},
			want:  []string{"ts"},
		},
		"named typescript project file": {
			files: map[string]string{"web/tsconfig.app.json": "{}"},
			want:  []string{"ts"},
		},
		"manifest with typescript sources": {
			files: map[string]string{"package.json": "{}", "src/index.mts": "export {};\n"},
			want:  []string{"ts"},
		},
		"mixed": {
			files: mixed,
			want:  []string{"go", "ts"},
		},
		"manifest with no typescript source": {
			files: map[string]string{
				"go.mod": "module example.com/app\n", "main.go": goProgram,
				"package.json": "{}", "scripts/lint.js": "export {};\n",
			},
			want: []string{"go"},
		},
		"typescript source with no manifest": {
			files: map[string]string{
				"go.mod": "module example.com/app\n", "main.go": goProgram,
				"tools/gen.ts": "export {};\n",
			},
			want: []string{"go"},
		},
		"go text inside a typescript string literal": {
			files: map[string]string{
				"package.json": "{}", "tsconfig.json": "{}",
				"src/gen.ts": "export const program = `" + goProgram + "`;\n",
			},
			want: []string{"ts"},
		},
		"go text inside documents and archives": {
			files: map[string]string{
				"package.json": "{}", "src/index.ts": "export {};\n",
				"docs/usage.md":         "```go\n" + goProgram + "```\n",
				"fixtures/module.txtar": "-- go.mod --\nmodule example.com/fixture\n-- main.go --\n" + goProgram,
			},
			want: []string{"ts"},
		},
		"installed packages": {
			files: map[string]string{
				"package.json": "{}", "tsconfig.json": "{}", "src/index.ts": "export {};\n",
				"node_modules/flatted/golang/pkg/flatted/flatted.go": "package flatted\n",
			},
			want: []string{"ts"},
		},
		"vendored sources, test inputs and hidden directories": {
			files: map[string]string{
				"package.json": "{}", "tsconfig.json": "{}",
				"vendor/example.com/lib/lib.go": "package lib\n",
				"testdata/fixture/go.mod":       "module example.com/fixture\n",
				"internal/x/testdata/case.go":   goProgram,
				".cache/build/main.go":          goProgram,
			},
			want: []string{"ts"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			root := tree(t, tc.files)

			got, err := detect.Languages(root, detect.Options{})
			if err != nil {
				t.Fatalf("Languages(%v) error = %v, want nil", tc.files, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Languages(%v) = %q, want %q", tc.files, got, tc.want)
			}
		})
	}
}

func TestLanguagesFilters(t *testing.T) {
	tests := map[string]struct {
		files   map[string]string
		filters []string
		want    []string
	}{
		"one directory": {
			files:   mixed,
			filters: []string{"server"},
			want:    []string{"go"},
		},
		"the other directory": {
			files:   mixed,
			filters: []string{"web"},
			want:    []string{"ts"},
		},
		"both directories": {
			files:   mixed,
			filters: []string{"web", "server/"},
			want:    []string{"go", "ts"},
		},
		"one file": {
			files:   mixed,
			filters: []string{"web/tsconfig.json"},
			want:    []string{"ts"},
		},
		"a skipped directory named outright": {
			files:   map[string]string{"testdata/fixture/go.mod": "module example.com/fixture\n"},
			filters: []string{"testdata"},
			want:    []string{"go"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			root := tree(t, tc.files)

			got, err := detect.Languages(root, detect.Options{Filters: tc.filters})
			if err != nil {
				t.Fatalf("Languages(filters %q) error = %v, want nil", tc.filters, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Languages(filters %q) = %q, want %q", tc.filters, got, tc.want)
			}
		})
	}
}

func TestLanguagesOverride(t *testing.T) {
	tests := map[string]struct {
		configured []string
		want       []string
	}{
		"a narrower set than the census": {
			configured: []string{"go"},
			want:       []string{"go"},
		},
		"a language the tree does not hold": {
			configured: []string{"ts"},
			want:       []string{"ts"},
		},
		"repeated and unordered": {
			configured: []string{"ts", "go", "ts"},
			want:       []string{"go", "ts"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			root := tree(t, map[string]string{"go.mod": "module example.com/app\n", "main.go": goProgram})

			got, err := detect.Languages(root, detect.Options{Languages: tc.configured})
			if err != nil {
				t.Fatalf("Languages(configured %q) error = %v, want nil", tc.configured, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Languages(configured %q) = %q, want %q", tc.configured, got, tc.want)
			}
		})
	}
}

func TestLanguagesThroughALink(t *testing.T) {
	target := tree(t, map[string]string{"go.mod": "module example.com/app\n"})
	link := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Setup: link %s to %s: %v", link, target, err)
	}

	got, err := detect.Languages(link, detect.Options{})
	if err != nil {
		t.Fatalf("Languages(%s) error = %v, want nil", link, err)
	}
	if want := []string{"go"}; !slices.Equal(got, want) {
		t.Errorf("Languages(%s) = %q, want %q", link, got, want)
	}
}

func TestLanguagesRefuses(t *testing.T) {
	tests := map[string]struct {
		files map[string]string
		opts  detect.Options
		want  error
	}{
		"no language": {
			files: map[string]string{"README.md": "# notes\n", "package.json": "{}"},
			want:  detect.ErrNoLanguage,
		},
		"a filter holding no language": {
			files: mixed,
			opts:  detect.Options{Filters: []string{"README.md"}},
			want:  detect.ErrNoLanguage,
		},
		"a filter naming nothing": {
			files: mixed,
			opts:  detect.Options{Filters: []string{"web", "client"}},
			want:  fs.ErrNotExist,
		},
		"a filter naming nothing beside a configured set": {
			files: mixed,
			opts:  detect.Options{Languages: []string{"go"}, Filters: []string{"client"}},
			want:  fs.ErrNotExist,
		},
		"a filter leaving the target": {
			files: mixed,
			opts:  detect.Options{Filters: []string{"../server"}},
			want:  detect.ErrFilter,
		},
		"an absolute filter": {
			files: mixed,
			opts:  detect.Options{Filters: []string{"/server"}},
			want:  detect.ErrFilter,
		},
		"an empty filter": {
			files: mixed,
			opts:  detect.Options{Filters: []string{""}},
			want:  detect.ErrFilter,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			root := tree(t, tc.files)

			got, err := detect.Languages(root, tc.opts)
			if !errors.Is(err, tc.want) {
				t.Errorf("Languages(%+v) = %q, %v, want an error matching %v", tc.opts, got, err, tc.want)
			}
		})
	}
}

func TestLanguagesAbsentTarget(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")

	got, err := detect.Languages(root, detect.Options{})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Languages(%s) = %q, %v, want an error matching %v", root, got, err, fs.ErrNotExist)
	}
}
