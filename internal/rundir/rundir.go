// Package rundir lays out the run directory: the evidence for every exit code
// the orchestrator returns. Per provider entry it holds what the analyzer
// described itself as, the configuration it was handed, the report it wrote
// and any scope document of its own, each keyed by the entry's name because
// two entries may claim one language; beside them it holds the run's scope
// document, the merged report and the renderings of the merged report that
// are files of their own. The files of the analyzers a run produces again
// over a narrowed scope lie in a directory of their own inside it, [Dir.Rerun].
//
// A run directory belongs to one run. [Create] refuses a directory that
// already exists and every write refuses a file that already exists, so no
// file one run reads can be left over from another. A directory or a file
// that cannot be made is a document the run cannot write.
package rundir

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/failure"
	"github.com/cplieger/go/filesafe"
)

// The files of a run directory that no provider entry keys. rerunDir holds no
// full stop, so no entry's file is named like it.
const (
	scopeFile  = "scope.json"
	mergedFile = "report.json"
	rerunDir   = "rerun"
)

// ErrName reports a provider entry name that cannot key a file of the run
// directory: the empty name, "." and "..", and a name holding a slash or a NUL.
var ErrName = errors.New("rundir: the name cannot key a file of the run directory")

// Dir is one run's directory.
type Dir struct {
	path string
}

// Create makes the run directory at path, whose parent must exist. A relative
// path is resolved against the working directory, and every path the
// directory names is absolute, because the analyzers that read and write them
// run in another directory. It refuses a path that already exists, with an
// error satisfying errors.Is(err, fs.ErrExist).
func Create(path string) (*Dir, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, unwritten(fmt.Errorf("rundir: create the run directory: %w", err))
	}
	if err := os.Mkdir(absolute, 0o750); err != nil {
		return nil, unwritten(fmt.Errorf("rundir: create the run directory: %w", err))
	}
	return &Dir{path: absolute}, nil
}

// CreateTemp makes a run directory of a name no other directory holds, under
// the directory [os.TempDir] names.
func CreateTemp() (*Dir, error) {
	path, err := os.MkdirTemp("", "deadset-run-")
	if err != nil {
		return nil, unwritten(fmt.Errorf("rundir: create the run directory: %w", err))
	}
	return &Dir{path: path}, nil
}

// Path is the run directory's absolute path.
func (d *Dir) Path() string { return d.path }

// Rerun makes the run directory, inside d, of the analyzers the run produces
// again, refusing one that exists as [Create] does.
func (d *Dir) Rerun() (*Dir, error) { return Create(filepath.Join(d.path, rerunDir)) }

// Scope is the path of the scope document every analyzer of the run reads
// unless its entry holds one of its own.
func (d *Dir) Scope() string { return filepath.Join(d.path, scopeFile) }

// WriteScope writes document as the run's scope document.
func (d *Dir) WriteScope(ctx context.Context, document []byte) error {
	return WriteFile(ctx, d.Scope(), document)
}

// Merged is the path the merged report is written to.
func (d *Dir) Merged() string { return filepath.Join(d.path, mergedFile) }

// WriteMerged writes r, in the encoding [report.Encode] writes, as the merged
// report. A report Encode refuses leaves no file behind.
func (d *Dir) WriteMerged(ctx context.Context, r *report.Report) error {
	var encoded bytes.Buffer
	if err := report.Encode(&encoded, r); err != nil {
		return err
	}
	return WriteFile(ctx, d.Merged(), encoded.Bytes())
}

// Rendering is the path of the merged report's rendering whose file carries
// suffix, appended to the merged report's own name.
func (d *Dir) Rendering(suffix string) string { return d.Merged() + suffix }

// WriteRendering writes data as the merged report's rendering whose file
// carries suffix.
func (d *Dir) WriteRendering(ctx context.Context, suffix string, data []byte) error {
	return WriteFile(ctx, d.Rendering(suffix), data)
}

// Entry is the files of one provider entry.
type Entry struct {
	dir  string
	name string
}

// Entry returns the files of the provider entry named name, or an [ErrName]
// for a name that cannot key a file.
func (d *Dir) Entry(name string) (Entry, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return Entry{}, fmt.Errorf("%w: %q", ErrName, name)
	}
	return Entry{dir: d.path, name: name}, nil
}

// Describe is the path of what the entry's analyzer printed when it described
// itself.
func (e Entry) Describe() string { return e.file("describe") }

// Scope is the path of the scope document the entry's analyzer reads when the
// run's own does not name its consumers: those of a run whose declared
// consumers the entry's languages claim only some of.
func (e Entry) Scope() string { return e.file("scope") }

// Config is the path of the configuration the entry's analyzer reads.
func (e Entry) Config() string { return e.file("config") }

// Report is the path the entry's analyzer writes its report to.
func (e Entry) Report() string { return e.file("report") }

// Facts is the path the entry's analyzer writes its facts document to.
func (e Entry) Facts() string { return e.file("facts") }

// WriteDescribe keeps document, the bytes the entry's analyzer printed when it
// described itself, as they were printed.
func (e Entry) WriteDescribe(ctx context.Context, document []byte) error {
	return WriteFile(ctx, e.Describe(), document)
}

// WriteConfig writes document as the configuration the entry's analyzer
// reads.
func (e Entry) WriteConfig(ctx context.Context, document []byte) error {
	return WriteFile(ctx, e.Config(), document)
}

// WriteScope writes document as the scope document of the entry's analyzer
// alone.
func (e Entry) WriteScope(ctx context.Context, document []byte) error {
	return WriteFile(ctx, e.Scope(), document)
}

// file is the path of the entry's file of one kind.
func (e Entry) file(kind string) string {
	return filepath.Join(e.dir, kind+"."+e.name+".json")
}

// WriteFile publishes data at path, in a run directory, whole or not at all:
// the write every file of the directory takes. It refuses a path that already
// exists, with an error satisfying errors.Is(err, fs.ErrExist), and a
// cancelled ctx; every failure is the run failure unwritten-output.
func WriteFile(ctx context.Context, path string, data []byte) error {
	// The check stands before the rename-replace write because each path of
	// a run directory has one writer.
	if _, err := os.Lstat(path); err == nil {
		return unwritten(fmt.Errorf("rundir: %s: %w", path, fs.ErrExist))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return unwritten(fmt.Errorf("rundir: %w", err))
	}
	if _, err := filesafe.WriteFile(ctx, path, data, filesafe.WithMode(0o600)); err != nil {
		return unwritten(fmt.Errorf("rundir: %w", err))
	}
	return nil
}

func unwritten(err error) error { return failure.Wrap(vocab.RunUnwrittenOutput, err) }
