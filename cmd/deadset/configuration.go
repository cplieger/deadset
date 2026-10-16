package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
)

// resolveConfiguration resolves the configuration a verb runs under over the
// target root target, the central configuration central and the setting flags
// set parsed, and returns it with the absolute path of the target root.
//
// A target that is not a directory is a failure of the run; a configuration
// document that cannot be read, the central one the invocation names
// included, is a command line the run refuses; an absent repository
// configuration is no configuration.
func resolveConfiguration(set *flag.FlagSet, target, central string) (*config.Resolved, string, error) {
	root, err := targetRoot(target)
	if err != nil {
		return nil, "", err
	}
	flags, err := config.FlagSettings(set)
	if err != nil {
		return nil, "", err
	}
	repository, centralDocument, err := config.ReadDocuments(target, central)
	if err != nil {
		if unread, ok := errors.AsType[*fs.PathError](err); ok && unread.Path == central {
			return nil, "", &usageError{err: err, option: "central"}
		}
		return nil, "", &usageError{err: err}
	}
	resolved, err := config.Resolve(&config.Inputs{
		ContractVersion: vocab.ContractVersion,
		Repository:      repository,
		Central:         centralDocument,
		Flags:           flags,
	})
	if err != nil {
		return nil, "", err
	}
	return resolved, root, nil
}

// targetRoot is the absolute path of the target root target names, which must
// be a directory.
func targetRoot(target string) (string, error) {
	root, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("the target %s: %w", target, err)
	}
	info, err := os.Stat(root)
	switch {
	case err != nil:
		return "", fmt.Errorf("the target cannot be read: %w", err)
	case !info.IsDir():
		return "", fmt.Errorf("the target %s is not a directory", root)
	}
	return root, nil
}
