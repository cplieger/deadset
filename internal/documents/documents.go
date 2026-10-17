// Package documents checks the documents at the target root that every
// analyzer of a run reads, the ignore file, the baseline and the edges
// document, before any analyzer runs, so a malformed one ends the run once,
// with the usage code, whichever analyzers the run would have invoked.
//
// Each document is checked whole, every entry of every scheme: the ignore
// file and the baseline through the shared suppression reader, the edges
// document against the Contract's edges schema. Whether a reference is in its
// language's grammar stays the analyzer's of that language to decide. Every
// document at the target root, the repository configuration included, is read
// through [Read].
package documents

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"

	"github.com/cplieger/atomicfile/v4"
	spec "github.com/cplieger/deadset-spec/v8"
	"github.com/cplieger/deadset-spec/v8/document"
	"github.com/cplieger/deadset-spec/v8/suppress"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/failure"
)

// Error is a shared document the run refuses; its message names the file and
// the value at fault.
type Error struct {
	err error
}

func (e *Error) Error() string { return e.err.Error() }

func (e *Error) Unwrap() error { return e.err }

var edgesSchema = sync.OnceValues(func() (*document.Schema, error) {
	return document.Compile(spec.Contract, "contract/edges.schema.json")
})

// Read reads the document name at the target root target, confined to the
// target's tree and bounded by [vocab.DocumentMaxBytes]. An absent document
// satisfies errors.Is(err, fs.ErrNotExist) and one over the bound
// errors.Is(err, atomicfile.ErrFileTooLarge); a name resolving outside the
// tree or to anything but a regular file is refused with nothing read.
func Read(ctx context.Context, target, name string) ([]byte, error) {
	root, err := os.OpenRoot(target)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return atomicfile.ReadBoundedInRoot(ctx, root, name, vocab.DocumentMaxBytes)
}

// Check refuses the first malformed shared document at the target root target
// as an [*Error], one over the bound [Read] holds it to included. An absent
// document is none, and one that cannot be read is the setup failure
// unreadable-document.
func Check(ctx context.Context, target string) error {
	edges, err := edgesSchema()
	if err != nil {
		return err
	}
	for _, held := range []struct {
		check func([]byte) error
		name  string
	}{
		{name: suppress.IgnoreFileName, check: func(data []byte) error { _, err := suppress.ReadIgnore(data); return err }},
		{name: suppress.BaselineFileName, check: func(data []byte) error { _, err := suppress.ReadBaseline(data); return err }},
		{name: vocab.EdgesFileName, check: func(data []byte) error { return checkEdges(edges, data) }},
	} {
		data, err := Read(ctx, target, held.name)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case errors.Is(err, atomicfile.ErrFileTooLarge):
			return &Error{err: fmt.Errorf("%s: the document is larger than the %d bytes one may hold", held.name, vocab.DocumentMaxBytes)}
		case err != nil:
			return failure.Wrap(vocab.SetupUnreadableDocument, fmt.Errorf("%s cannot be read: %w", held.name, err))
		}
		if err := held.check(data); err != nil {
			return &Error{err: err}
		}
	}
	return nil
}

// checkEdges refuses an edges document that is not strict JSON or that the
// edges schema refuses, naming the value a reader fixes first.
func checkEdges(schema *document.Schema, data []byte) error {
	value, err := document.Decode(data)
	if err != nil {
		return fmt.Errorf("edges: %s: %w", vocab.EdgesFileName, err)
	}
	if refused := schema.FirstRefusal(value); refused != nil {
		return fmt.Errorf("edges: %s: %w", vocab.EdgesFileName, refused)
	}
	return nil
}
