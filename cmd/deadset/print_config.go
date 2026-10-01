package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"

	"github.com/cplieger/deadset/internal/config"
	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/verdict"
)

func init() { register("print-config", runPrintConfig) }

const printConfigUsage = "usage: deadset print-config [--target=DIR] [--central=FILE] " +
	"[--languages=LIST] [--min-confidence=CLASS] [--formats=LIST] [--fail-on=SEVERITY]"

// runPrintConfig resolves the configuration an analysis of the target runs under
// and prints it, with the source of every setting. It reads no argument. A
// configuration the resolution refuses, and a central configuration that does not
// exist where the invocation names it, are usage errors.
func runPrintConfig(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("deadset print-config", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.Usage = func() { fmt.Fprintln(stderr, printConfigUsage) }
	target := set.String("target", ".", "the target root, which holds the repository configuration "+config.RepositoryFile)
	central := set.String("central", "", "the central configuration, whose settings the repository configuration overrides")
	config.RegisterFlags(set)
	if err := set.Parse(args); err != nil {
		return verdict.Usage
	}
	if set.NArg() != 0 {
		fmt.Fprintf(stderr, "deadset: print-config takes no argument, got %q\n", set.Arg(0))
		set.Usage()
		return verdict.Usage
	}

	if err := printConfig(set, *target, *central, stdout); err != nil {
		fmt.Fprintf(stderr, "deadset: %v\n", err)
		code := verdict.ForError(err)
		if errors.Is(err, fs.ErrNotExist) {
			// The file that is absent is one a flag or the repository names.
			code = verdict.Usage
		}
		if code == verdict.Usage {
			set.Usage()
		}
		return code
	}
	return verdict.Clean
}

// printConfig resolves the documents the parsed flags name and prints the result.
func printConfig(set *flag.FlagSet, target, central string, stdout io.Writer) error {
	flags, err := config.FlagSettings(set)
	if err != nil {
		return err
	}
	repository, centralDocument, err := config.ReadDocuments(target, central)
	if err != nil {
		return err
	}
	resolved, err := config.Resolve(&config.Inputs{
		ContractVersion: report.ContractVersion,
		Repository:      repository,
		Central:         centralDocument,
		Flags:           flags,
	})
	if err != nil {
		return err
	}
	return resolved.Print(stdout)
}
