package report

// Report is one report document: the envelope an analyzer writes over one
// analysis, or the envelope a merge writes over several. Every array is
// required and holds its elements in the order the schema states for it; a
// report Decode returns has every array non-nil, and Encode refuses a report
// with a nil one.
type Report struct {
	SchemaVersion   string   `json:"schema_version"`
	ContractVersion string   `json:"contract_version"`
	Analyzer        Analyzer `json:"analyzer"`

	// MergedFrom names every input report a merge read, ordered by name and
	// then by version. It is nil on a report an analyzer wrote, which is how a
	// reader tells the two kinds of report apart.
	MergedFrom []InputReport `json:"merged_from,omitzero"`

	Target Target `json:"target"`

	// Configurations is the build matrix the analysis ran, ordered by
	// identifier; it holds at least one entry.
	Configurations []Configuration `json:"configurations"`

	// ConfigurationsNotBuilt is every configuration the analysis derived from
	// the tree and dropped because it could not build it. An identifier names
	// an entry here or in Configurations, never both.
	ConfigurationsNotBuilt []ConfigurationNotBuilt `json:"configurations_not_built"`

	Consumers Consumers `json:"consumers"`

	// Findings is every reported finding. A finding an edge evaluation carries
	// is not among them, and none names an exemption class in RetainedBy.
	Findings []Finding `json:"findings"`

	// EdgeEvaluations holds one record per declared cross-language edge side an
	// analyzer enumerated. A merged report holds no record whose state is dead.
	EdgeEvaluations []EdgeEvaluation `json:"edge_evaluations"`

	StaleSuppressions []StaleSuppression `json:"stale_suppressions"`
	DeclaredGaps      []DeclaredGap      `json:"declared_gaps"`

	// ExcludedByCgo is every source file the toolchain ignored solely for
	// importing the C pseudo-package, as a target-relative path.
	ExcludedByCgo       []string             `json:"excluded_by_cgo"`
	TestFileRules       []TestFileRule       `json:"test_file_rules"`
	TypeErrorSkips      []TypeErrorSkip      `json:"type_error_skips"`
	Notes               []Note               `json:"notes"`
	UnansweredQuestions []UnansweredQuestion `json:"unanswered_questions"`
	ConventionsApplied  []ConventionApplied  `json:"conventions_applied"`
	Totals              Totals               `json:"totals"`
}

// Analyzer is the product that wrote a report: an analyzer for a report of one
// analysis, the merging product for a merged report.
//
//nolint:govet // fieldalignment: the field order is the schema's member order, which the encoding writes
type Analyzer struct {
	Name    string `json:"name"`
	Version string `json:"version"`

	// Languages is the language set the product analyzed, each "go" or "ts".
	Languages              []string    `json:"languages"`
	SchemaVersionsAccepted []string    `json:"schema_versions_accepted"`
	Conformance            Conformance `json:"conformance"`
}

// Conformance is a product's answer over the conformance corpus. A merge
// refuses an input report whose Result is not [ResultPass].
type Conformance struct {
	CorpusVersion string `json:"corpus_version"`
	Result        Result `json:"result"`

	// Digest is the sha256 of the results document the product's corpus runner
	// wrote, spelled sha256:<64 lowercase hex digits>.
	Digest string `json:"digest"`
}

// Result is a product's result over the whole conformance corpus.
type Result string

// The results a conformance block carries.
const (
	ResultPass Result = "pass"
	ResultFail Result = "fail"
)

// InputReport is one report a merge read: the analyzer that wrote it, that
// analyzer's version, and the digest of its build artifact.
type InputReport struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// Target is what a report is about. A merged report names the same target as
// every report it merged.
type Target struct {
	Kind TargetKind `json:"kind"`

	// Root is the directory the target was analyzed from, relative to the
	// directory the run was invoked from: "." or a slash-separated path with no
	// empty, "." or ".." segment.
	Root string `json:"root"`

	// Identity is the name the target publishes itself under, one name for
	// every language the target holds.
	Identity string `json:"identity"`
}

// TargetKind says whether a target's callers are all in the analyzed graph.
type TargetKind string

// The kinds of target the configuration declares.
const (
	TargetApplication TargetKind = "application"
	TargetLibrary     TargetKind = "library"
)

// Consumers is the consumer set an analysis loaded beside the target.
//
//nolint:govet // fieldalignment: the field order is the schema's member order, which the encoding writes
type Consumers struct {
	Declared    int                   `json:"declared"`
	Loaded      []LoadedConsumer      `json:"loaded"`
	Unavailable []UnavailableConsumer `json:"unavailable"`
}

// LoadedConsumer is one declared consumer the analysis loaded. Role is
// "consumer", the one role the schema admits.
type LoadedConsumer struct {
	ID   string `json:"id"`
	Role string `json:"role"`
	Path string `json:"path"`
}

// UnavailableConsumer is one declared consumer the analysis did not load, with
// the reason. Role is "consumer", the one role the schema admits.
type UnavailableConsumer struct {
	ID     string `json:"id"`
	Role   string `json:"role"`
	Reason string `json:"reason"`
}

// EdgeEvaluation is one analyzer's verdict on one side of a declared
// cross-language edge. Finding is the pending finding: present exactly when
// State is [StateDead], and present nowhere else in the report.
type EdgeEvaluation struct {
	Edge    string   `json:"edge"`
	Side    Side     `json:"side"`
	Symbol  string   `json:"symbol"`
	State   State    `json:"state"`
	Finding *Finding `json:"finding,omitzero"`

	// Analyzer names the analyzer whose report carried the record, on a merged
	// report; it is empty on an analyzer's own report.
	Analyzer string `json:"analyzer,omitzero"`
}

// Side names one side of a declared edge: the symbol that provides, or the
// symbol that uses it.
type Side string

// The two sides of an edge.
const (
	SideProvides Side = "provides"
	SideUsedBy   Side = "used_by"
)

// State is an analyzer's verdict on the symbol one side of an edge names.
type State string

// The states an evaluation reports: the symbol is live, dead with a pending
// finding, or not enumerated by the analyzer at all.
const (
	StateLive   State = "live"
	StateDead   State = "dead"
	StateAbsent State = "absent"
)

// StaleSuppression is one suppression that matches no current finding, a
// record of its own rather than a row of the finding list. Code is always
// "DS1703", and Mechanism is "inline", "ignore" or "baseline".
//
//nolint:govet // fieldalignment: the field order is the schema's member order, which the encoding writes
type StaleSuppression struct {
	Code      string              `json:"code"`
	Mechanism string              `json:"mechanism"`
	Entry     SuppressionEntry    `json:"entry"`
	Position  SuppressionPosition `json:"position"`
	Symbol    string              `json:"symbol"`
	Message   string              `json:"message"`

	// Analyzer names the analyzer whose report carried the record, on a merged
	// report; it is empty on an analyzer's own report.
	Analyzer string `json:"analyzer,omitzero"`
}

// SuppressionEntry is the suppression record a stale suppression reports, in
// the shape an ignore entry and a baseline row share. Symbol is nil where the
// record names no symbol.
type SuppressionEntry struct {
	Code   string  `json:"code"`
	Symbol *string `json:"symbol,omitzero"`
	Path   string  `json:"path"`
	Reason string  `json:"reason"`
}

// SuppressionPosition is a suppression record's own site. A record spans no
// range, so it carries no end line.
type SuppressionPosition struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// DeclaredGap is one capability a product declines, for one corpus fixture and
// optionally one expectation of it, with the reason.
type DeclaredGap struct {
	Fixture    string `json:"fixture"`
	Symbol     string `json:"symbol,omitzero"`
	Capability string `json:"capability"`
	Reason     string `json:"reason"`

	// Analyzer names the analyzer whose report carried the row, on a merged
	// report; it is empty on an analyzer's own report.
	Analyzer string `json:"analyzer,omitzero"`
}

// TestFileRule is one rule by which a run classified a file as a test file,
// with the number of files it matched.
type TestFileRule struct {
	Rule    string `json:"rule"`
	Matched int    `json:"matched"`
}

// TypeErrorSkip is one type error inside a function or a file-level statement
// the analysis therefore did not evaluate.
//
//nolint:govet // fieldalignment: the field order is the schema's member order, which the encoding writes
type TypeErrorSkip struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// Note is one hint the analysis has about a run's setup. It is no finding:
// nothing counts it and it fails no run.
type Note struct {
	Kind    NoteKind `json:"kind"`
	Path    string   `json:"path"`
	Key     string   `json:"key"`
	Message string   `json:"message"`
}

// NoteKind is what a note is about.
type NoteKind string

// NotePublishedPackage is a package of an application no other package of the
// target imports and outside programs may import.
const NotePublishedPackage NoteKind = "published-package"

// UnansweredQuestion counts, for one configuration, the questions the analysis
// asked its type checker that went unanswered and the declarations they held.
type UnansweredQuestion struct {
	Configuration string `json:"configuration"`
	Questions     int    `json:"questions"`
	Declarations  int    `json:"declarations"`
}

// ConventionApplied is one convention row the analysis applied: the row's
// name, the enabling package and its installed version, and the manifest that
// declares it.
type ConventionApplied struct {
	Name     string `json:"name"`
	Package  string `json:"package"`
	Version  string `json:"version"`
	Manifest string `json:"manifest"`
}

// Totals are the counts a summary line prints. A merge recomputes them over
// the merged arrays.
type Totals struct {
	Findings             int        `json:"findings"`
	BySeverity           BySeverity `json:"by_severity"`
	DeletableLines       int        `json:"deletable_lines"`
	SuppressionsInEffect int        `json:"suppressions_in_effect"`
	ReasonsRecorded      int        `json:"reasons_recorded"`
	StaleSuppressions    int        `json:"stale_suppressions"`
	Pending              int        `json:"pending"`
	Omitted              int        `json:"omitted"`
}

// BySeverity is how many findings carry each severity.
type BySeverity struct {
	Allow int `json:"allow"`
	Warn  int `json:"warn"`
	Deny  int `json:"deny"`
}
