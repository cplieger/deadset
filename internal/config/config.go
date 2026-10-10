// Package config resolves the deadset configuration for the orchestrator. It reads
// the closed key list the Contract declares from the command line, the repository
// configuration and the central configuration, applies them in that rank over each
// key's documented default, records the source of every resolved setting, and
// splits the result into the document each analyzer receives.
//
// The orchestrator reads the settings the Contract gives every product and the ones
// it gives the orchestrator, and those are the fields of [config]. A setting the
// Contract gives every analyzer is checked here, because every analyzer reads it the
// same way, and is otherwise only passed on. A key of a language's own section
// belongs to that language's analyzer: its name is part of the closed key list, and
// its value passes through unread.
package config

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

// Reporters is the reporters section: how the merged findings are rendered and
// which of them fail the run.
type Reporters struct {
	Sort        Sort
	Cascade     cascade
	FailOn      Severity
	Formats     []Format
	MaxFindings int
}

// Severity is what a finding of one issue kind does to a run: allow reports
// nothing, warn reports without failing the run, deny fails it.
type Severity string

// The severities, from the lowest to the highest.
const (
	Allow Severity = "allow"
	Warn  Severity = "warn"
	Deny  Severity = "deny"
)

// Format is one rendering of the finding list.
type Format string

// The formats.
const (
	FormatText     Format = "text"
	formatJSON     Format = "json"
	FormatGitHub   Format = "github"
	FormatSARIF    Format = "sarif"
	FormatTemplate Format = "template"
)

// Sort is the order findings are rendered in.
type Sort string

// The orders: position is the canonical order, size puts the largest deletion first.
const (
	SortPosition Sort = "position"
	SortSize     Sort = "size"
)

// cascade is how much of a dead component a rendering lists.
type cascade string

// The cascade renderings: roots lists a component's roots, full every member.
const (
	cascadeRoots cascade = "roots"
	CascadeFull  cascade = "full"
)

// Error is a configuration the orchestrator refuses: a document that is not one
// instance of the closed key list, a key it does not implement, a resolution no
// source supplied a required setting for, or a provider list in which no entry
// claims a language in scope. Every refusal exits with the usage code.
type Error struct {
	Message string
}

// Error returns the message, which names the key and the source that carried it.
func (e *Error) Error() string { return e.Message }
