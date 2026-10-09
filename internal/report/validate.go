package report

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
)

// The patterns contract/report.schema.json and contract/finding.schema.json
// give their string members, each named for what it spells. A test pins every
// pattern location in the two schemas to one of these.
var (
	semverPattern     = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	versionPattern    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	tokenPattern      = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	codePattern       = regexp.MustCompile(`^DS\d{4}$`)
	edgePattern       = regexp.MustCompile(`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`)
	oneLinePattern    = regexp.MustCompile(`^[^\r\n]+$`)
	messagePattern    = regexp.MustCompile(`^[^\r\n]*[^.\r\n]$`)
	nonBlankPattern   = regexp.MustCompile(`[^ \t\r\n]`)
	componentPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]*/c-\d+$`)
	gapSymbolPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
	capabilityPattern = regexp.MustCompile(`^(DS\d{4}|[a-z][a-z0-9]*(-[a-z0-9]+)*)$`)
	artifactPattern   = regexp.MustCompile(`^(\.?[A-Za-z0-9_@-][A-Za-z0-9_@.-]*)(/\.?[A-Za-z0-9_@-][A-Za-z0-9_@.-]*)*$`)
	relativePattern   = regexp.MustCompile(`^(?:[^/\\.\r\n][^/\\\r\n]*|\.[^/\\.\r\n][^/\\\r\n]*|\.\.[^/\\\r\n]+)(?:/(?:[^/\\.\r\n][^/\\\r\n]*|\.[^/\\.\r\n][^/\\\r\n]*|\.\.[^/\\\r\n]+))*$`)
	rootPattern       = regexp.MustCompile(`^(?:\.|(?:[^/\\.\r\n][^/\\\r\n]*|\.[^/\\.\r\n][^/\\\r\n]*|\.\.[^/\\\r\n]+)(?:/(?:[^/\\.\r\n][^/\\\r\n]*|\.[^/\\.\r\n][^/\\\r\n]*|\.\.[^/\\\r\n]+))*)$`)
)

// The closed vocabularies of the two schemas. A test pins every enumeration
// and constant the schemas declare to one of these.
var (
	languages       = []string{"go", "ts"}
	results         = []Result{ResultPass, ResultFail}
	targetKinds     = []TargetKind{TargetApplication, TargetLibrary}
	consumerRoles   = []string{"consumer"}
	sides           = []Side{SideProvides, SideUsedBy}
	states          = []State{StateLive, StateDead, StateAbsent}
	staleCodes      = []string{staleSuppressionCode}
	mechanisms      = []string{"inline", "ignore", "baseline"}
	noteKinds       = []NoteKind{NotePublishedPackage}
	noteKeys        = []string{"roots.patterns"}
	classes         = []Class{ClassCertain, ClassProbable, ClassPossible}
	relations       = []Relation{RelationReferenceCounting, RelationReachability}
	fixabilities    = []Fixability{FixabilityDeletable, FixabilityNarrowable, FixabilityManual, FixabilityNone}
	severities      = []Severity{SeverityAllow, SeverityWarn, SeverityDeny}
	visibilities    = []string{"file", "package", "module"}
	dependencyKinds = []string{"require", "dependency", "dev-dependency", "peer-dependency"}
	symbolKinds     = slices.Concat([]string{
		"function", "method", "type", "class", "interface", "enum", "namespace", "variable", "constant",
		"export-alias", "field", "class-member", "type-member", "interface-method", "enum-member",
		"satisfaction-assertion", "type-parameter",
	}, relationFreeKinds)
)

// A finding carries no liveness relation where its subject is not a
// declaration (a part of one, or an artifact the run read) and where its code
// reports a subject the analysis holds live; every other finding carries one.
var (
	relationFreeKinds = []string{
		"parameter", "receiver", "result", "statement", "case", "store",
		"file", "dependency", "module-directive", "suppression", "root", "configured-declaration", "edge",
	}
	relationFreeCodes = []string{"DS1101", "DS1102", "DS1104", "DS1204", "DS1301"}
)

// staleSuppressionCode is the one code a stale-suppression record carries.
const staleSuppressionCode = "DS1703"

// suppressionCodes are the codes of a finding about a suppression record,
// which carries the record and where it lives.
var suppressionCodes = []string{"DS1701", "DS1702", staleSuppressionCode}

// detailBranch is one rule of the finding schema that ties a details member to
// a set of codes: the member is required under those codes and forbidden under
// every other.
type detailBranch struct {
	present func(*Details) bool
	member  string
	codes   []string
}

// detailBranches are the finding schema's per-code details rules, apart from
// excluded_by, whose rule also reads the language, and removes_last_use_of,
// whose rule reads the fixability.
var detailBranches = []detailBranch{
	{
		member: "narrower_visibility", codes: []string{"DS1101", "DS1102", "DS1104"},
		present: func(d *Details) bool { return d.NarrowerVisibility != "" },
	},
	{
		member: "implementations", codes: []string{"DS1201", "DS1203", "DS1204"},
		present: func(d *Details) bool { return d.Implementations != nil },
	},
	{
		member: "write_positions", codes: []string{"DS1301", "DS1807"},
		present: func(d *Details) bool { return d.WritePositions != nil },
	},
	{
		member: "dependency_class", codes: []string{"DS1601"},
		present: func(d *Details) bool { return d.DependencyClass != "" },
	},
	{
		member: "replacement", codes: []string{"DS1605"},
		present: func(d *Details) bool { return d.Replacement != "" },
	},
	{
		member: "mechanism", codes: suppressionCodes,
		present: func(d *Details) bool { return d.Mechanism != "" },
	},
	{
		member: "entry", codes: suppressionCodes,
		present: func(d *Details) bool { return d.Entry != nil },
	},
	{
		member: "edge", codes: []string{"DS1705"},
		present: func(d *Details) bool { return d.Edge != "" },
	},
	{
		member: "sides", codes: []string{"DS1705"},
		present: func(d *Details) bool { return d.Sides != nil },
	},
	{
		member: "overlap", codes: []string{"DS1801", "DS1802", "DS1803", "DS1805", "DS1807", "DS1809"},
		present: func(d *Details) bool { return d.Overlap != nil },
	},
}

// excludedByCode is the code whose details carry excluded_by, on a Go finding
// alone.
const excludedByCode = "DS1501"

// validate holds a report to every rule of the report and finding schemas
// that a decode does not already enforce by reading the document, and to the
// presence of every required array, which a report built in memory can lack.
func (r *Report) validate() error {
	return first(
		matches("schema_version", r.SchemaVersion, semverPattern),
		matches("contract_version", r.ContractVersion, semverPattern),
		at("analyzer", r.Analyzer.validate()),
		listOf("merged_from", r.MergedFrom, true, (*InputReport).validate),
		at("target", r.Target.validate()),
		nonEmptyList("configurations", r.Configurations),
		listOf("configurations", r.Configurations, true, (*Configuration).validate),
		required("configurations_not_built", r.ConfigurationsNotBuilt),
		listOf("configurations_not_built", r.ConfigurationsNotBuilt, true, (*ConfigurationNotBuilt).validate),
		at("consumers", r.Consumers.validate()),
		required("findings", r.Findings),
		listOf("findings", r.Findings, false, (*Finding).validateReported),
		required("edge_evaluations", r.EdgeEvaluations),
		listOf("edge_evaluations", r.EdgeEvaluations, false, (*EdgeEvaluation).validate),
		required("stale_suppressions", r.StaleSuppressions),
		listOf("stale_suppressions", r.StaleSuppressions, false, (*StaleSuppression).validate),
		required("declared_gaps", r.DeclaredGaps),
		listOf("declared_gaps", r.DeclaredGaps, false, (*DeclaredGap).validate),
		required("excluded_by_cgo", r.ExcludedByCgo),
		matchesEach("excluded_by_cgo", r.ExcludedByCgo, true, artifactPattern),
		required("test_file_rules", r.TestFileRules),
		listOf("test_file_rules", r.TestFileRules, true, (*TestFileRule).validate),
		required("type_error_skips", r.TypeErrorSkips),
		listOf("type_error_skips", r.TypeErrorSkips, true, (*TypeErrorSkip).validate),
		required("notes", r.Notes),
		listOf("notes", r.Notes, true, (*Note).validate),
		required("unanswered_questions", r.UnansweredQuestions),
		listOf("unanswered_questions", r.UnansweredQuestions, true, (*UnansweredQuestion).validate),
		required("conventions_applied", r.ConventionsApplied),
		listOf("conventions_applied", r.ConventionsApplied, true, (*ConventionApplied).validate),
		at("totals", r.Totals.validate()),
	)
}

func (a *Analyzer) validate() error {
	return first(
		matches("name", a.Name, tokenPattern),
		matches("version", a.Version, versionPattern),
		nonEmptyList("languages", a.Languages),
		listOf("languages", a.Languages, true, func(language *string) error { return within("", *language, languages) }),
		nonEmptyList("schema_versions_accepted", a.SchemaVersionsAccepted),
		matchesEach("schema_versions_accepted", a.SchemaVersionsAccepted, true, semverPattern),
		at("conformance", first(
			matches("corpus_version", a.Conformance.CorpusVersion, semverPattern),
			within("result", a.Conformance.Result, results),
			matches("digest", a.Conformance.Digest, digestPattern),
		)),
	)
}

func (m *InputReport) validate() error {
	return first(
		matches("name", m.Name, tokenPattern),
		matches("version", m.Version, versionPattern),
		matches("digest", m.Digest, digestPattern),
	)
}

func (t *Target) validate() error {
	return first(
		within("kind", t.Kind, targetKinds),
		matches("root", t.Root, rootPattern),
		nonEmpty("identity", t.Identity),
	)
}

func (c *Configuration) validate() error {
	if (c.Platform != nil) == (c.Project != "") {
		return fmt.Errorf("%w: exactly one of a platform and a project is required", errShape)
	}
	if c.Project != "" {
		return first(nonEmpty("id", c.ID), matches("project", c.Project, relativePattern))
	}
	return first(
		nonEmpty("id", c.ID),
		nonEmpty("os", c.Platform.OS),
		nonEmpty("arch", c.Platform.Arch),
		required("tags", c.Platform.Tags),
		listOf("tags", c.Platform.Tags, true, func(tag *string) error { return nonEmpty("", *tag) }),
	)
}

func (c *ConfigurationNotBuilt) validate() error {
	return first(c.Configuration.validate(), matches("error", c.Error, oneLinePattern))
}

func (c *Consumers) validate() error {
	return first(
		atLeast("declared", c.Declared, 0),
		required("loaded", c.Loaded),
		listOf("loaded", c.Loaded, true, func(one *LoadedConsumer) error {
			return first(nonEmpty("id", one.ID), within("role", one.Role, consumerRoles), nonEmpty("path", one.Path))
		}),
		required("unavailable", c.Unavailable),
		listOf("unavailable", c.Unavailable, true, func(one *UnavailableConsumer) error {
			return first(
				nonEmpty("id", one.ID),
				within("role", one.Role, consumerRoles),
				matches("reason", one.Reason, nonBlankPattern),
			)
		}),
	)
}

func (e *EdgeEvaluation) validate() error {
	var pending error
	switch {
	case e.State == StateDead && e.Finding == nil:
		pending = at("finding", fmt.Errorf("%w: a dead evaluation carries its pending finding", errMissing))
	case e.State != StateDead && e.Finding != nil:
		pending = at("finding", fmt.Errorf("%w: only a dead evaluation carries a pending finding", errForbidden))
	case e.Finding != nil:
		pending = at("finding", e.Finding.validate())
	}
	return first(
		matches("edge", e.Edge, edgePattern),
		within("side", e.Side, sides),
		within("state", e.State, states),
		pending,
		optionalMatch("analyzer", e.Analyzer, tokenPattern),
	)
}

func (s *StaleSuppression) validate() error {
	return first(
		within("code", s.Code, staleCodes),
		within("mechanism", s.Mechanism, mechanisms),
		at("entry", first(
			matches("code", s.Entry.Code, codePattern),
			matches("path", s.Entry.Path, artifactPattern),
			matches("reason", s.Entry.Reason, nonBlankPattern),
		)),
		at("position", first(
			matches("path", s.Position.Path, artifactPattern),
			atLeast("line", s.Position.Line, 1),
			atLeast("column", s.Position.Column, 1),
		)),
		matches("message", s.Message, nonBlankPattern),
		optionalMatch("analyzer", s.Analyzer, tokenPattern),
	)
}

func (g *DeclaredGap) validate() error {
	return first(
		matches("fixture", g.Fixture, tokenPattern),
		optionalMatch("symbol", g.Symbol, gapSymbolPattern),
		matches("capability", g.Capability, capabilityPattern),
		matches("reason", g.Reason, nonBlankPattern),
		optionalMatch("analyzer", g.Analyzer, tokenPattern),
	)
}

func (t *TestFileRule) validate() error {
	return first(matches("rule", t.Rule, tokenPattern), atLeast("matched", t.Matched, 0))
}

func (s *TypeErrorSkip) validate() error {
	return first(
		matches("path", s.Path, artifactPattern),
		atLeast("line", s.Line, 1),
		matches("message", s.Message, oneLinePattern),
	)
}

func (n *Note) validate() error {
	return first(
		within("kind", n.Kind, noteKinds),
		matches("path", n.Path, artifactPattern),
		within("key", n.Key, noteKeys),
		matches("message", n.Message, oneLinePattern),
	)
}

func (q *UnansweredQuestion) validate() error {
	return first(
		nonEmpty("configuration", q.Configuration),
		atLeast("questions", q.Questions, 1),
		atLeast("declarations", q.Declarations, 0),
	)
}

func (c *ConventionApplied) validate() error {
	return first(
		matches("name", c.Name, tokenPattern),
		nonEmpty("package", c.Package),
		nonEmpty("version", c.Version),
		matches("manifest", c.Manifest, artifactPattern),
	)
}

func (t *Totals) validate() error {
	return first(
		atLeast("findings", t.Findings, 0),
		at("by_severity", first(
			atLeast("allow", t.BySeverity.Allow, 0),
			atLeast("warn", t.BySeverity.Warn, 0),
			atLeast("deny", t.BySeverity.Deny, 0),
		)),
		atLeast("deletable_lines", t.DeletableLines, 0),
		atLeast("suppressions_in_effect", t.SuppressionsInEffect, 0),
		atLeast("reasons_recorded", t.ReasonsRecorded, 0),
		atLeast("stale_suppressions", t.StaleSuppressions, 0),
		atLeast("pending", t.Pending, 0),
		atLeast("omitted", t.Omitted, 0),
		at("withheld", first(
			atLeast("certain", t.Withheld.Certain, 0),
			atMost("certain", t.Withheld.Certain, 0),
			atLeast("probable", t.Withheld.Probable, 0),
			atLeast("possible", t.Withheld.Possible, 0),
		)),
	)
}

// validateReported holds a finding of a report's finding list to the finding
// schema and to the one rule the report schema adds there: a reported finding
// names no exemption class.
func (f *Finding) validateReported() error {
	if len(f.RetainedBy) > 0 {
		return at("retained_by", fmt.Errorf("%w: a reported finding names no exemption class, and this one names %q", errValue, f.RetainedBy))
	}
	return f.validate()
}

// validate holds a finding to the finding schema.
func (f *Finding) validate() error {
	return first(
		matches("code", f.Code, codePattern),
		matches("kind", f.Kind, tokenPattern),
		within("language", f.Language, languages),
		at("position", f.Position.validate()),
		at("symbol", f.Symbol.validate()),
		within("reachability_class", f.ReachabilityClass, classes),
		within("confidence", f.Confidence, classes),
		f.checkRelation(),
		at("component", f.Component.validate()),
		required("retained_by", f.RetainedBy),
		matchesEach("retained_by", f.RetainedBy, true, tokenPattern),
		required("configurations", f.Configurations),
		listOf("configurations", f.Configurations, true, func(id *string) error { return nonEmpty("", *id) }),
		required("consumers_loaded", f.ConsumersLoaded),
		listOf("consumers_loaded", f.ConsumersLoaded, true, func(id *string) error { return nonEmpty("", *id) }),
		within("fixability", f.Fixability, fixabilities),
		within("severity", f.Severity, severities),
		matches("message", f.Message, messagePattern),
		optionalMatch("analyzer", f.Analyzer, tokenPattern),
		at("details", f.Details.validate()),
		at("details", f.checkDetails()),
	)
}

// checkRelation holds the liveness relation to the subject: absent on a
// subject that is not a declaration and on a code whose subject the analysis
// holds live, one of the two relations on every other finding.
func (f *Finding) checkRelation() error {
	absent := slices.Contains(relationFreeKinds, f.Symbol.Kind) || slices.Contains(relationFreeCodes, f.Code)
	switch {
	case absent && f.LivenessRelation != "":
		return at("liveness_relation", fmt.Errorf("%w: no relation decides a subject of kind %s under %s", errForbidden, f.Symbol.Kind, f.Code))
	case !absent && f.LivenessRelation == "":
		return at("liveness_relation", fmt.Errorf("%w: a subject of kind %s under %s carries the relation that decided it", errMissing, f.Symbol.Kind, f.Code))
	case absent:
		return nil
	}
	return within("liveness_relation", f.LivenessRelation, relations)
}

// checkDetails holds the details members to the finding's code, its language
// and its fixability.
func (f *Finding) checkDetails() error {
	for _, branch := range detailBranches {
		if err := carried(branch.member, branch.present(&f.Details), slices.Contains(branch.codes, f.Code), f.Code); err != nil {
			return err
		}
	}
	excludedBy := f.Code == excludedByCode && f.Language == "go"
	if err := carried("excluded_by", f.Details.ExcludedBy != "", excludedBy, f.Code); err != nil {
		return err
	}
	if f.Details.RemovesLastUseOf != nil && f.Fixability != FixabilityDeletable {
		return at("removes_last_use_of", fmt.Errorf("%w: only a deletable finding removes a dependency's last use, and this one is %s", errForbidden, f.Fixability))
	}
	return nil
}

// carried holds one details member to the rule that says whether the finding
// carries it.
func carried(member string, present, carries bool, code string) error {
	switch {
	case carries && !present:
		return at(member, fmt.Errorf("%w: %s carries %s", errMissing, code, member))
	case !carries && present:
		return at(member, fmt.Errorf("%w: %s carries no %s", errForbidden, code, member))
	}
	return nil
}

func (p *Position) validate() error {
	return first(
		matches("path", p.Path, relativePattern),
		atLeast("line", p.Line, 1),
		atLeast("column", p.Column, 1),
		atLeast("end_line", p.EndLine, 1),
	)
}

func (s *Symbol) validate() error {
	return first(
		nonEmpty("ref", s.Ref),
		within("kind", s.Kind, symbolKinds),
		matches("name", s.Name, oneLinePattern),
		atLeast("size_lines", s.SizeLines, 1),
	)
}

func (c *Component) validate() error {
	return first(
		matches("id", c.ID, componentPattern),
		atLeast("symbol_count", c.SymbolCount, 1),
		atLeast("deletable_lines", c.DeletableLines, 0),
		listOf("members", c.Members, true, (*Positioned).validate),
	)
}

func (p *Positioned) validate() error {
	return first(nonEmpty("ref", p.Ref), matches("name", p.Name, oneLinePattern), at("position", p.Position.validate()))
}

func (d *Details) validate() error {
	nonEmptyItem := func(item *string) error { return nonEmpty("", *item) }
	return first(
		optionalWithin("narrower_visibility", d.NarrowerVisibility, visibilities),
		listOf("implementations", d.Implementations, true, (*Positioned).validate),
		optionalList("write_positions", d.WritePositions, (*Position).validate),
		optionalWithin("dependency_class", d.DependencyClass, dependencyKinds),
		optionalWithin("mechanism", d.Mechanism, mechanisms),
		at("entry", d.Entry.validate()),
		optionalList("overlap", d.Overlap, nonEmptyItem),
		optionalMatch("edge", d.Edge, edgePattern),
		optionalList("sides", d.Sides, func(side *EdgeSide) error {
			return first(within("side", side.Side, sides), nonEmpty("symbol", side.Symbol), within("state", side.State, states))
		}),
		optionalList("removes_last_use_of", d.RemovesLastUseOf, nonEmptyItem),
	)
}

func (e *Entry) validate() error {
	if e == nil {
		return nil
	}
	return first(
		matches("code", e.Code, codePattern),
		optionalMatch("path", e.Path, relativePattern),
		optionalMatch("reason", e.Reason, nonBlankPattern),
	)
}

// first is the first of errs that is not nil.
func first(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// matches holds a string member to a pattern. An empty member name holds the
// value itself, for an array element.
func matches(member, value string, pattern *regexp.Regexp) error {
	if pattern.MatchString(value) {
		return nil
	}
	return located(member, fmt.Errorf("%w: %q does not match %s", errValue, value, pattern))
}

// optionalMatch holds an optional string member to a pattern where it is
// present.
func optionalMatch(member, value string, pattern *regexp.Regexp) error {
	if value == "" {
		return nil
	}
	return matches(member, value, pattern)
}

// nonEmpty holds a string member to a minimum length of one.
func nonEmpty(member, value string) error {
	if value != "" {
		return nil
	}
	return located(member, fmt.Errorf("%w: the empty string", errValue))
}

// within holds a member to a closed vocabulary.
func within[T ~string](member string, value T, vocabulary []T) error {
	if slices.Contains(vocabulary, value) {
		return nil
	}
	return located(member, fmt.Errorf("%w: %q is not one of %q", errValue, value, vocabulary))
}

// optionalWithin holds an optional member to a closed vocabulary where it is
// present.
func optionalWithin[T ~string](member string, value T, vocabulary []T) error {
	if value == "" {
		return nil
	}
	return within(member, value, vocabulary)
}

// atLeast holds an integer member to a minimum.
func atLeast(member string, value, minimum int) error {
	if value >= minimum {
		return nil
	}
	return located(member, fmt.Errorf("%w: %d is below the minimum %d", errValue, value, minimum))
}

// atMost holds an integer member to a maximum.
func atMost(member string, value, maximum int) error {
	if value <= maximum {
		return nil
	}
	return located(member, fmt.Errorf("%w: %d is above the maximum %d", errValue, value, maximum))
}

// required holds a required array to being present, which a nil slice is not.
func required[T any](member string, items []T) error {
	if items != nil {
		return nil
	}
	return at(member, errNull)
}

// nonEmptyList holds a required array to holding at least one element.
func nonEmptyList[T any](member string, items []T) error {
	if len(items) > 0 {
		return nil
	}
	return at(member, fmt.Errorf("%w: the array is empty", errValue))
}

// optionalList holds an optional array to holding at least one distinct
// element where it is present, and each element to check.
func optionalList[T any](member string, items []T, check func(*T) error) error {
	if items == nil {
		return nil
	}
	return first(nonEmptyList(member, items), listOf(member, items, true, check))
}

// matchesEach holds every element of a string array to a pattern, and the
// elements to being distinct where unique is true.
func matchesEach(member string, items []string, unique bool, pattern *regexp.Regexp) error {
	return listOf(member, items, unique, func(item *string) error { return matches("", *item, pattern) })
}

// listOf holds every element of an array to check, and the elements to being
// distinct where unique is true, placing an error at the element's index.
func listOf[T any](member string, items []T, unique bool, check func(*T) error) error {
	encoded := make(map[string]int, len(items))
	for i := range items {
		if err := check(&items[i]); err != nil {
			return at(member, at(strconv.Itoa(i), err))
		}
		if !unique {
			continue
		}
		written, err := marshal(&items[i])
		if err != nil {
			return at(member, at(strconv.Itoa(i), err))
		}
		if earlier, repeated := encoded[string(written)]; repeated {
			return at(member, at(strconv.Itoa(i), fmt.Errorf("%w: the element repeats element %d", errValue, earlier)))
		}
		encoded[string(written)] = i
	}
	return nil
}

// located places err at member, or leaves it where it is for an empty member
// name.
func located(member string, err error) error {
	if member == "" {
		return err
	}
	return at(member, err)
}
