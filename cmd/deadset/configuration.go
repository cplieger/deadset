package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cplieger/deadset/internal/config"
	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/verdict"
)

// targetUsage and centralUsage describe the two flags every verb that reads a
// configuration takes.
const (
	targetUsage  = "the target root, which holds the repository configuration " + config.RepositoryFile
	centralUsage = "the central configuration, whose settings the repository configuration overrides"
)

// resolveConfiguration resolves the configuration a verb runs under over the
// target root target, the central configuration central and the setting flags
// set parsed, and returns it with the absolute path of the target root.
//
// Each document is read once and its absence answered by who named it: a
// target that is not a directory is a failure of the run, a central
// configuration the flag names that does not exist is an invocation the run
// refuses, and an absent repository configuration is no configuration.
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
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", &verdict.InvocationError{Err: err, Flag: "central"}
	}
	if err != nil {
		return nil, "", err
	}
	resolved, err := config.Resolve(&config.Inputs{
		ContractVersion: report.ContractVersion,
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
