// Package scope is the Contract's scope document: the target a run reports on
// and the consumers whose references count against it, each a directory on
// the local filesystem. The run reads the document an invocation names and
// writes the one every analyzer's analyze verb reads, and both are this one
// model, every path absolute.
package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/cplieger/atomicfile/v4"
	spec "github.com/cplieger/deadset-spec/v8"
	"github.com/cplieger/deadset-spec/v8/document"
	"github.com/cplieger/deadset-spec/v8/vocab"
)

// ErrDocument reports a scope document the Contract's scope schema refuses.
var ErrDocument = errors.New("scope: the document is refused")

var schema = sync.OnceValues(func() (*document.Schema, error) {
	return document.Compile(spec.Contract, "contract/scope.schema.json")
})

// Module is one module a scope document names.
type Module struct {
	// ID is the name the module publishes itself under, empty where the
	// document leaves the name to the analyzer's load.
	ID string

	// Path is the module's directory, absolute and clean.
	Path string
}

// Document is one scope document: the target, the workspace file and the
// consumers in the order the document lists them.
type Document struct {
	// Workspace is the absolute, clean path of the workspace file the document
	// names, and empty where it names none.
	Workspace string

	Target    Module
	Consumers []Module
}

// wire is the document as the scope schema spells it.
type wire struct {
	Target    wireModule   `json:"target"`
	Workspace string       `json:"workspace,omitempty"`
	Consumers []wireModule `json:"consumers,omitempty"`
}

type wireModule struct {
	ID   string `json:"id,omitempty"`
	Role string `json:"role"`
	Path string `json:"path"`
}

// Read reads the scope document at path, resolving a relative path inside it
// against the directory that holds it. A document larger than
// [vocab.DocumentMaxBytes], one that is not strict JSON and one the scope
// schema refuses are errors satisfying errors.Is(err, [ErrDocument]) that name
// the file and the JSON Pointer at fault; an unreadable document wraps the
// read's error, so an absent one satisfies errors.Is(err, fs.ErrNotExist).
func Read(ctx context.Context, path string) (*Document, error) {
	compiled, err := schema()
	if err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("scope: %w", err)
	}
	data, err := atomicfile.ReadBounded(ctx, absolute, vocab.DocumentMaxBytes)
	if errors.Is(err, atomicfile.ErrFileTooLarge) {
		return nil, fmt.Errorf("%w: %s: the document is larger than the %d bytes one may hold", ErrDocument, path, vocab.DocumentMaxBytes)
	}
	if err != nil {
		return nil, fmt.Errorf("scope: %w", err)
	}
	var read wire
	if err := compiled.Unmarshal(data, &read); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrDocument, path, err)
	}
	base := filepath.Dir(absolute)
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(base, p)
	}
	d := &Document{Target: Module{ID: read.Target.ID, Path: resolve(read.Target.Path)}}
	if read.Workspace != "" {
		d.Workspace = resolve(read.Workspace)
	}
	for _, consumer := range read.Consumers {
		d.Consumers = append(d.Consumers, Module{ID: consumer.ID, Path: resolve(consumer.Path)})
	}
	return d, nil
}

// Encode is the document as the scope schema states it: the target with the
// role target and each consumer with the role consumer, in the order d holds
// them, every path absolute, two spaces of indentation and a trailing line
// feed.
func (d *Document) Encode() ([]byte, error) {
	out := wire{Target: wireModule{ID: d.Target.ID, Role: "target", Path: d.Target.Path}, Workspace: d.Workspace}
	for _, consumer := range d.Consumers {
		out.Consumers = append(out.Consumers, wireModule{ID: consumer.ID, Role: "consumer", Path: consumer.Path})
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(&out); err != nil {
		return nil, fmt.Errorf("scope: encode the document: %w", err)
	}
	return encoded.Bytes(), nil
}

// Root is the deepest directory that holds the target, every consumer and the
// workspace file, which is the one directory every path of the document is
// beneath.
func (d *Document) Root() string {
	root := d.Target.Path
	for _, consumer := range d.Consumers {
		root = ancestor(root, consumer.Path)
	}
	if d.Workspace != "" {
		root = ancestor(root, filepath.Dir(d.Workspace))
	}
	return root
}

// ancestor is the deepest directory holding both of two absolute, clean
// paths: dir itself or one of its parents.
func ancestor(dir, path string) string {
	for {
		if relative, err := filepath.Rel(dir, path); err == nil && filepath.IsLocal(relative) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}
