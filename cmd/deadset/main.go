// Command deadset is the front door of the deadset dead-code toolkit. It
// detects the languages in a repository, runs each language's analyzer as a
// separate process, resolves the cross-language edges between their reports,
// merges the reports into one and returns one exit code. It contains no
// language analysis of its own and never edits a source file.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// Version is the version of this build of the command.
const Version = "0.1.0-dev"

// ContractVersion is the version of the deadset contract this build implements.
const ContractVersion = "0.1.0"

const (
	exitOK    = 0
	exitUsage = 2
)

const usageText = `usage: deadset <command> [arguments]

commands:
  analyze       find dead code in a repository and merge every analyzer's report
  explain       say why one symbol is or is not reported
  print-config  print the resolved configuration and where each setting came from
  install       install the analyzers the provider list names
  describe      print this build's capabilities as JSON
  version       print the version of this build and of the contract it implements

Only version is implemented in this build.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the command line in args and returns the process exit code.
// It writes the report to stdout and diagnostics to stderr, and never exits
// the process itself, so a test drives it directly.
func run(args []string, stdout, stderr io.Writer) int {
	if flagName, ok := requestsFix(args); ok {
		fmt.Fprintf(stderr, "deadset: %s requested a source edit; deadset is report-only and never edits a source file\n", flagName)
		return exitUsage
	}

	fs := flag.NewFlagSet("deadset", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usageText) }
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	switch fs.Arg(0) {
	case "version":
		fmt.Fprintf(stdout, "deadset %s\ncontract %s\n", Version, ContractVersion)
		return exitOK
	case "":
		fs.Usage()
		return exitUsage
	default:
		fmt.Fprintf(stderr, "deadset: unknown command %q\n", fs.Arg(0))
		fs.Usage()
		return exitUsage
	}
}

// requestsFix reports whether args carries a fix flag in any spelling
// (-fix, --fix, -fix=value, --fix=value), and returns the spelling found.
func requestsFix(args []string) (string, bool) {
	for _, arg := range args {
		name := strings.TrimLeft(arg, "-")
		if len(name) == len(arg) {
			continue
		}
		if name == "fix" || strings.HasPrefix(name, "fix=") {
			return arg, true
		}
	}
	return "", false
}
