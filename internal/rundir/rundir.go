// Package rundir lays out the run directory: the evidence for every exit code
// the orchestrator returns. Per provider entry it holds what the analyzer
// described itself as, the configuration it was handed, the report it wrote
// and any scope document of its own, each keyed by the entry's name because
// two entries may claim one language; beside them it holds the run's scope
// document, the merged report and the renderings of the merged report that
// are files of their own.
//
// A run directory belongs to one run. [Create] refuses a directory that
// already exists and every write refuses a file that already exists, so no
// file one run reads can be left over from another.
package rundir

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cplieger/deadset/internal/config"
	"github.com/cplieger/deadset/internal/report"
)

// The files of a run directory that no provider entry keys.
const (
	scopeFile  = "scope.json"
	mergedFile = "report.json"
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
		return nil, fmt.Errorf("rundir: create the run directory: %w", err)
	}
	if err := os.Mkdir(absolute, 0o750); err != nil {
		return nil, fmt.Errorf("rundir: create the run directory: %w", err)
	}
	return &Dir{path: absolute}, nil
}

// CreateTemp makes a run directory of a name no other directory holds, under
// the directory [os.TempDir] names.
func CreateTemp() (*Dir, error) {
	path, err := os.MkdirTemp("", "deadset-run-")
	if err != nil {
		return nil, fmt.Errorf("rundir: create the run directory: %w", err)
	}
	return &Dir{path: path}, nil
}

// Path is the path of the run directory.
func (d *Dir) Path() string { return d.path }

// Scope is the path of the scope document the analyzers of the run read.
func (d *Dir) Scope() string { return filepath.Join(d.path, scopeFile) }

// Merged is the path of the merged report.
func (d *Dir) Merged() string { return filepath.Join(d.path, mergedFile) }

// WriteMerged writes r, in the encoding [report.Encode] writes, as the merged
// report. A report Encode refuses leaves no file behind.
func (d *Dir) WriteMerged(r *report.Report) error {
	var encoded bytes.Buffer
	if err := report.Encode(&encoded, r); err != nil {
		return err
	}
	return writeOnce(d.Merged(), encoded.Bytes())
}

// Rendering is the path of the merged report's rendering whose file carries
// suffix, appended to the merged report's own name.
func (d *Dir) Rendering(suffix string) string { return d.Merged() + suffix }

// WriteRendering writes data as the merged report's rendering whose file
// carries suffix.
func (d *Dir) WriteRendering(suffix string, data []byte) error {
	return writeOnce(d.Rendering(suffix), data)
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

// WriteDescribe keeps document, the bytes the entry's analyzer printed when it
// described itself, as they were printed.
func (e Entry) WriteDescribe(document []byte) error {
	return writeOnce(e.Describe(), document)
}

// WriteConfig writes the configuration an analyzer claiming languages
// receives, as [config.Resolved.Split] renders it from resolved.
func (e Entry) WriteConfig(resolved *config.Resolved, languages ...string) error {
	document, err := resolved.Split(languages...)
	if err != nil {
		return err
	}
	return writeOnce(e.Config(), document)
}

// file is the path of the entry's file of one kind.
func (e Entry) file(kind string) string {
	return filepath.Join(e.dir, kind+"."+e.name+".json")
}

// writeOnce creates the file at path holding data, refusing a file that
// already exists.
func writeOnce(path string, data []byte) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("rundir: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("rundir: %w", closeErr)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("rundir: %w", err)
	}
	return nil
}
