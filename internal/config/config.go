// Package config resolves the deadset configuration for the orchestrator. It
// reads the closed key list of the Contract's generated setting table from the
// command line, the repository configuration and the central configuration,
// applies them in that rank over each key's default, records the source of
// every resolved setting, and splits the result into the document each
// analyzer receives, holding the keys the table delivers to its languages.
//
// Every document is checked whole against the Contract's configuration
// schema before any analyzer runs, every language's section included: the
// orchestrator is the one product that reads the whole document, so a fault
// in any key ends the run here, with the usage code. The orchestrator reads
// the settings of [config] by their keys.
package config

import "github.com/cplieger/deadset-spec/v8/vocab"

// config is the part of the resolved configuration the orchestrator reads.
type config struct {
	// Languages is analysis.languages: the languages in scope, or none when the
	// orchestrator detects them from the target tree.
	Languages []string

	// Providers is providers.analyzers: every analyzer the run may invoke, in the
	// list's order. A run invokes those of them that claim a language in scope.
	Providers []Provider

	Reporters Reporters
}

// Reporters is the reporters settings the orchestrator applies to the merged
// report: its renderings, the failing severity, the most findings it keeps,
// and whether reporters.sort puts the largest deletion first.
type Reporters struct {
	FailOn      vocab.Severity
	Formats     []vocab.Format
	MaxFindings int
	BySize      bool
}

// sortBySize is the reporters.sort value that puts the largest deletion first.
const sortBySize = "size"

// Error is a configuration the orchestrator refuses: a document the
// configuration schema refuses, a resolution no source supplied a required
// setting for, or a provider list in which no entry claims a language in
// scope. Every refusal exits with the usage code.
type Error struct {
	Message string
}

// Error returns the message, which names the key and the source that carried it.
func (e *Error) Error() string { return e.Message }
