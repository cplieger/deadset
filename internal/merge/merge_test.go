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

// caller is the merging product's facts the tests pass.
var caller = Caller{
	SchemaVersion:   report.SchemaVersion,
	ContractVersion: report.ContractVersion,
	Name:            "deadset",
	Version:         "1.2.3",
	Conformance:     report.Conformance{CorpusVersion: "1.0.0", Result: report.ResultPass, Digest: digest("f")},
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

// pending is a dead evaluation of one side of edge carrying f as its pending
// finding, about f's symbol.
func pending(edge string, side report.Side, f report.Finding) report.EdgeEvaluation {
	return report.EdgeEvaluation{Edge: edge, Side: side, Symbol: f.Symbol.Ref, State: report.StateDead, Finding: &f}
}

// componentIDs is the component identifier of every finding, in order, the
// merge's own findings named by the merging product alone.
func componentIDs(merged *report.Report) []string {
	ids := make([]string, len(merged.Findings))
	for i := range merged.Findings {
		ids[i] = merged.Findings[i].Component.ID
		if merged.Findings[i].Analyzer == "" {
			ids[i], _, _ = strings.Cut(ids[i], "/")
		}
	}
	return ids
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

	merged, err := Merge(in, accepted, &caller)
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
	typescript.EdgeEvaluations = []report.EdgeEvaluation{evaluation("wire/Event", report.SideUsedBy, report.StateLive)}

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

// A promoted pending finding and the carried findings of its component are
// rewritten to the joined component, which leaves the input reports they came
// from as they were.
func TestMergeLeavesItsInputsAsTheyWere(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	golang.Findings = []report.Finding{finding("deadset-go/c-2", "z.go", 1, "z"), finding("deadset-go/c-1", "a.go", 1, "a")}
	golang.StaleSuppressions = []report.StaleSuppression{stale("deadset-ignore.json", 4, "go://example.com/app#b")}
	golang.EdgeEvaluations = []report.EdgeEvaluation{evaluation("wire/Event", report.SideProvides, report.StateDead)}
	golang.Configurations = []report.Configuration{platform("linux-arm64"), platform("linux-amd64")}
	typescript := analyzerReport("deadset-ts", "ts")
	typescript.Findings = []report.Finding{finding("deadset-ts/c-1", "web/b.ts", 1, "b")}
	typescript.EdgeEvaluations = []report.EdgeEvaluation{
		pending("wire/Event", report.SideUsedBy, finding("deadset-ts/c-1", "web/wire.ts", 1, "Event")),
		evaluation("wire/Stale", report.SideUsedBy, report.StateAbsent),
	}
	product := caller
	before, err := json.Marshal([]any{golang, typescript, product})
	if err != nil {
		t.Fatalf("Setup: json.Marshal(the arguments) = %v", err)
	}

	merged, _ := mustMerge(t, inputs(golang, typescript))
	if got := componentIDs(merged); !slices.Equal(got, []string{"deadset-go/c-1", "deadset", "deadset-ts/c-1", "deadset-ts/c-1", "deadset-ts/c-1", "deadset-go/c-2"}) {
		t.Fatalf("Setup: Merge() component ids = %q, want the pending pair promoted and joined", got)
	}
	if after, err := json.Marshal([]any{golang, typescript, product}); err != nil || !bytes.Equal(after, before) {
		t.Errorf("the arguments after Merge() =\n%s\nwant them unchanged\n%s", after, before)
	}
}

func TestMergeRefusesNoInput(t *testing.T) {
	t.Parallel()

	if merged, err := Merge(nil, accepted, &caller); merged != nil || !errors.Is(err, ErrNoInput) {
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
		merged, err := Merge(inputs(order...), accepted, &caller)
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

	_, err := Merge(inputs(old), accepted, &caller)
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
		merged, err := Merge(inputs(order...), accepted, &caller)
		mismatch, ok := errors.AsType[*TargetError](err)
		if merged != nil || !ok {
			t.Fatalf("Merge(two targets) = %v, %v, want no report and a *TargetError", merged, err)
		}
		if mismatch.Analyzers != [2]string{"deadset-go", "deadset-ts"} || mismatch.Targets[1].Identity != "@example/app" {
			t.Errorf("Merge(two targets) = %+v, want deadset-go's target, then deadset-ts's", mismatch)
		}
	}
}

func TestMergeNamesTheCallerWithTheInputsLanguagesAndTheAcceptedVersions(t *testing.T) {
	t.Parallel()

	typescript := analyzerReport("deadset-ts", "ts")
	typescript.Analyzer.Version = "2.0.0"
	golang := analyzerReport("deadset-go", "go")
	other := analyzerReport("example-go", "go")
	other.Analyzer.Version = "1.10.0"
	facts := caller
	facts.SchemaVersion, facts.ContractVersion = "6.1.0", "3.9.0"

	in := inputs(typescript, other, golang)
	merged, err := Merge(in, accepted, &facts)
	if err != nil {
		t.Fatalf("Merge() = %v, want a report", err)
	}
	want := report.Analyzer{
		Name:                   "deadset",
		Version:                "1.2.3",
		Languages:              []string{"go", "ts"},
		SchemaVersionsAccepted: accepted,
		Conformance:            caller.Conformance,
	}
	if !reflect.DeepEqual(merged.Analyzer, want) {
		t.Errorf("Merge() analyzer = %+v, want %+v", merged.Analyzer, want)
	}
	wantFrom := []report.InputReport{
		{Name: "deadset-go", Version: "1.0.0", Digest: in[2].Digest},
		{Name: "deadset-ts", Version: "2.0.0", Digest: in[0].Digest},
		{Name: "example-go", Version: "1.10.0", Digest: in[1].Digest},
	}
	if !slices.Equal(merged.MergedFrom, wantFrom) {
		t.Errorf("Merge() merged_from = %+v, want %+v", merged.MergedFrom, wantFrom)
	}
	if merged.SchemaVersion != "6.1.0" || merged.ContractVersion != "3.9.0" {
		t.Errorf("Merge() versions = %s and %s, want the caller's 6.1.0 and 3.9.0", merged.SchemaVersion, merged.ContractVersion)
	}
}

func TestMergeUnionsTheEnvelopeMembersWithOneEntryPerID(t *testing.T) {
	t.Parallel()

	golang := analyzerReport("deadset-go", "go")
	golang.Configurations = []report.Configuration{platform("linux-amd64"), platform("linux-amd64-integration", "integration")}
	golang.ConfigurationsNotBuilt = []report.ConfigurationNotBuilt{{Configuration: platform("windows-amd64"), Error: "z: no build"}}
	golang.Consumers.Loaded = []report.LoadedConsumer{{ID: "example.com/cli", Role: "consumer", Path: "../cli"}}
	golang.Consumers.Unavailable = []report.UnavailableConsumer{{ID: "@example/web", Role: "consumer", Reason: "z: it declares no path"}}
	golang.ExcludedByCgo = []string{"z_cgo.go", "a_cgo.go"}
	golang.TestFileRules = []report.TestFileRule{{Rule: "go-test-suffix", Matched: 3}}
	typescript := analyzerReport("deadset-ts", "ts")
	typescript.Configurations = []report.Configuration{{ID: "tsconfig.json", Project: "tsconfig.json"}, platform("linux-amd64")}
	typescript.ConfigurationsNotBuilt = []report.ConfigurationNotBuilt{{Configuration: platform("windows-amd64"), Error: "a: no build"}}
	typescript.Consumers.Loaded = []report.LoadedConsumer{{ID: "example.com/cli", Role: "consumer", Path: "../cli"}}
	typescript.Consumers.Unavailable = []report.UnavailableConsumer{{ID: "@example/web", Role: "consumer", Reason: "a: excluded"}}
	typescript.ExcludedByCgo = []string{"a_cgo.go"}
	typescript.TestFileRules = []report.TestFileRule{{Rule: "go-test-suffix", Matched: 2}, {Rule: "ts-test-suffix", Matched: 1}}

	for _, order := range [][]*report.Report{{golang, typescript}, {typescript, golang}} {
		merged, _ := mustMerge(t, inputs(order...))
		var ids []string
		for _, c := range merged.Configurations {
			ids = append(ids, c.ID)
		}
		if want := []string{"linux-amd64", "linux-amd64-integration", "tsconfig.json"}; !slices.Equal(ids, want) {
			t.Errorf("Merge() configurations = %q, want %q", ids, want)
		}
		if got := merged.ConfigurationsNotBuilt; len(got) != 1 || got[0].Error != "z: no build" {
			t.Errorf("Merge() configurations_not_built = %+v, want the one id once, with deadset-go's error", got)
		}
		c := merged.Consumers
		if c.Declared != 2 || len(c.Loaded) != 1 || len(c.Unavailable) != 1 || c.Unavailable[0].Reason != "z: it declares no path" {
			t.Errorf("Merge() consumers = %+v, want one loaded, one unavailable with deadset-go's reason, two declared", c)
		}
		if !slices.Equal(merged.ExcludedByCgo, []string{"a_cgo.go", "z_cgo.go"}) {
			t.Errorf("Merge() excluded_by_cgo = %q, want [a_cgo.go z_cgo.go]", merged.ExcludedByCgo)
		}
		wantRules := []report.TestFileRule{{Rule: "go-test-suffix", Matched: 2}, {Rule: "go-test-suffix", Matched: 3}, {Rule: "ts-test-suffix", Matched: 1}}
		if !slices.Equal(merged.TestFileRules, wantRules) {
			t.Errorf("Merge() test_file_rules = %+v, want %+v", merged.TestFileRules, wantRules)
		}
	}
}

// Two reports that disagree on what one id names are refused, whichever order
// the merge reads them in: two entries under one id differing in an identity
// member, and one id in an array and in the array paired with it.
func TestMergeRefusesTwoReportsDisagreeingOnAnID(t *testing.T) {
	t.Parallel()

	loaded := func(path string) []report.LoadedConsumer {
		return []report.LoadedConsumer{{ID: "example.com/cli", Role: "consumer", Path: path}}
	}
	unavailable := []report.UnavailableConsumer{{ID: "example.com/cli", Role: "consumer", Reason: "it declares no path"}}
	cases := []struct {
		golang, typescript func(*report.Report)
		name               string
		reason             error
		arrays             [2]string
	}{
		{
			name: "platform-tags", reason: ErrEntryIdentity, arrays: [2]string{"configurations", "configurations"},
			golang:     func(r *report.Report) { r.Configurations = []report.Configuration{platform("linux-amd64", "netgo")} },
			typescript: func(r *report.Report) { r.Configurations = []report.Configuration{platform("linux-amd64")} },
		},
		{
			name: "platform-and-project", reason: ErrEntryIdentity, arrays: [2]string{"configurations", "configurations"},
			golang: func(r *report.Report) { r.Configurations = []report.Configuration{platform("tsconfig.json")} },
			typescript: func(r *report.Report) {
				r.Configurations = []report.Configuration{{ID: "tsconfig.json", Project: "tsconfig.json"}}
			},
		},
		{
			name: "not-built-shapes", reason: ErrEntryIdentity, arrays: [2]string{"configurations_not_built", "configurations_not_built"},
			golang: func(r *report.Report) {
				r.ConfigurationsNotBuilt = []report.ConfigurationNotBuilt{{Configuration: platform("windows-amd64", "cgo"), Error: "no build"}}
			},
			typescript: func(r *report.Report) {
				r.ConfigurationsNotBuilt = []report.ConfigurationNotBuilt{{Configuration: platform("windows-amd64"), Error: "no build"}}
			},
		},
		{
			name: "loaded-paths", reason: ErrEntryIdentity, arrays: [2]string{"consumers.loaded", "consumers.loaded"},
			golang:     func(r *report.Report) { r.Consumers.Loaded = loaded("../cli") },
			typescript: func(r *report.Report) { r.Consumers.Loaded = loaded("../other") },
		},
		{
			name: "built-and-not-built", reason: ErrEntryState, arrays: [2]string{"configurations", "configurations_not_built"},
			golang: func(r *report.Report) {},
			typescript: func(r *report.Report) {
				r.ConfigurationsNotBuilt = []report.ConfigurationNotBuilt{{Configuration: platform("linux-amd64"), Error: "no build"}}
			},
		},
		{
			name: "unavailable-and-loaded", reason: ErrEntryState, arrays: [2]string{"consumers.unavailable", "consumers.loaded"},
			golang:     func(r *report.Report) { r.Consumers.Unavailable = unavailable },
			typescript: func(r *report.Report) { r.Consumers.Loaded = loaded("../cli") },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			golang, typescript := analyzerReport("deadset-go", "go"), analyzerReport("deadset-ts", "ts")
			tc.golang(golang)
			tc.typescript(typescript)
			for _, order := range [][]*report.Report{{golang, typescript}, {typescript, golang}} {
				merged, err := Merge(inputs(order...), accepted, &caller)
				refused, ok := errors.AsType[*EntryError](err)
				if merged != nil || !ok || !errors.Is(err, tc.reason) {
					t.Fatalf("Merge(%s) = %v, %v, want no report and an *EntryError for %v", tc.name, merged, err, tc.reason)
				}
				if refused.Analyzers != [2]string{"deadset-go", "deadset-ts"} || refused.Arrays != tc.arrays {
					t.Errorf("Merge(%s) = %+v, want deadset-go then deadset-ts, in %q", tc.name, refused, tc.arrays)
				}
			}
		})
	}
}

// Two reports of one analyzer name are refused, whatever their versions and
// digests and whichever order the merge reads them in, before their entries
// are compared.
func TestMergeRefusesTwoReportsOfOneAnalyzerName(t *testing.T) {
	t.Parallel()

	older := analyzerReport("deadset-go", "go")
	newer := analyzerReport("deadset-go", "go")
	newer.Analyzer.Version = "1.1.0"
	newer.ConfigurationsNotBuilt = []report.ConfigurationNotBuilt{{Configuration: platform("linux-amd64"), Error: "no build"}}

	for _, order := range [][]*report.Report{{older, newer}, {newer, older}} {
		merged, err := Merge(inputs(order...), accepted, &caller)
		refused, ok := errors.AsType[*NameError](err)
		if merged != nil || !ok {
			t.Fatalf("Merge(one name twice) = %v, %v, want no report and a *NameError", merged, err)
		}
		if refused.Name != "deadset-go" || refused.Versions != [2]string{"1.0.0", "1.1.0"} {
			t.Errorf("Merge(one name twice) = %+v, want deadset-go at 1.0.0 and 1.1.0", refused)
		}
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

// A report whose cap kept findings out of its list is refused, so no merged
// report is built from a partial finding list.
func TestMergeRefusesAReportThatOmittedFindings(t *testing.T) {
	t.Parallel()

	capped := analyzerReport("deadset-go", "go")
	capped.Findings = []report.Finding{finding("deadset-go/c-1", "store.go", 1, "a")}
	capped.Totals = report.Totals{Findings: 3, BySeverity: report.BySeverity{Deny: 3}, Omitted: 2}
	whole := analyzerReport("deadset-ts", "ts")

	merged, err := Merge(inputs(whole, capped), accepted, &caller)
	refused, ok := errors.AsType[*AdmissionError](err)
	if merged != nil || !ok || !errors.Is(err, ErrOmitted) {
		t.Fatalf("Merge(a capped input) = %v, %v, want no report and an *AdmissionError for ErrOmitted", merged, err)
	}
	if refused.Analyzer != "deadset-go" || refused.Omitted != 2 {
		t.Errorf("Merge(a capped input) = %+v, want deadset-go refused on its 2 omitted findings", refused)
	}
}
