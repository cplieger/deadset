package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/cplieger/deadset/internal/config"
	"github.com/cplieger/deadset/internal/verdict"
)

func init() { register("print-config", runPrintConfig) }

const printConfigUsage = "usage: deadset print-config [--target=DIR] [--central=FILE] " +
	"[--languages=LIST] [--min-confidence=CLASS] [--formats=LIST] [--fail-on=SEVERITY]"

// runPrintConfig resolves the configuration an analysis of the target runs under
// and prints it, with the source of every setting. It reads no argument. A
// configuration the resolution refuses, and a central configuration that does not
// exist where the invocation names it, are usage errors; a target that is not a
// directory is a failure.
func runPrintConfig(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("deadset print-config", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.Usage = func() { fmt.Fprintln(stderr, printConfigUsage) }
	target := set.String("target", ".", targetUsage)
	central := set.String("central", "", centralUsage)
	config.RegisterFlags(set)
	if err := set.Parse(args); err != nil {
		return verdict.Usage
	}
	if set.NArg() != 0 {
		fmt.Fprintf(stderr, "deadset: print-config takes no argument, got %q\n", set.Arg(0))
		set.Usage()
		return verdict.Usage
	}

	resolved, _, err := resolveConfiguration(set, *target, *central)
	if err == nil {
		err = resolved.Print(stdout)
	}
	if err != nil {
		fmt.Fprintf(stderr, "deadset: %v\n", err)
		code := verdict.ForError(err)
		if code == verdict.Usage {
			set.Usage()
		}
		return code
	}
	return verdict.Clean
}
