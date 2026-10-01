package merge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/deadset/internal/report"
)

// self is the merging product's analyzer member the tests pass.
var self = report.Analyzer{
	Name:                   "deadset",
	Version:                "1.2.3",
	Languages:              []string{"go"},
	SchemaVersionsAccepted: []string{"9.9.9"},
	Conformance:            report.Conformance{CorpusVersion: "1.0.0", Result: report.ResultPass, Digest: digest("f")},
}

// accepted is the schema versions the tests admit.
var accepted = []string{report.SchemaVersion}

func digest(hex string) string { return "sha256:" + strings.Repeat(hex, 64) }

// analyzerReport is a report one analyzer wrote over example.com/app with one
// built configuration and no record.
func analyzerReport(name, language string) *report.Report {
	return &report.Report{
		SchemaVersion:   report.SchemaVersion,
		ContractVersion: report.ContractVersion,
		Analyzer: report.Analyzer{
			Name:                   name,
			Version:                "1.0.0",
			Languages:              []string{language},
			SchemaVersionsAccepted: []string{report.SchemaVersion},
			Conformance:            report.Conformance{CorpusVersion: "1.0.0", Result: report.ResultPass, Digest: digest("a")},
		},
		Target:                 report.Target{Kind: report.TargetApplication, Root: ".", Identity: "example.com/app"},
		Configurations:         []report.Configuration{platform("linux-amd64")},
		ConfigurationsNotBuilt: []report.ConfigurationNotBuilt{},
		Consumers:              report.Consumers{Loaded: []report.LoadedConsumer{}, Unavailable: []report.UnavailableConsumer{}},
		Findings:               []report.Finding{},
		EdgeEvaluations:        []report.EdgeEvaluation{},
		StaleSuppressions:      []report.StaleSuppression{},
		DeclaredGaps:           []report.DeclaredGap{},
		ExcludedByCgo:          []string{},
		TestFileRules:          []report.TestFileRule{},
	}
}

func platform(id string, tags ...string) report.Configuration {
	return report.Configuration{ID: id, Platform: &report.Platform{OS: "linux", Arch: "amd64", Tags: append([]string{}, tags...)}}
}

// finding is an unexported function the analyzer named in component reports
// dead at path and line, rooting its own component of five lines.
func finding(component, path string, line int, name string) report.Finding {
	language := "go"
	if strings.HasSuffix(path, ".ts") {
		language = "ts"
	}
	return report.Finding{
		Code:              "DS1002",
		Kind:              "unused-unexported",
		Language:          language,
		Position:          report.Position{Path: path, Line: line, Column: 6, EndLine: line + 4},
		Symbol:            report.Symbol{Ref: language + "://example.com/app#" + name, Kind: "function", Name: name, SizeLines: 5},
		ReachabilityClass: report.ClassCertain,
		Confidence:        report.ClassCertain,
		LivenessRelation:  report.RelationReferenceCounting,
		Component:         report.Component{ID: component, Root: true, SymbolCount: 1, DeletableLines: 5},
		RetainedBy:        []string{},
		Configurations:    []string{"linux-amd64"},
		ConsumersLoaded:   []string{},
		Fixability:        report.FixabilityDeletable,
		Severity:          report.SeverityDeny,
		Message:           "unexported function has no reference in the target",
	}
}

func stale(path string, line int, symbol string) report.StaleSuppression {
	return report.StaleSuppression{
		Code:      "DS1703",
		Mechanism: "ignore",
		Entry:     report.SuppressionEntry{Code: "DS1001", Path: "main.go", Reason: "kept for the next release"},
		Position:  report.SuppressionPosition{Path: path, Line: line, Column: 5},
		Symbol:    symbol,
		Message:   "ignore entry for DS1001 matches no current finding",
	}
}

func gap(fixture string) report.DeclaredGap {
	return report.DeclaredGap{Fixture: fixture, Capability: "DS1003", Reason: "member analysis is not implemented"}
}

func evaluation(edge string, side report.Side, state report.State) report.EdgeEvaluation {
	e := report.EdgeEvaluation{Edge: edge, Side: side, Symbol: "go://example.com/app#Wire", State: state}
	if state == report.StateDead {
		pending := finding("deadset-go/c-9", "wire.go", 3, "Wire")
		e.Finding = &pending
	}
	return e
}

// inputs pairs each report with a digest derived from its analyzer's name and
// version, so one artifact keeps one digest whatever the order of the inputs.
func inputs(reports ...*report.Report) []Input {
	held := make([]Input, len(reports))
	for i, r := range reports {
		sum := sha256.Sum256([]byte(r.Analyzer.Name + " " + r.Analyzer.Version))
		held[i] = Input{Report: r, Digest: "sha256:" + hex.EncodeToString(sum[:])}
	}
	return held
}

// mustMerge merges and encodes, failing the test where either refuses.
func mustMerge(t *testing.T, in []Input) (*report.Report, []byte) {
	t.Helper()

	merged, err := Merge(in, accepted, &self)
	if err != nil {
		t.Fatalf("Merge() = %v, want a report", err)
	}
	var written bytes.Buffer
	if err := report.Encode(&written, merged); err != nil {
		t.Fatalf("report.Encode(the merged report) = %v", err)
	}
	return merged, written.Bytes()
}

// findingNames is the analyzer and symbol name of every finding, in order.
func findingNames(merged *report.Report) []string {
	names := make([]string, len(merged.Findings))
	for i := range merged.Findings {
		names[i] = merged.Findings[i].Analyzer + ":" + merged.Findings[i].Symbol.Name
	}
	return names
}

func TestMergeDoublesEveryFindingTwoAnalyzersOfOneLanguageShare(t *testing.T) {
	t.Parallel()

	first := analyzerReport("deadset-go", "go")
	first.Findings = []report.Finding{
		finding("deadset-go/c-1", "store.go", 40, "normalizeKey"),
		finding("deadset-go/c-2", "store.go", 10, "purge"),
	}
	second := analyzerReport("example-go", "go")
	second.Findings = []report.Finding{
		finding("example-go/c-1", "store.go", 10, "purge"),
		finding("example-go/c-2", "legacy.go", 9, "oldHelper"),
		finding("example-go/c-3", "store.go", 40, "normalizeKey"),
	}

	merged, forward := mustMerge(t, inputs(first, second))
	want := []string{
		"example-go:oldHelper",
		"deadset-go:purge", "example-go:purge",
		"deadset-go:normalizeKey", "example-go:normalizeKey",
	}
	if got := findingNames(merged); !slices.Equal(got, want) {
		t.Errorf("Merge(two Go analyzers) findings = %q, want %q", got, want)
	}
	if _, backward := mustMerge(t, inputs(second, first)); !bytes.Equal(forward, backward) {
		t.Errorf("Merge(second, first) =\n%s\nwant the bytes of Merge(first, second)\n%s", backward, forward)
	}
}

func TestMergeCarriesEveryRecordOfEveryInputUnderItsAnalyzer(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	golang.Findings = []report.Finding{finding("deadset-go/c-1", "store.go", 1, "a")}
	golang.StaleSuppressions = []report.StaleSuppression{stale("deadset-ignore.json", 4, "go://example.com/app#b")}
	golang.DeclaredGaps = []report.DeclaredGap{gap("private-member-unread")}
	golang.EdgeEvaluations = []report.EdgeEvaluation{evaluation("wire/Event", report.SideProvides, report.StateLive)}
	typescript := analyzerReport("deadset-ts", "ts")
	typescript.Findings = []report.Finding{finding("deadset-ts/c-1", "web/a.ts", 1, "a"), finding("deadset-ts/c-1", "web/a.ts", 1, "a")}
	typescript.StaleSuppressions = []report.StaleSuppression{stale("deadset-ignore.json", 4, "go://example.com/app#b")}
	typescript.DeclaredGaps = []report.DeclaredGap{gap("private-member-unread")}
	typescript.EdgeEvaluations = []report.EdgeEvaluation{evaluation("wire/Event", report.SideUsedBy, report.StateAbsent)}

	merged, _ := mustMerge(t, inputs(golang, typescript))
	if got := findingNames(merged); !slices.Equal(got, []string{"deadset-go:a", "deadset-ts:a", "deadset-ts:a"}) {
		t.Errorf("Merge() findings = %q, want both inputs' findings, the repeated one twice", got)
	}
	var carriers []string
	for _, s := range merged.StaleSuppressions {
		carriers = append(carriers, "stale:"+s.Analyzer)
	}
	for _, g := range merged.DeclaredGaps {
		carriers = append(carriers, "gap:"+g.Analyzer)
	}
	for _, e := range merged.EdgeEvaluations {
		carriers = append(carriers, "edge:"+e.Analyzer+":"+string(e.Side))
	}
	want := []string{
		"stale:deadset-go", "stale:deadset-ts",
		"gap:deadset-go", "gap:deadset-ts",
		"edge:deadset-go:provides", "edge:deadset-ts:used_by",
	}
	if !slices.Equal(carriers, want) {
		t.Errorf("Merge() records by carrier = %q, want %q", carriers, want)
	}
}

func TestMergeLeavesItsInputsAsTheyWere(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	golang.Findings = []report.Finding{finding("deadset-go/c-2", "z.go", 1, "z"), finding("deadset-go/c-1", "a.go", 1, "a")}
	golang.StaleSuppressions = []report.StaleSuppression{stale("deadset-ignore.json", 4, "go://example.com/app#b")}
	golang.EdgeEvaluations = []report.EdgeEvaluation{evaluation("wire/Event", report.SideProvides, report.StateDead)}
	golang.Configurations = []report.Configuration{platform("linux-arm64"), platform("linux-amd64")}
	product := self
	product.Languages = slices.Clone(self.Languages)
	before, err := json.Marshal([]any{golang, product})
	if err != nil {
		t.Fatalf("Setup: json.Marshal(the arguments) = %v", err)
	}

	if _, err := Merge(inputs(golang), accepted, &product); err != nil {
		t.Fatalf("Merge() = %v, want a report", err)
	}
	if after, err := json.Marshal([]any{golang, product}); err != nil || !bytes.Equal(after, before) {
		t.Errorf("the arguments after Merge() =\n%s\nwant them unchanged\n%s", after, before)
	}
}

// An unresolved dead evaluation stays in the merged report with its pending
// finding, which counts as pending rather than as a reported finding.
func TestMergeCountsAnUnresolvedDeadEvaluationAsPending(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	golang.EdgeEvaluations = []report.EdgeEvaluation{evaluation("wire/Event", report.SideProvides, report.StateDead)}
	golang.Totals.Pending = 1

	merged, _ := mustMerge(t, inputs(golang))
	if len(merged.EdgeEvaluations) != 1 || merged.EdgeEvaluations[0].Finding == nil {
		t.Errorf("Merge() edge evaluations = %+v, want the dead evaluation with its finding", merged.EdgeEvaluations)
	}
	if merged.Totals.Pending != 1 || merged.Totals.Findings != 0 || len(merged.Findings) != 0 {
		t.Errorf("Merge() totals = %+v with %d findings, want one pending and no finding", merged.Totals, len(merged.Findings))
	}
}

func TestMergeRefusesNoInput(t *testing.T) {
	t.Parallel()

	if merged, err := Merge(nil, accepted, &self); merged != nil || !errors.Is(err, ErrNoInput) {
		t.Errorf("Merge(nil) = %v, %v, want no report and ErrNoInput", merged, err)
	}
}

func TestMergeNamesEveryRefusedInputInMergedFromOrder(t *testing.T) {
	t.Parallel()

	old := analyzerReport("zeta-go", "go")
	old.SchemaVersion = "1.0.0"
	failing := analyzerReport("alpha-ts", "ts")
	failing.Analyzer.Conformance.Result = report.ResultFail
	passing := analyzerReport("beta-go", "go")

	var messages []string
	for _, order := range [][]*report.Report{{old, passing, failing}, {failing, old, passing}} {
		merged, err := Merge(inputs(order...), accepted, &self)
		if merged != nil || !errors.Is(err, ErrSchemaVersion) || !errors.Is(err, ErrConformance) {
			t.Fatalf("Merge(a schema refusal and a conformance refusal) = %v, %v, want no report and both refusals", merged, err)
		}
		messages = append(messages, err.Error())
	}
	want := "merge: alpha-ts has no conformance pass: its conformance result is \"fail\"\n" +
		"merge: zeta-go writes schema version 1.0.0, and the merge accepts " + report.SchemaVersion
	for _, got := range messages {
		if got != want {
			t.Errorf("Merge() error = %q, want %q whatever the input order", got, want)
		}
	}
}

func TestMergeRefusesAnUnacceptedSchemaVersionOnThatAlone(t *testing.T) {
	t.Parallel()

	old := analyzerReport("deadset-go", "go")
	old.SchemaVersion = "5.0.0"
	old.Analyzer.Conformance.Result = report.ResultFail

	_, err := Merge(inputs(old), accepted, &self)
	if !errors.Is(err, ErrSchemaVersion) || errors.Is(err, ErrConformance) {
		t.Errorf("Merge(an unaccepted version with a failed conformance) = %v, want the schema version refusal alone", err)
	}
}

func TestMergeRefusesInputsNamingDifferentTargets(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	typescript := analyzerReport("deadset-ts", "ts")
	typescript.Target.Identity = "@example/app"

	for _, order := range [][]*report.Report{{golang, typescript}, {typescript, golang}} {
		merged, err := Merge(inputs(order...), accepted, &self)
		mismatch, ok := errors.AsType[*TargetError](err)
		if merged != nil || !ok {
			t.Fatalf("Merge(two targets) = %v, %v, want no report and a *TargetError", merged, err)
		}
		if mismatch.Analyzers != [2]string{"deadset-go", "deadset-ts"} || mismatch.Targets[1].Identity != "@example/app" {
			t.Errorf("Merge(two targets) = %+v, want deadset-go's target, then deadset-ts's", mismatch)
		}
	}
}

func TestMergeNamesSelfWithTheInputsLanguagesAndTheAcceptedVersions(t *testing.T) {
	t.Parallel()

	typescript := analyzerReport("deadset-ts", "ts")
	typescript.Analyzer.Version = "2.0.0"
	golang := analyzerReport("deadset-go", "go")
	other := analyzerReport("deadset-go", "go")
	other.Analyzer.Version = "1.10.0"

	in := inputs(typescript, golang, other)
	merged, _ := mustMerge(t, in)
	want := report.Analyzer{
		Name:                   "deadset",
		Version:                "1.2.3",
		Languages:              []string{"go", "ts"},
		SchemaVersionsAccepted: accepted,
		Conformance:            self.Conformance,
	}
	if !reflect.DeepEqual(merged.Analyzer, want) {
		t.Errorf("Merge() analyzer = %+v, want %+v", merged.Analyzer, want)
	}
	wantFrom := []report.InputReport{
		{Name: "deadset-go", Version: "1.0.0", Digest: in[1].Digest},
		{Name: "deadset-go", Version: "1.10.0", Digest: in[2].Digest},
		{Name: "deadset-ts", Version: "2.0.0", Digest: in[0].Digest},
	}
	if !slices.Equal(merged.MergedFrom, wantFrom) {
		t.Errorf("Merge() merged_from = %+v, want %+v", merged.MergedFrom, wantFrom)
	}
	if merged.SchemaVersion != report.SchemaVersion || merged.ContractVersion != report.ContractVersion {
		t.Errorf("Merge() versions = %s and %s, want this module's %s and %s",
			merged.SchemaVersion, merged.ContractVersion, report.SchemaVersion, report.ContractVersion)
	}
}

func TestMergeUnionsTheEnvelopeMembersWithARepeatedEntryOnce(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	golang.Configurations = []report.Configuration{platform("linux-amd64"), platform("linux-amd64-integration", "integration")}
	golang.ConfigurationsNotBuilt = []report.ConfigurationNotBuilt{{Configuration: platform("windows-amd64"), Error: "no build"}}
	golang.Consumers.Loaded = []report.LoadedConsumer{{ID: "example.com/cli", Role: "consumer", Path: "../cli"}}
	golang.ExcludedByCgo = []string{"z_cgo.go", "a_cgo.go"}
	golang.TestFileRules = []report.TestFileRule{{Rule: "go-test-suffix", Matched: 3}}
	typescript := analyzerReport("deadset-ts", "ts")
	typescript.Configurations = []report.Configuration{{ID: "tsconfig.json", Project: "tsconfig.json"}, platform("linux-amd64", "dom")}
	typescript.ConfigurationsNotBuilt = []report.ConfigurationNotBuilt{{Configuration: platform("windows-amd64"), Error: "no build"}}
	typescript.Consumers.Loaded = []report.LoadedConsumer{{ID: "example.com/cli", Role: "consumer", Path: "../cli"}}
	typescript.Consumers.Unavailable = []report.UnavailableConsumer{{ID: "@example/web", Role: "consumer", Reason: "it declares no path"}}
	typescript.ExcludedByCgo = []string{"a_cgo.go"}
	typescript.TestFileRules = []report.TestFileRule{{Rule: "go-test-suffix", Matched: 2}, {Rule: "ts-test-suffix", Matched: 1}}

	merged, _ := mustMerge(t, inputs(golang, typescript))
	var ids []string
	for _, c := range merged.Configurations {
		encoded, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("json.Marshal(%+v) = %v", c, err)
		}
		ids = append(ids, string(encoded))
	}
	wantIDs := []string{
		`{"id":"linux-amd64","os":"linux","arch":"amd64","tags":["dom"]}`,
		`{"id":"linux-amd64","os":"linux","arch":"amd64","tags":[]}`,
		`{"id":"linux-amd64-integration","os":"linux","arch":"amd64","tags":["integration"]}`,
		`{"id":"tsconfig.json","project":"tsconfig.json"}`,
	}
	if !slices.Equal(ids, wantIDs) {
		t.Errorf("Merge() configurations =\n%s\nwant\n%s", strings.Join(ids, "\n"), strings.Join(wantIDs, "\n"))
	}
	if len(merged.ConfigurationsNotBuilt) != 1 {
		t.Errorf("Merge() configurations_not_built = %+v, want the one repeated entry once", merged.ConfigurationsNotBuilt)
	}
	if c := merged.Consumers; c.Declared != 2 || len(c.Loaded) != 1 || len(c.Unavailable) != 1 {
		t.Errorf("Merge() consumers = %+v, want one loaded, one unavailable, two declared", c)
	}
	if !slices.Equal(merged.ExcludedByCgo, []string{"a_cgo.go", "z_cgo.go"}) {
		t.Errorf("Merge() excluded_by_cgo = %q, want [a_cgo.go z_cgo.go]", merged.ExcludedByCgo)
	}
	wantRules := []report.TestFileRule{{Rule: "go-test-suffix", Matched: 2}, {Rule: "go-test-suffix", Matched: 3}, {Rule: "ts-test-suffix", Matched: 1}}
	if !slices.Equal(merged.TestFileRules, wantRules) {
		t.Errorf("Merge() test_file_rules = %+v, want %+v", merged.TestFileRules, wantRules)
	}
}

func TestMergeRecomputesTheTotalsAndSumsTheSuppressionCounts(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	shared := finding("deadset-go/c-1", "store.go", 1, "a")
	shared.Component.DeletableLines = 7
	second := finding("deadset-go/c-1", "store.go", 9, "b")
	second.Component.DeletableLines = 7
	member := finding("deadset-go/c-1", "store.go", 20, "c")
	member.Component.Root = false
	warned := finding("deadset-go/c-2", "store.go", 30, "d")
	warned.Severity = report.SeverityWarn
	golang.Findings = []report.Finding{shared, second, member, warned}
	golang.StaleSuppressions = []report.StaleSuppression{stale("deadset-ignore.json", 4, "go://example.com/app#x")}
	golang.Totals = report.Totals{SuppressionsInEffect: 2, ReasonsRecorded: 3}
	typescript := analyzerReport("deadset-ts", "ts")
	typescript.Findings = []report.Finding{finding("deadset-ts/c-1", "web/a.ts", 1, "a")}
	typescript.Totals = report.Totals{SuppressionsInEffect: 1, ReasonsRecorded: 4}

	merged, _ := mustMerge(t, inputs(golang, typescript))
	want := report.Totals{
		Findings:             5,
		BySeverity:           report.BySeverity{Warn: 1, Deny: 4},
		DeletableLines:       7 + 5 + 5,
		SuppressionsInEffect: 3,
		ReasonsRecorded:      7,
		StaleSuppressions:    1,
	}
	if merged.Totals != want {
		t.Errorf("Merge() totals = %+v, want %+v", merged.Totals, want)
	}
}

// A capped input's omitted findings stay counted under their severities, so a
// deny finding the cap kept out of the list still fails the merged run.
func TestMergeCountsTheFindingsACappedInputOmitted(t *testing.T) {
	t.Parallel()

	capped := analyzerReport("deadset-go", "go")
	capped.Findings = []report.Finding{finding("deadset-go/c-1", "store.go", 1, "a")}
	capped.Totals = report.Totals{Findings: 3, BySeverity: report.BySeverity{Warn: 1, Deny: 2}, Omitted: 2}
	whole := analyzerReport("deadset-ts", "ts")
	whole.Findings = []report.Finding{finding("deadset-ts/c-1", "web/a.ts", 1, "a")}
	whole.Totals = report.Totals{Findings: 1, BySeverity: report.BySeverity{Deny: 1}}

	merged, _ := mustMerge(t, inputs(capped, whole))
	if got := merged.Totals; got.Findings != 4 || got.Omitted != 2 || got.BySeverity != (report.BySeverity{Warn: 1, Deny: 3}) {
		t.Errorf("Merge(a capped input) totals = %+v, want 4 findings, 2 omitted, 1 warn and 3 deny", got)
	}
}
