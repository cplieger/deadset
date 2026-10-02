package gomod_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/deadset/internal/gomod"
)

// writeFile writes one file a test's setup needs.
func writeFile(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("Setup: create the directory of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("Setup: write %s: %v", path, err)
	}
}

// Every form of the module directive the go.mod grammar admits names the
// module path it holds.
func TestPathReadsTheModuleDirective(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, file, want string
	}{
		{name: "identifier", file: "module example.com/app\n\ngo 1.27\n", want: "example.com/app"},
		{name: "interpreted-string", file: "module \"example.com/app\"\n", want: "example.com/app"},
		{name: "raw-string", file: "module `example.com/app`\n", want: "example.com/app"},
		{name: "trailing-comment", file: "module example.com/app // the server\n", want: "example.com/app"},
		{name: "after-comments", file: "// Deprecated: use example.com/app/v2.\n\nmodule example.com/app\n", want: "example.com/app"},
		{name: "after-other-directives", file: "go 1.27\n\nrequire example.com/dep v1.0.0\nmodule example.com/app\n", want: "example.com/app"},
		{name: "tab-separated", file: "module\texample.com/app\n", want: "example.com/app"},
		{name: "carriage-returns", file: "module example.com/app\r\ngo 1.27\r\n", want: "example.com/app"},
		{name: "block", file: "module (\n\texample.com/app\n)\n", want: "example.com/app"},
		{name: "block-with-comments", file: "module ( // the server\n\n\t// its path\n\t\"example.com/app\"\n)\n", want: "example.com/app"},
		{name: "block-no-space", file: "module(\n\texample.com/app\n)\n", want: "example.com/app"},
		{name: "keyword-prefix-is-no-directive", file: "modules example.com/other\nmodule example.com/app\n", want: "example.com/app"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "go.mod"), c.file)
			got, found, err := gomod.Path(dir)
			if got != c.want || !found || err != nil {
				t.Errorf("Path over a go.mod holding %q = %q, %t, %v, want %q, true, nil", c.file, got, found, err, c.want)
			}
		})
	}
}

// A go.mod file whose module directive is absent or holds no module path is
// an error naming the file.
func TestPathRefusesAModuleDirectiveWithNoPath(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, file, names string
	}{
		{name: "no-directive", file: "go 1.27\n", names: "no module directive"},
		{name: "empty-file", file: "", names: "no module directive"},
		{name: "keyword-alone", file: "module\ngo 1.27\n", names: "no module directive"},
		{name: "unterminated-string", file: "module \"example.com/app\n", names: "not a string literal"},
		{name: "two-tokens", file: "module example.com/app extra\n", names: "not a module path"},
		{name: "empty-string", file: "module \"\"\n", names: "not a module path"},
		{name: "empty-block", file: "module (\n)\n", names: "not a module path"},
		{name: "path-on-the-parenthesis-line", file: "module (example.com/app\n)\n", names: "not a module path"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			file := filepath.Join(dir, "go.mod")
			writeFile(t, file, c.file)
			got, found, err := gomod.Path(dir)
			if err == nil || !strings.Contains(err.Error(), file) || !strings.Contains(err.Error(), c.names) {
				t.Errorf("Path over a go.mod holding %q = %q, %t, %v, want an error naming %s and %q", c.file, got, found, err, file, c.names)
			}
		})
	}
}

// The go.mod file that governs a directory is the one in it, or else in the
// nearest directory above it; a directory named go.mod is not one.
func TestPathTakesTheNearestGoModAtOrAboveTheDirectory(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	writeFile(t, filepath.Join(base, "go.mod"), "module example.com/outer\n")
	writeFile(t, filepath.Join(base, "inner", "go.mod"), "module example.com/inner\n")
	writeFile(t, filepath.Join(base, "pkg", "deep", "file.go"), "package deep\n")
	if err := os.MkdirAll(filepath.Join(base, "shadow", "go.mod"), 0o750); err != nil {
		t.Fatalf("Setup: create the directory shadow/go.mod: %v", err)
	}

	for _, c := range []struct {
		dir, want string
	}{
		{dir: ".", want: "example.com/outer"},
		{dir: "inner", want: "example.com/inner"},
		{dir: "pkg/deep", want: "example.com/outer"},
		{dir: "shadow", want: "example.com/outer"},
	} {
		dir := filepath.Join(base, filepath.FromSlash(c.dir))
		if got, found, err := gomod.Path(dir); got != c.want || !found || err != nil {
			t.Errorf("Path(%s) = %q, %t, %v, want %q, true, nil", c.dir, got, found, err, c.want)
		}
	}
}

// A directory no go.mod file governs, up to the filesystem root, names no
// module and is no error.
func TestPathFindsNoModuleWhereNoGoModGovernsTheDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for above := dir; ; above = filepath.Dir(above) {
		if _, err := os.Stat(filepath.Join(above, "go.mod")); err == nil {
			t.Skipf("the temporary directory %s is inside the module at %s", dir, above)
		}
		if filepath.Dir(above) == above {
			break
		}
	}
	if got, found, err := gomod.Path(dir); got != "" || found || err != nil {
		t.Errorf("Path(%s) = %q, %t, %v, want \"\", false, nil", dir, got, found, err)
	}
}
