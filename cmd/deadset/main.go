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
	"slices"
	"strings"
)

// The exit codes contract/exit-codes.json names, which every verb returns.
const (
	exitClean    = 0
	exitFindings = 1
	exitUsage    = 2
	exitFailure  = 3
	exitPending  = 4
)

// command is one verb of this command's surface and the line the usage text
// gives it.
type command struct {
	name    string
	summary string
}

// commands is every verb this command answers, in the order the usage text
// lists them. A verb with no registered handler is listed, and invoking it is a
// usage error.
var commands = []command{
	{name: "analyze", summary: "find dead code in a repository and merge every analyzer's report"},
	{name: "explain", summary: "say why one symbol is or is not reported"},
	{name: "print-config", summary: "print the resolved configuration and where each setting came from"},
	{name: "install", summary: "install the analyzers the provider list names"},
	{name: "describe", summary: "print this build's capabilities as JSON"},
	{name: "version", summary: "print the version of this build and of the contract it implements"},
}

// handler runs one verb over the arguments that follow the verb's name. It
// writes its result to stdout and its diagnostics to stderr, and returns the
// exit code.
type handler func(args []string, stdout, stderr io.Writer) int

// handlers maps a verb's name to the function that runs it. Each verb's own
// file adds its entry by calling register from an init function, so a verb is
// added by adding its file, and the map is complete before main runs.
var handlers = map[string]handler{}

// register makes run the handler of the verb named name. It panics on a name
// commands does not list and on a name registered twice: each is a mistake in
// this command's own source, and a panic at initialization fails every test of
// the package rather than shipping a verb the usage text cannot reach.
func register(name string, run handler) {
	if !slices.ContainsFunc(commands, func(c command) bool { return c.name == name }) {
		panic(fmt.Sprintf("deadset: register %q: the command list names no such verb", name))
	}
	if _, taken := handlers[name]; taken {
		panic(fmt.Sprintf("deadset: register %q: the verb has a handler already", name))
	}
	handlers[name] = run
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the command line in args and returns the process exit code.
// It writes the report to stdout and diagnostics to stderr, and never exits
// the process itself, so a test drives it directly.
func run(args []string, stdout, stderr io.Writer) int {
	return dispatch(handlers, args, stdout, stderr)
}

// dispatch runs the verb args names from verbs, the handlers registered by name.
// It takes the handlers as a parameter so a test pins the dispatch over a fixed
// set, whichever verb files the build holds.
func dispatch(verbs map[string]handler, args []string, stdout, stderr io.Writer) int {
	if flagName, ok := requestsFix(args); ok {
		fmt.Fprintf(stderr, "deadset: %s requested a source edit; deadset is report-only and never edits a source file\n", flagName)
		return exitUsage
	}

	fs := flag.NewFlagSet("deadset", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage(verbs)) }
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	name := fs.Arg(0)
	if name == "" {
		fs.Usage()
		return exitUsage
	}
	verb, ok := verbs[name]
	if !ok {
		fmt.Fprintf(stderr, "deadset: unknown command %q\n", name)
		fs.Usage()
		return exitUsage
	}
	return verb(fs.Args()[1:], stdout, stderr)
}

// usage is the usage text: every verb of commands with its summary, and the
// verbs verbs holds a handler for while any verb is still without one.
func usage(verbs map[string]handler) string {
	var text strings.Builder
	text.WriteString("usage: deadset <command> [arguments]\n\ncommands:\n")
	var implemented []string
	for _, c := range commands {
		fmt.Fprintf(&text, "  %-13s %s\n", c.name, c.summary)
		if _, ok := verbs[c.name]; ok {
			implemented = append(implemented, c.name)
		}
	}
	switch n := len(implemented); {
	case n == 0, n == len(commands):
	case n == 1:
		fmt.Fprintf(&text, "\nOnly %s is implemented in this build.\n", implemented[0])
	default:
		fmt.Fprintf(&text, "\nOnly %s and %s are implemented in this build.\n",
			strings.Join(implemented[:n-1], ", "), implemented[n-1])
	}
	return text.String()
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
