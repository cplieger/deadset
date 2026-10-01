package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// RepositoryFile is the name of the repository configuration at the target root.
const RepositoryFile = "deadset.json"

// maxDocumentBytes bounds a configuration document. A document over it is refused
// without being read whole.
const maxDocumentBytes = 1 << 20

// Document is one configuration document: the file it was read from, which every
// provenance entry the document supplies names, and its contents. Data is nil when
// no document exists at Path.
type Document struct {
	Path string
	Data []byte
}

// ReadDocuments reads the configuration documents one invocation names: the
// repository configuration at the target root, which may be absent, and the central
// configuration at central, which is read when central is not empty and must then
// exist.
func ReadDocuments(target, central string) (repository, centralDocument Document, err error) {
	repository = Document{Path: filepath.Join(target, RepositoryFile)}
	repository.Data, err = readFile(repository.Path)
	if errors.Is(err, fs.ErrNotExist) {
		repository.Data, err = nil, nil
	}
	if err != nil || central == "" {
		return repository, Document{}, err
	}
	centralDocument = Document{Path: central}
	centralDocument.Data, err = readFile(central)
	return repository, centralDocument, err
}

// readFile reads one configuration document whole, refusing one larger than the
// bound.
func readFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read the configuration: %w", err)
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, maxDocumentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read the configuration %s: %w", path, err)
	}
	if len(data) > maxDocumentBytes {
		return nil, &Error{Message: fmt.Sprintf("%s: the configuration is larger than the %d bytes one may hold", path, maxDocumentBytes)}
	}
	return data, nil
}
