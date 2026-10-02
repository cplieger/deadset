package rundir

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
)

// ErrScopePath reports a scope naming a path that is not inside the run's
// working directory: a working directory that is not absolute, or a module
// or workspace path that is empty, absolute, or climbs out of it.
var ErrScopePath = errors.New("rundir: the scope names a path outside the run's working directory")

// Scope is what the analyzers of one run load: the target they report on and
// the consumers whose references count against it, each a directory inside the
// run's working directory.
type Scope struct {
	// Root is the run's working directory, an absolute path. Every other path
	// of the scope is relative to it and does not leave it.
	Root string

	// Workspace is the workspace file through which a consumer's load resolves
	// the target, where the target and its consumers share one; empty, each
	// consumer resolves the target through its own module file.
	Workspace string

	Target    Module
	Consumers []Module
}

// Module is one module a scope names.
type Module struct {
	// ID is the name the module publishes itself under; empty, the analyzer
	// takes the name its load reports for the directory.
	ID string

	// Path is the module's directory, relative to the scope's Root.
	Path string
}

// scopeDocument is the scope document as the Contract's scope schema states it.
type scopeDocument struct {
	Target    scopeModule   `json:"target"`
	Workspace string        `json:"workspace,omitempty"`
	Consumers []scopeModule `json:"consumers,omitempty"`
}

// scopeModule is one module of a scope document, with its role.
type scopeModule struct {
	ID   string `json:"id,omitempty"`
	Role string `json:"role"`
	Path string `json:"path"`
}

// WriteScope writes s as the run's scope document, at [Dir.Scope]: the target
// with the role target and each consumer with the role consumer, in the order
// s holds them, every path the absolute path of the directory or file it names
// inside s.Root. It refuses, with an error satisfying errors.Is(err,
// ErrScopePath) and before writing anything, a scope naming a path outside
// s.Root.
func (d *Dir) WriteScope(s *Scope) error { return writeScope(d.Scope(), s) }

// WriteScope writes s, as [Dir.WriteScope] writes the run's, as the scope
// document of the entry's analyzer alone, at [Entry.Scope].
func (e Entry) WriteScope(s *Scope) error { return writeScope(e.Scope(), s) }

// writeScope writes s as the scope document at the path file.
func writeScope(file string, s *Scope) error {
	if !filepath.IsAbs(s.Root) {
		return fmt.Errorf("%w: the working directory %q is not an absolute path", ErrScopePath, s.Root)
	}
	inside := func(member, path string) (string, error) {
		if !filepath.IsLocal(path) {
			return "", fmt.Errorf("%w: %s %q is not a local path inside %s", ErrScopePath, member, path, s.Root)
		}
		return filepath.Join(s.Root, path), nil
	}

	var document scopeDocument
	var err error
	if document.Target.Path, err = inside("the target path", s.Target.Path); err != nil {
		return err
	}
	document.Target.ID, document.Target.Role = s.Target.ID, "target"
	if s.Workspace != "" {
		if document.Workspace, err = inside("the workspace", s.Workspace); err != nil {
			return err
		}
	}
	for i, consumer := range s.Consumers {
		path, err := inside(fmt.Sprintf("consumer %d's path", i), consumer.Path)
		if err != nil {
			return err
		}
		document.Consumers = append(document.Consumers, scopeModule{ID: consumer.ID, Role: "consumer", Path: path})
	}

	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(&document); err != nil {
		return fmt.Errorf("rundir: encode the scope document: %w", err)
	}
	return writeOnce(file, encoded.Bytes())
}
