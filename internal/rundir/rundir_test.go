package rundir_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	spec "github.com/cplieger/deadset-spec/v3"
	"github.com/cplieger/deadset/internal/config"
	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/rundir"
)

// mixedTarget is a repository configuration for a target holding both languages.
const mixedTarget = `{
  "target": {"kind": "library"},
  "go": {},
  "ts": {"entry_files": ["src/cli.ts"]}
}`

// resolved resolves document as the one configuration source of a run.
func resolved(t *testing.T, document string) *config.Resolved {
	t.Helper()

	r, err := config.Resolve(&config.Inputs{
		ContractVersion: report.ContractVersion,
		Repository:      config.Document{Path: "deadset.json", Data: []byte(document)},
	})
	if err != nil {
		t.Fatalf("Setup: resolve %s: %v", document, err)
	}
	return r
}

// created is a run directory created under a fresh temporary directory.
func created(t *testing.T) (*rundir.Dir, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "run")
	dir, err := rundir.Create(path)
	if err != nil {
		t.Fatalf("Setup: Create(%s): %v", path, err)
	}
	return dir, path
}

// entry is the files of the provider entry named name.
func entry(t *testing.T, dir *rundir.Dir, name string) rundir.Entry {
	t.Helper()

	e, err := dir.Entry(name)
	if err != nil {
		t.Fatalf("Setup: Entry(%q): %v", name, err)
	}
	return e
}

// listed is the names of the files the directory at path holds, sorted.
func listed(t *testing.T, path string) []string {
	t.Helper()

	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read the run directory %s: %v", path, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	return names
}

// TestARunKeepsEveryFileKeyedByEntryName writes what a run writes for two
// entries claiming one language, and the run directory holds one describe, one
// configuration and one report per entry, each named for the entry, beside the
// scope document and the merged report, with nothing else.
func TestARunKeepsEveryFileKeyedByEntryName(t *testing.T) {
	t.Parallel()

	dir, path := created(t)
	r := resolved(t, mixedTarget)
	if err := dir.WriteScope(&rundir.Scope{Root: path, Target: rundir.Module{Path: "."}}); err != nil {
		t.Fatalf("WriteScope() = %v", err)
	}
	for _, name := range []string{"deadset-go", "deadset-go-next"} {
		e := entry(t, dir, name)
		if err := e.WriteDescribe([]byte(`{"name":"deadset-go"}`)); err != nil {
			t.Fatalf("Entry(%q).WriteDescribe() = %v", name, err)
		}
		if err := e.WriteConfig(r, "go"); err != nil {
			t.Fatalf("Entry(%q).WriteConfig(go) = %v", name, err)
		}
		// The analyzer writes its own report, at the path the entry names.
		if err := os.WriteFile(e.Report(), []byte("{}"), 0o600); err != nil {
			t.Fatalf("Setup: write the report at %s: %v", e.Report(), err)
		}
	}
	merged, err := report.Decode(exampleReport(t))
	if err != nil {
		t.Fatalf("Setup: decode the example report: %v", err)
	}
	if err := dir.WriteMerged(merged); err != nil {
		t.Fatalf("WriteMerged() = %v", err)
	}

	want := []string{
		"config.deadset-go-next.json", "config.deadset-go.json",
		"describe.deadset-go-next.json", "describe.deadset-go.json",
		"report.deadset-go-next.json", "report.deadset-go.json",
		"report.json", "scope.json",
	}
	if got := listed(t, path); !slices.Equal(got, want) {
		t.Errorf("the run directory holds %q, want %q", got, want)
	}
	if got, want := dir.Merged(), filepath.Join(path, "report.json"); got != want {
		t.Errorf("Merged() = %q, want %q", got, want)
	}
	if got, want := dir.Scope(), filepath.Join(path, "scope.json"); got != want {
		t.Errorf("Scope() = %q, want %q", got, want)
	}
}

// exampleReport is a report document the Contract publishes.
func exampleReport(t *testing.T) []byte {
	t.Helper()

	body, err := fs.ReadFile(spec.Vectors, "vectors/merge/one-report/inputs/00-go.json")
	if err != nil {
		t.Fatalf("Setup: read the example report: %v", err)
	}
	return body
}

// TestEntryRefusesANameThatCannotKeyAFile pins the names a run directory
// refuses, each of which would name a file outside the directory or no file.
func TestEntryRefusesANameThatCannotKeyAFile(t *testing.T) {
	t.Parallel()

	dir, _ := created(t)
	for _, name := range []string{"", ".", "..", "../deadset-go", "go/deadset", "deadset\x00go"} {
		if _, err := dir.Entry(name); !errors.Is(err, rundir.ErrName) {
			t.Errorf("Entry(%q) = %v, want an error satisfying errors.Is(err, ErrName)", name, err)
		}
	}
}

// TestCreateRefusesAnExistingDirectory pins that a run directory is never one
// an earlier run left behind.
func TestCreateRefusesAnExistingDirectory(t *testing.T) {
	t.Parallel()

	_, path := created(t)
	if _, err := rundir.Create(path); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Create(%s) over an existing run directory = %v, want an error satisfying errors.Is(err, fs.ErrExist)", path, err)
	}
}

// TestCreateTempMakesAnEmptyDirectoryOfItsOwn pins that two runs given no
// directory each get a fresh, empty one, at the path the run names.
func TestCreateTempMakesAnEmptyDirectoryOfItsOwn(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	first, err := rundir.CreateTemp()
	if err != nil {
		t.Fatalf("CreateTemp() = %v", err)
	}
	second, err := rundir.CreateTemp()
	if err != nil {
		t.Fatalf("CreateTemp() a second time = %v", err)
	}
	if first.Path() == second.Path() {
		t.Errorf("CreateTemp() twice = %s both times, want two directories", first.Path())
	}
	for _, dir := range []*rundir.Dir{first, second} {
		held, err := os.ReadDir(dir.Path())
		if err != nil || len(held) != 0 {
			t.Errorf("ReadDir(%s) = %v, %v, want an empty directory", dir.Path(), held, err)
		}
		if got := filepath.Dir(dir.Scope()); got != dir.Path() {
			t.Errorf("the scope document of %s is in %s, want the run directory", dir.Path(), got)
		}
	}
}

// TestWriteConfigWritesTheSplitForTheEntrysLanguages pins the configuration an
// entry's analyzer reads to the split of the resolved configuration for the
// languages the entry claims, byte for byte.
func TestWriteConfigWritesTheSplitForTheEntrysLanguages(t *testing.T) {
	t.Parallel()

	dir, _ := created(t)
	r := resolved(t, mixedTarget)
	e := entry(t, dir, "deadset-ts")
	if err := e.WriteConfig(r, "ts"); err != nil {
		t.Fatalf("WriteConfig(ts) = %v", err)
	}

	want, err := r.Split("ts")
	if err != nil {
		t.Fatalf("Setup: Split(ts): %v", err)
	}
	got, err := os.ReadFile(e.Config())
	if err != nil {
		t.Fatalf("read %s: %v", e.Config(), err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WriteConfig(ts) wrote\n%s\nwant Split(ts)\n%s", got, want)
	}
}

// TestWriteDescribeKeepsTheBytesAsPrinted pins that the describe evidence is the
// analyzer's output itself, whatever it holds.
func TestWriteDescribeKeepsTheBytesAsPrinted(t *testing.T) {
	t.Parallel()

	dir, _ := created(t)
	e := entry(t, dir, "deadset-go")
	printed := []byte("{\"name\": \"deadset-go\",\n  \"truncated")
	if err := e.WriteDescribe(printed); err != nil {
		t.Fatalf("WriteDescribe() = %v", err)
	}
	got, err := os.ReadFile(e.Describe())
	if err != nil {
		t.Fatalf("read %s: %v", e.Describe(), err)
	}
	if !bytes.Equal(got, printed) {
		t.Errorf("WriteDescribe(%q) kept %q, want the bytes as printed", printed, got)
	}
}

// TestEveryFileIsWrittenOnce pins that a second write of any file of the run
// directory is refused and leaves the first one as it was.
func TestEveryFileIsWrittenOnce(t *testing.T) {
	t.Parallel()

	r := resolved(t, mixedTarget)
	merged, err := report.Decode(exampleReport(t))
	if err != nil {
		t.Fatalf("Setup: decode the example report: %v", err)
	}
	writes := []struct {
		write func(*rundir.Dir, rundir.Entry) error
		path  func(*rundir.Dir, rundir.Entry) string
		name  string
	}{
		{
			name:  "describe",
			write: func(_ *rundir.Dir, e rundir.Entry) error { return e.WriteDescribe([]byte("{}")) },
			path:  func(_ *rundir.Dir, e rundir.Entry) string { return e.Describe() },
		},
		{
			name:  "config",
			write: func(_ *rundir.Dir, e rundir.Entry) error { return e.WriteConfig(r, "go") },
			path:  func(_ *rundir.Dir, e rundir.Entry) string { return e.Config() },
		},
		{
			name:  "merged",
			write: func(d *rundir.Dir, _ rundir.Entry) error { return d.WriteMerged(merged) },
			path:  func(d *rundir.Dir, _ rundir.Entry) string { return d.Merged() },
		},
		{
			name: "scope",
			write: func(d *rundir.Dir, _ rundir.Entry) error {
				return d.WriteScope(&rundir.Scope{Root: "/src/run", Target: rundir.Module{Path: "."}})
			},
			path: func(d *rundir.Dir, _ rundir.Entry) string { return d.Scope() },
		},
	}
	for _, w := range writes {
		t.Run(w.name, func(t *testing.T) {
			t.Parallel()

			dir, _ := created(t)
			e := entry(t, dir, "deadset-go")
			if err := w.write(dir, e); err != nil {
				t.Fatalf("the first %s write = %v", w.name, err)
			}
			first, err := os.ReadFile(w.path(dir, e))
			if err != nil {
				t.Fatalf("read the %s file: %v", w.name, err)
			}
			if err := w.write(dir, e); !errors.Is(err, fs.ErrExist) {
				t.Errorf("the second %s write = %v, want an error satisfying errors.Is(err, fs.ErrExist)", w.name, err)
			}
			if again, _ := os.ReadFile(w.path(dir, e)); !bytes.Equal(again, first) {
				t.Errorf("the refused %s write changed the file to\n%s\nwant\n%s", w.name, again, first)
			}
		})
	}
}

// TestWriteMergedRefusesAReportEncodeRefuses pins that a merged report the
// encoding refuses leaves no file, so a later write of the corrected report is
// not refused as a second one.
func TestWriteMergedRefusesAReportEncodeRefuses(t *testing.T) {
	t.Parallel()

	dir, _ := created(t)
	merged, err := report.Decode(exampleReport(t))
	if err != nil {
		t.Fatalf("Setup: decode the example report: %v", err)
	}
	refused := *merged
	refused.Findings = nil
	if err := dir.WriteMerged(&refused); !errors.As(err, new(*report.Error)) {
		t.Fatalf("WriteMerged(a report with no findings array) = %v, want a *report.Error", err)
	}
	if _, err := os.Stat(dir.Merged()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("after a refused WriteMerged, Stat(%s) = %v, want no file", dir.Merged(), err)
	}
	if err := dir.WriteMerged(merged); err != nil {
		t.Errorf("WriteMerged(the report) after a refused one = %v, want it written", err)
	}
}
