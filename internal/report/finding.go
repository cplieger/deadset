package report

// Finding is what an analyzer reports about one subject at one position. Code
// names a row of contract/kinds.json, and Language is "go" or "ts".
//
//nolint:govet // fieldalignment: the field order is the schema's member order, which the encoding writes
type Finding struct {
	Code     string   `json:"code"`
	Kind     string   `json:"kind"`
	Language string   `json:"language"`
	Position Position `json:"position"`
	Symbol   Symbol   `json:"symbol"`

	ReachabilityClass Class `json:"reachability_class"`
	Confidence        Class `json:"confidence"`

	// LivenessRelation is the relation that decided the subject, empty where
	// none did: on a subject that is not a declaration (a part of one, or an
	// artifact the run read) and on the codes whose subject the analysis holds
	// live.
	LivenessRelation Relation `json:"liveness_relation,omitzero"`

	TestOnly  bool      `json:"test_only"`
	Generated bool      `json:"generated"`
	Component Component `json:"component"`

	// RetainedBy is the exemption classes that held the subject back. It is
	// empty on every finding a report's finding list holds.
	RetainedBy []string `json:"retained_by"`

	// Configurations is the identifiers of the build configurations in which
	// the finding holds.
	Configurations  []string `json:"configurations"`
	ConsumersLoaded []string `json:"consumers_loaded"`

	Fixability Fixability `json:"fixability"`
	Severity   Severity   `json:"severity"`

	// Message is one line that does not end with a full stop.
	Message string `json:"message"`

	// Analyzer names the analyzer whose report carried the finding, on a
	// merged report; it is empty on an analyzer's own report and on a finding
	// the merge itself emits.
	Analyzer string `json:"analyzer,omitzero"`

	Details Details `json:"details"`
}

// Position is a span in a source file. Path is target-relative with forward
// slashes, and Column counts UTF-16 code units from one.
type Position struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	EndLine int    `json:"end_line"`
}

// Symbol is what a finding is about: its stable reference, the kind of
// subject, its display name, and the container and span a consumer acts on.
//
//nolint:govet // fieldalignment: the field order is the schema's member order, which the encoding writes
type Symbol struct {
	Ref string `json:"ref"`

	// Kind is one value of the closed vocabulary finding.schema.json declares
	// for symbol.kind.
	Kind string `json:"kind"`
	Name string `json:"name"`

	// Parent is the reference of the subject's container, empty where it has
	// none.
	Parent string `json:"parent,omitzero"`

	// Exported is nil where the subject declares no visibility of its own.
	Exported *bool `json:"exported,omitzero"`

	SizeLines  int    `json:"size_lines"`
	Objectpath string `json:"objectpath,omitzero"`
}

// Component is the dead component a finding's subject belongs to. ID carries
// the minting analyzer's name as a prefix ("deadset-go/c-0007") and is compared
// as bytes.
//
//nolint:govet // fieldalignment: the field order is the schema's member order, which the encoding writes
type Component struct {
	ID             string `json:"id"`
	Root           bool   `json:"root"`
	SymbolCount    int    `json:"symbol_count"`
	DeletableLines int    `json:"deletable_lines"`

	// Members is the component's members where a report lists them, nil where
	// it does not.
	Members []Positioned `json:"members,omitzero"`
}

// Positioned is one symbol a finding names beside its subject: its reference,
// its display name and where it is.
type Positioned struct {
	Ref      string   `json:"ref"`
	Name     string   `json:"name"`
	Position Position `json:"position"`
}

// Details is what only some kinds carry. Each member is zero (nil for a slice
// or a pointer) where the finding does not carry it, and the finding's code
// decides which members it carries; a slice member that is present and empty
// is a non-nil empty slice.
//
//nolint:govet // fieldalignment: the field order is the schema's member order, which the encoding writes
type Details struct {
	// NarrowerVisibility is "file", "package" or "module".
	NarrowerVisibility string       `json:"narrower_visibility,omitzero"`
	Implementations    []Positioned `json:"implementations,omitzero"`
	WritePositions     []Position   `json:"write_positions,omitzero"`
	ExcludedBy         string       `json:"excluded_by,omitzero"`

	// DependencyClass is "require", "dependency", "dev-dependency" or
	// "peer-dependency".
	DependencyClass string `json:"dependency_class,omitzero"`
	Replacement     string `json:"replacement,omitzero"`

	// Mechanism is "inline", "ignore" or "baseline".
	Mechanism string `json:"mechanism,omitzero"`
	Entry     *entry `json:"entry,omitzero"`

	Overlap          []string   `json:"overlap,omitzero"`
	Edge             string     `json:"edge,omitzero"`
	Sides            []EdgeSide `json:"sides,omitzero"`
	RemovesLastUseOf []string   `json:"removes_last_use_of,omitzero"`

	// NameLiteral is the first string literal spelling the subject's name, or a
	// package-level constant's defined type's, carried only where it lowered the
	// class to possible.
	NameLiteral *Position `json:"name_literal,omitzero"`
}

// entry is the suppression record a finding about one reports. A member is
// empty exactly where the record lacks it.
type entry struct {
	Code   string `json:"code"`
	Symbol string `json:"symbol,omitzero"`
	Path   string `json:"path,omitzero"`
	Reason string `json:"reason,omitzero"`
}

// EdgeSide is one side of a stale edge an analyzer evaluated, with the symbol
// it names and the state the evaluation reported.
type EdgeSide struct {
	Side   Side   `json:"side"`
	Symbol string `json:"symbol"`
	State  State  `json:"state"`
}

// Class is a reachability class, which a finding carries twice: the class the
// analysis derived, and the confidence after the kind's ceiling caps it.
type Class string

// The reachability classes, from the most to the least certain.
const (
	ClassCertain  Class = "certain"
	ClassProbable Class = "probable"
	classPossible Class = "possible"
)

// Relation is the liveness relation that decided a subject.
type Relation string

// The liveness relations.
const (
	RelationReferenceCounting Relation = "reference-counting"
	relationReachability      Relation = "reachability"
)

// Fixability is what a maintainer does with a finding's subject.
type Fixability string

// The fixabilities a finding carries.
const (
	FixabilityDeletable  Fixability = "deletable"
	fixabilityNarrowable Fixability = "narrowable"
	fixabilityManual     Fixability = "manual"
	FixabilityNone       Fixability = "none"
)

// Severity is how a run treats a finding.
type Severity string

// The severities, from the one that never fails a run to the one that does.
const (
	SeverityAllow Severity = "allow"
	SeverityWarn  Severity = "warn"
	SeverityDeny  Severity = "deny"
)
