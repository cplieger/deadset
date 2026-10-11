package render

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/deadset/internal/merge"
	"github.com/cplieger/deadset/internal/report"
)

// renderSARIF renders one merged case over its synthesized source tree.
func renderSARIF(t *testing.T, c *mergedCase) []byte {
	t.Helper()

	var out bytes.Buffer
	if err := SARIF(&out, c.merged, &Sources{Read: synthesizedSource, Inputs: c.inputs}); err != nil {
		t.Fatalf("SARIF(%s) = %v", c.name, err)
	}
	return out.Bytes()
}

// goldenCases are the merged reports whose SARIF rendering is committed: two
// analyzers' runs, a run of the merge's own finding, a stale suppression, and
// the withheld lines of two runs and of the log.
// Every rule list is the whole vocabulary's, so the other cases are held to
// the mapping's structure rather than to bytes.
var goldenCases = []string{"two-reports-no-edges", "edge-absent-on-every-side", "stale-suppression-carried", "withheld-counts-summed"}

// The SARIF rendering of each golden case is its committed golden, byte for
// byte.
//
// UPDATE_GOLDEN=1 writes the goldens instead of comparing them.
func TestSARIFOfMergedVectors(t *testing.T) {
	t.Parallel()

	for _, c := range mergedCases(t) {
		if !slices.Contains(goldenCases, c.name) {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			compareGolden(t, "sarif", c.name+".sarif", renderSARIF(t, &c))
		})
	}
}

// The members of a SARIF log the Contract's mapping pins, and two it never
// emits, decoded independently of the rendering's own types.
type (
	checkedLog struct {
		Schema     string       `json:"$schema"`
		Version    string       `json:"version"`
		Runs       []checkedRun `json:"runs"`
		Properties struct {
			Totals   *report.Totals `json:"totals"`
			Withheld *string        `json:"withheld"`
		} `json:"properties"`
	}
	checkedRun struct {
		Tool struct {
			Driver struct {
				Name            string        `json:"name"`
				Version         string        `json:"version"`
				SemanticVersion string        `json:"semanticVersion"`
				Rules           []checkedRule `json:"rules"`
			} `json:"driver"`
		} `json:"tool"`
		AutomationDetails  map[string]string                     `json:"automationDetails"`
		ColumnKind         string                                `json:"columnKind"`
		OriginalURIBaseIDs map[string]map[string]json.RawMessage `json:"originalUriBaseIds"`
		Results            []checkedResult                       `json:"results"`
		Properties         struct {
			Totals   *report.Totals `json:"totals"`
			Withheld *string        `json:"withheld"`
		} `json:"properties"`
		Invocations json.RawMessage `json:"invocations"`
	}
	checkedRule struct {
		ID               string            `json:"id"`
		Name             string            `json:"name"`
		ShortDescription map[string]string `json:"shortDescription"`
		FullDescription  map[string]string `json:"fullDescription"`
		Help             map[string]string `json:"help"`
	}
	checkedResult struct {
		RuleID    string            `json:"ruleId"`
		RuleIndex int               `json:"ruleIndex"`
		Level     string            `json:"level"`
		Message   map[string]string `json:"message"`
		Locations []struct {
			PhysicalLocation struct {
				ArtifactLocation map[string]string `json:"artifactLocation"`
				Region           map[string]int    `json:"region"`
			} `json:"physicalLocation"`
		} `json:"locations"`
		PartialFingerprints map[string]string `json:"partialFingerprints"`
		Suppressions        json.RawMessage   `json:"suppressions"`
	}
)

// The values the two fingerprint keys take: a hash and its counter, and a
// digest.
var (
	lineHashValue  = regexp.MustCompile(`^[0-9a-f]+:[1-9][0-9]*$`)
	symbolRefValue = regexp.MustCompile(`^[0-9a-f]{64}$`)
	languageID     = regexp.MustCompile(`^deadset/[a-z]+(\+[a-z]+)*/$`)
)

// conforms names the first way log departs from the structure the Contract's
// mapping pins for r, merged from inputs: r's totals and their withheld line on
// the log, one run per input report in bytewise order of name and one for the
// records naming no analyzer, each with its driver, its ordered and described
// rules, its category, column unit, base, the totals of the report its driver
// wrote and their withheld line, and one result per record its analyzer carried.
func conforms(log *checkedLog, r *report.Report, inputs []*report.Report) error {
	if log.Schema != sarifSchema || log.Version != "2.1.0" {
		return fmt.Errorf("the log names %q version %q", log.Schema, log.Version)
	}
	if log.Properties.Totals == nil || *log.Properties.Totals != r.Totals {
		return fmt.Errorf("the log's totals are %+v, want the merged report's %+v", log.Properties.Totals, r.Totals)
	}
	if err := withheldConforms(log.Properties.Withheld, &r.Totals); err != nil {
		return fmt.Errorf("the log: %w", err)
	}
	carriers := make(map[string]*report.Totals)
	var order []string
	for _, input := range inputs {
		carriers[input.Analyzer.Name] = &input.Totals
		order = append(order, input.Analyzer.Name)
	}
	slices.Sort(order)
	if slices.ContainsFunc(r.Findings, func(f report.Finding) bool { return f.Analyzer == "" }) ||
		slices.ContainsFunc(r.StaleSuppressions, func(s report.StaleSuppression) bool { return s.Analyzer == "" }) {
		carriers[""] = &r.Totals
		order = append(order, "")
	}
	if len(log.Runs) != len(order) {
		return fmt.Errorf("the log holds %d runs, want one per carrier %q", len(log.Runs), order)
	}
	for i, carrier := range order {
		if err := runConforms(&log.Runs[i], r, carrier, carriers[carrier]); err != nil {
			return fmt.Errorf("run %d: %w", i, err)
		}
	}
	return nil
}

// runConforms names the first way one run departs from the run of the
// records carrier carried, the merge's own where carrier is empty, whose
// report's totals are totals.
func runConforms(run *checkedRun, r *report.Report, carrier string, totals *report.Totals) error {
	driver := &run.Tool.Driver
	wantName, automationOK := carrier, languageID.MatchString(run.AutomationDetails["id"])
	if carrier == "" {
		wantName, automationOK = r.Analyzer.Name, run.AutomationDetails["id"] == "deadset/merge/"
	}
	base, declared := run.OriginalURIBaseIDs["%SRCROOT%"]
	switch {
	case driver.Name != wantName || driver.Version == "" || driver.SemanticVersion != driver.Version:
		return fmt.Errorf("the driver is %q %q/%q, want %q", driver.Name, driver.Version, driver.SemanticVersion, wantName)
	case !automationOK:
		return fmt.Errorf("the automation id is %q", run.AutomationDetails["id"])
	case run.ColumnKind != "utf16CodeUnits":
		return fmt.Errorf("the column kind is %q", run.ColumnKind)
	case len(run.OriginalURIBaseIDs) != 1 || !declared || base["uri"] != nil || base["description"] == nil:
		return fmt.Errorf("the bases are %v, want %%SRCROOT%% alone, described and with no uri", run.OriginalURIBaseIDs)
	case run.Properties.Totals == nil || *run.Properties.Totals != *totals:
		return fmt.Errorf("the totals are %+v, want those of the report its driver wrote, %+v", run.Properties.Totals, *totals)
	case run.Invocations != nil:
		return errors.New("the run carries invocations")
	}
	if err := withheldConforms(run.Properties.Withheld, totals); err != nil {
		return err
	}
	for at, rule := range driver.Rules {
		ordered := at == 0 || driver.Rules[at-1].ID < rule.ID
		if !ordered || rule.Name == "" || rule.ShortDescription["text"] == "" ||
			rule.FullDescription["text"] == "" || rule.Help["text"] == "" {
			return fmt.Errorf("rule %d is %+v, want the next code in order, named and described", at, rule)
		}
	}
	records := carriedBy(r, carrier)
	if len(run.Results) != len(records) {
		return fmt.Errorf("the run holds %d results, want %d, one per record %q carried", len(run.Results), len(records), carrier)
	}
	for at := range records {
		if err := resultConforms(&run.Results[at], driver.Rules, &records[at]); err != nil {
			return fmt.Errorf("result %d: %w", at, err)
		}
	}
	return nil
}

// withheldConforms names how a withheld property departs from the withheld line
// of totals: present and equal where the line is written, absent where it is
// not.
func withheldConforms(got *string, totals *report.Totals) error {
	want := totals.Withheld.Line()
	switch {
	case want == "" && got != nil:
		return fmt.Errorf("the withheld line is %q, want none for %+v", *got, totals.Withheld)
	case want != "" && got == nil:
		return fmt.Errorf("no withheld line is written, want %q", want)
	case want != "" && *got != want:
		return fmt.Errorf("the withheld line is %q, want %q", *got, want)
	}
	return nil
}

// record is what one result renders: a finding or a stale suppression.
type record struct {
	code, level, path, message string
	line, column, endLine      int
}

// carriedBy is every record of r the named analyzer carried, findings first,
// each as its result renders it.
func carriedBy(r *report.Report, analyzer string) []record {
	levels := map[report.Severity]string{report.SeverityDeny: "error", report.SeverityWarn: "warning", report.SeverityAllow: "note"}
	var held []record
	for i := range r.Findings {
		if f := &r.Findings[i]; f.Analyzer == analyzer {
			held = append(held, record{
				code: f.Code, level: levels[f.Severity], path: f.Position.Path, message: f.Message,
				line: f.Position.Line, column: f.Position.Column, endLine: f.Position.EndLine,
			})
		}
	}
	for i := range r.StaleSuppressions {
		if s := &r.StaleSuppressions[i]; s.Analyzer == analyzer {
			held = append(held, record{
				code: s.Code, level: "error", path: s.Position.Path, message: s.Message,
				line: s.Position.Line, column: s.Position.Column, endLine: s.Position.Line,
			})
		}
	}
	return held
}

// resultConforms names the first way one result departs from rendering want.
func resultConforms(result *checkedResult, rules []checkedRule, want *record) error {
	if result.RuleIndex < 0 || result.RuleIndex >= len(rules) || rules[result.RuleIndex].ID != result.RuleID {
		return fmt.Errorf("ruleId %s at ruleIndex %d names another rule", result.RuleID, result.RuleIndex)
	}
	if result.RuleID != want.code || result.Level != want.level || !strings.HasPrefix(result.Message["text"], want.message) {
		return fmt.Errorf("the result is %s at %s saying %q, want %s at %s saying %q",
			result.RuleID, result.Level, result.Message["text"], want.code, want.level, want.message)
	}
	if len(result.Locations) != 1 {
		return fmt.Errorf("the result holds %d locations, want one", len(result.Locations))
	}
	at := &result.Locations[0].PhysicalLocation
	wantRegion := map[string]int{"startLine": want.line, "startColumn": want.column, "endLine": want.endLine}
	if at.ArtifactLocation["uri"] != want.path || at.ArtifactLocation["uriBaseId"] != "%SRCROOT%" || !maps.Equal(at.Region, wantRegion) {
		return fmt.Errorf("the location is %v %v, want %s %v against %%SRCROOT%%", at.ArtifactLocation, at.Region, want.path, wantRegion)
	}
	if len(result.PartialFingerprints) != 2 || !lineHashValue.MatchString(result.PartialFingerprints["primaryLocationLineHash"]) ||
		!symbolRefValue.MatchString(result.PartialFingerprints["deadsetSymbolRef/v1"]) {
		return fmt.Errorf("the fingerprints are %v, want the line hash and the symbol digest", result.PartialFingerprints)
	}
	if result.Suppressions != nil {
		return errors.New("the result carries suppressions")
	}
	return nil
}

// The SARIF rendering of every published merged report has the structure the
// Contract's mapping pins, record for record.
func TestSARIFHasTheMappingsStructure(t *testing.T) {
	t.Parallel()

	for _, c := range mergedCases(t) {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var log checkedLog
			if err := json.Unmarshal(renderSARIF(t, &c), &log); err != nil {
				t.Fatalf("decode SARIF(%s): %v", c.name, err)
			}
			if err := conforms(&log, c.merged, c.inputs); err != nil {
				t.Errorf("SARIF(%s) departs from the mapping: %v", c.name, err)
			}
		})
	}
}

// A finding naming positions beyond its own carries a related location for
// each, implementations then writes then its name literal then the other
// members of its component, numbered from one, and its message links every
// one. relatedLocations reads no code, so one finding carries every list.
func TestSARIFRelatesEveryPositionAFindingNames(t *testing.T) {
	t.Parallel()

	at := func(path string, line, column int) report.Position {
		return report.Position{Path: path, Line: line, Column: column, EndLine: line}
	}
	found := report.Finding{
		Code: "DS1301", Severity: report.SeverityDeny, Message: "private member is written and never read",
		Position: at("src/features/tabs/index.ts", 12, 3),
		Symbol:   report.Symbol{Ref: "ts://@example/app/src/features/tabs/index.ts#Tabs.cache"},
		Details: report.Details{
			Implementations: []report.Positioned{{Ref: "ts://x#Impl", Position: at("src/impl.ts", 4, 1)}},
			WritePositions:  []report.Position{at("src/features/tabs/index.ts", 1190, 9), at("src/features/tabs/index.ts", 1201, 9)},
			NameLiteral:     new(at("src/registry.ts", 4, 10)),
		},
		Component: report.Component{Members: []report.Positioned{
			{Ref: "ts://@example/app/src/features/tabs/index.ts#Tabs.cache", Position: at("src/features/tabs/index.ts", 12, 3)},
			{Ref: "ts://x#Helper", Position: at("src/a b.ts", 30, 5)},
		}},
	}
	related := relatedLocations(&found)
	got := messageWithLinks(found.Message, related)
	want := "private member is written and never read (see [implementation src/impl.ts:4:1](1), " +
		"[write src/features/tabs/index.ts:1190:9](2), [write src/features/tabs/index.ts:1201:9](3), [name src/registry.ts:4:10](4), " +
		"[member src/a b.ts:30:5](5))"
	if got != want {
		t.Errorf("messageWithLinks(%s) =\n%s\nwant\n%s", found.Code, got, want)
	}
	if len(related) != 5 || related[4].ID != 5 || related[4].PhysicalLocation.ArtifactLocation.URI != "src/a%20b.ts" {
		t.Errorf("relatedLocations(%s) = %+v, want five, numbered from one, the last at src/a%%20b.ts", found.Code, related)
	}
}

// A finding naming more positions than a result carries relates the first
// hundred of them.
func TestSARIFRelatesAtMostAHundredPositions(t *testing.T) {
	t.Parallel()

	found := report.Finding{}
	for line := range 150 {
		found.Details.WritePositions = append(found.Details.WritePositions, report.Position{Path: "a.go", Line: line + 1, Column: 1, EndLine: line + 1})
	}
	related := relatedLocations(&found)
	if len(related) != maxRelatedLocations || related[99].PhysicalLocation.Region.StartLine != 100 {
		t.Errorf("relatedLocations(150 writes) holds %d, want the first %d", len(related), maxRelatedLocations)
	}
}

// A relative reference percent-encodes each segment, and a colon in the first
// segment, where it would read as a scheme.
func TestRelativeReference(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]string{
		"internal/store/store.go": "internal/store/store.go",
		"web/a b/Ünused.ts":       "web/a%20b/%C3%9Cnused.ts",
		"c:d/e:f.go":              "c%3Ad/e:f.go",
		"100%.go":                 "100%25.go",
	} {
		if got := relativeReference(path); got != want {
			t.Errorf("relativeReference(%q) = %q, want %q", path, got, want)
		}
	}
}

// A rendering refuses what it cannot fingerprint or place: a file it cannot
// read, a line the file does not hold, and an input report it was not given.
func TestSARIFRefusals(t *testing.T) {
	t.Parallel()

	var c mergedCase
	for _, held := range mergedCases(t) {
		if held.name == "one-report" {
			c = held
		}
	}
	if c.merged == nil {
		t.Fatal("Setup: the one-report case is not published")
	}
	short := func(string) ([]byte, error) { return []byte("one line\n"), nil }
	absent := func(string) ([]byte, error) { return nil, fs.ErrNotExist }
	for _, refusal := range []struct {
		name   string
		r      *report.Report
		src    Sources
		target error
		names  string
	}{
		{name: "unreadable", r: c.merged, src: Sources{Read: absent, Inputs: c.inputs}, target: fs.ErrNotExist, names: "line fingerprint"},
		{name: "short-file", r: c.merged, src: Sources{Read: short, Inputs: c.inputs}, names: "holds 2 lines"},
		{name: "no-input", r: c.merged, src: Sources{Read: synthesizedSource}, names: "no input report is deadset-go"},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			err := SARIF(&out, refusal.r, &refusal.src)
			if err == nil || (refusal.target != nil && !errors.Is(err, refusal.target)) || !strings.Contains(err.Error(), refusal.names) {
				t.Errorf("SARIF(%s) = %v, want a refusal naming %q", refusal.name, err, refusal.names)
			}
		})
	}
}

// A merged record that names no analyzer belongs to the merge's own run, a
// stale suppression as well as a finding.
func TestSARIFPlacesAStaleSuppressionNamingNoAnalyzerInTheMergesRun(t *testing.T) {
	t.Parallel()

	var c mergedCase
	for _, held := range mergedCases(t) {
		if held.name == "stale-suppression-carried" {
			c = held
		}
	}
	if c.merged == nil || len(c.merged.StaleSuppressions) != 1 || len(c.merged.Findings) != 0 {
		t.Fatal("Setup: the stale-suppression-carried case holds no lone stale suppression")
	}
	c.merged.StaleSuppressions[0].Analyzer = ""

	var log checkedLog
	if err := json.Unmarshal(renderSARIF(t, &c), &log); err != nil {
		t.Fatalf("decode SARIF(%s): %v", c.name, err)
	}
	last := log.Runs[len(log.Runs)-1]
	if len(log.Runs) != len(c.merged.MergedFrom)+1 || last.Tool.Driver.Name != c.merged.Analyzer.Name ||
		len(last.Results) != 1 || last.Results[0].RuleID != "DS1703" {
		t.Errorf("SARIF(a stale suppression naming no analyzer) holds %d runs, the last %q with %d results, "+
			"want the merge's own run after the inputs' holding the DS1703 result", len(log.Runs), last.Tool.Driver.Name, len(last.Results))
	}
}

// A merge carries every member of a component, so the merged log relates the
// declaration a second package of one name holds under the finding's own
// reference, with the links the analyzer's own log writes.
func TestSARIFOfAMergedReportRelatesEveryOtherDeclarationOfTheComponent(t *testing.T) {
	t.Parallel()

	const name = "component-member-sharing-the-finding-reference"
	c := readSARIFCase(t, name)
	conformance, err := merge.Conformance()
	if err != nil {
		t.Fatalf("Setup: merge.Conformance() = %v", err)
	}
	merged, err := merge.Merge([]merge.Input{{Report: c.report, Digest: "sha256:" + strings.Repeat("0", 64)}}, report.SchemaVersions, &merge.Caller{
		SchemaVersion: report.SchemaVersion, ContractVersion: report.ContractVersion,
		Name: "deadset", Version: "0.0.0", Conformance: conformance,
	})
	if err != nil {
		t.Fatalf("Setup: merge.Merge(%s) = %v", name, err)
	}
	var out bytes.Buffer
	if err := SARIF(&out, merged, &Sources{Read: c.read, Inputs: []*report.Report{c.report}}); err != nil {
		t.Fatalf("SARIF(merged %s) = %v", name, err)
	}

	got, want := relatedOfEachResult(t, out.Bytes()), relatedOfEachResult(t, c.expected)
	if len(got) != 1 || len(want) != 1 {
		t.Fatalf("SARIF(merged %s) holds %d results, want the one result of expected.json, which holds %d", name, len(got), len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SARIF(merged %s) relates %+v, want the related locations and links of expected.json %+v", name, got, want)
	}
}

// relatedOfEachResult is the message and the related locations of every
// result of a SARIF log, in run and result order, each decoded as a value.
func relatedOfEachResult(t *testing.T, document []byte) []any {
	t.Helper()

	var log struct {
		Runs []struct {
			Results []struct {
				RelatedLocations json.RawMessage `json:"relatedLocations"`
				Message          json.RawMessage `json:"message"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(document, &log); err != nil {
		t.Fatalf("decode the SARIF log: %v", err)
	}
	var held []any
	for _, run := range log.Runs {
		for _, result := range run.Results {
			held = append(held, []any{decodedValue(t, result.Message), decodedValue(t, result.RelatedLocations)})
		}
	}
	return held
}
