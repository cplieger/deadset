// Package providers chooses the analyzers a run invokes: the entries of the
// provider list that claim a language in scope, each with the executable its
// command resolves to. A command is resolved on the local filesystem alone and
// is never fetched, so an entry whose command resolves to nothing is refused
// rather than acquired.
package providers

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/deadset/internal/config"
)

// listKey is the configuration setting that holds the provider list.
const listKey = "providers.analyzers"

// ErrRelativeCommand reports a command that is a path but not an absolute one,
// which is neither of the two forms a command takes.
var ErrRelativeCommand = errors.New("a command is a name looked up on PATH or an absolute path")

// Analyzer is one provider entry a run invokes.
type Analyzer struct {
	// Executable is the file the entry's command resolves to, which the run
	// executes in place of the command as written.
	Executable string

	Entry config.Provider
}

// CommandError is a provider entry the run invokes whose command resolves to
// no executable file.
type CommandError struct {
	// Err is why: an [*exec.Error] from the lookup, or [ErrRelativeCommand].
	Err error

	Name  string // the entry's analyzer name
	Index int    // the entry's place in the provider list
}

// Error names the analyzer and the provider entry.
func (e *CommandError) Error() string {
	return fmt.Sprintf("analyzer %s, provider entry %s[%d]: %v", e.Name, listKey, e.Index, e.Err)
}

// Unwrap is why the command resolves to nothing.
func (e *CommandError) Unwrap() error { return e.Err }

// Select returns the analyzers a run over the languages in scope invokes, in
// the order of entries: every entry claiming at least one of them, two entries
// claiming one language included, and no other.
//
// A language in scope that no entry claims is a [*config.Error] naming every
// such language, reported before any command is resolved. Otherwise an entry
// whose command resolves to no executable is a [*CommandError], one joined
// per such entry in the order of entries.
func Select(entries []config.Provider, inScope []string) ([]Analyzer, error) {
	var unclaimed []string
	for _, language := range inScope {
		claimed := slices.ContainsFunc(entries, func(e config.Provider) bool { return slices.Contains(e.Languages, language) })
		if !claimed {
			unclaimed = append(unclaimed, language)
		}
	}
	if len(unclaimed) > 0 {
		return nil, unclaimedError(unclaimed)
	}

	var (
		selected []Analyzer
		failures []error
	)
	for i := range entries {
		e := &entries[i]
		if !slices.ContainsFunc(e.Languages, func(language string) bool { return slices.Contains(inScope, language) }) {
			continue
		}
		executable, err := resolve(e.Command)
		if err != nil {
			failures = append(failures, &CommandError{Err: err, Name: e.Name, Index: i})
			continue
		}
		selected = append(selected, Analyzer{Executable: executable, Entry: *e})
	}
	if len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	return selected, nil
}

// resolve returns the executable file command names: a command holding a
// separator is a path, taken as it is when it is absolute, and any other
// command is a name looked up on PATH.
func resolve(command string) (string, error) {
	if strings.ContainsRune(command, '/') || strings.ContainsRune(command, filepath.Separator) {
		if !filepath.IsAbs(command) {
			return "", fmt.Errorf("%w: %q is a relative path", ErrRelativeCommand, command)
		}
	}
	return exec.LookPath(command)
}

// unclaimedError refuses a provider list that leaves languages in scope with
// no analyzer.
func unclaimedError(languages []string) *config.Error {
	quoted := make([]string, len(languages))
	for i, language := range languages {
		quoted[i] = strconv.Quote(language)
	}
	return &config.Error{
		Key: listKey,
		Message: fmt.Sprintf("%s holds no entry claiming %s, in scope for this run: "+
			"add an entry claiming each, or leave them out of scope", listKey, strings.Join(quoted, ", ")),
	}
}
