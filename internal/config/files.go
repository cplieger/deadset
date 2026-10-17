package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cplieger/atomicfile/v4"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/documents"
)

// RepositoryFile is the name of the repository configuration at the target root.
const RepositoryFile = "deadset.json"

// Document is one configuration document: the file it was read from, which every
// provenance entry the document supplies names, and its contents. Data is nil when
// no document exists at Path.
type Document struct {
	Path string
	Data []byte
}

// ReadDocuments reads the configuration documents one invocation names: the
// repository configuration at the target root, read as [documents.Read] reads
// a document there, which may be absent, and the central configuration at
// central, which is read when central is not empty and must then exist. A
// document over [vocab.DocumentMaxBytes] is an [*Error].
func ReadDocuments(ctx context.Context, target, central string) (repository, centralDocument Document, err error) {
	repository = Document{Path: filepath.Join(target, RepositoryFile)}
	repository.Data, err = documents.Read(ctx, target, RepositoryFile)
	if errors.Is(err, fs.ErrNotExist) {
		repository.Data, err = nil, nil
	}
	if err != nil {
		return repository, Document{}, readError(repository.Path, err)
	}
	if central == "" {
		return repository, Document{}, nil
	}
	centralDocument = Document{Path: central}
	centralDocument.Data, err = readNamed(ctx, central)
	if err != nil {
		return repository, centralDocument, readError(central, err)
	}
	return repository, centralDocument, nil
}

// readNamed reads the document at a path the invocation names, relative paths
// included, which [atomicfile.ReadBounded] refuses.
func readNamed(ctx context.Context, path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return atomicfile.ReadBoundedFile(ctx, file, vocab.DocumentMaxBytes)
}

// readError is the error reading the configuration at path failed with.
func readError(path string, err error) error {
	if errors.Is(err, atomicfile.ErrFileTooLarge) {
		return &Error{Message: fmt.Sprintf("%s: the configuration is larger than the %d bytes one may hold", path, vocab.DocumentMaxBytes)}
	}
	return fmt.Errorf("read the configuration: %w", err)
}
