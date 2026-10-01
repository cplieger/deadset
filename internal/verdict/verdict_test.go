package verdict_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"strconv"
	"strings"
	"testing"

	spec "github.com/cplieger/deadset-spec/v3"
	"github.com/cplieger/deadset/internal/config"
	"github.com/cplieger/deadset/internal/detect"
	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/verdict"
)

// vector decodes one report document of the published merge vectors, named by
// its path below vectors/merge.
func vector(t *testing.T, name string) *report.Report {
	t.Helper()

	data, err := spec.Vectors.ReadFile(path.Join("vectors/merge", name))
	if err != nil {
		t.Fatalf("Setup: read vectors/merge/%s: %v", name, err)
	}
	r, err := report.Decode(data)
	if err != nil {
		t.Fatalf("Setup: decode vectors/merge/%s: %v", name, err)
	}
	return r
}

// lowered sets every finding of r to severity, as a severity map naming every
// kind at that severity would have, and counts them under it.
func lowered(r *report.Report, severity report.Severity) *report.Report {
	for i := range r.Findings {
		r.Findings[i].Severity = severity
	}
	counts := map[report.Severity]int{severity: len(r.Findings)}
	r.Totals.BySeverity = report.BySeverity{
		Allow: counts[report.SeverityAllow],
		Warn:  counts[report.SeverityWarn],
		Deny:  counts[report.SeverityDeny],
	}
	return r
}

// TestTheExitCodesAreTheContractsTable pins every exit code to the code
// contract/exit-codes.json gives the same name.
func TestTheExitCodesAreTheContractsTable(t *testing.T) {
	t.Parallel()

	body, err := spec.Contract.ReadFile("contract/exit-codes.json")
	if err != nil {
		t.Fatalf("Setup: read contract/exit-codes.json: %v", err)
	}
	var table struct {
		ExitCodes []struct {
			Name string `json:"name"`
			Code int    `json:"code"`
		} `json:"exit_codes"`
	}
	if err := json.Unmarshal(body, &table); err != nil {
		t.Fatalf("Setup: decode contract/exit-codes.json: %v", err)
	}
	contract := make(map[string]int, len(table.ExitCodes))
	for _, row := range table.ExitCodes {
		contract[row.Name] = row.Code
	}

	ours := map[string]int{
		"clean":    verdict.Clean,
		"findings": verdict.Findings,
		"usage":    verdict.Usage,
		"failure":  verdict.Failure,
		"pending":  verdict.Pending,
	}
	if !maps.Equal(ours, contract) {
		t.Errorf("exit codes = %v, want contract/exit-codes.json's %v", ours, contract)
	}
}

// TestCodeOfEveryMergedVectorIsItsPublishedExitCode pins the verdict over every
// merged report the vectors publish to the exit code the case declares.
func TestCodeOfEveryMergedVectorIsItsPublishedExitCode(t *testing.T) {
	t.Parallel()

	merged, err := fs.Glob(spec.Vectors, "vectors/merge/*/expected.json")
	if err != nil || len(merged) == 0 {
		t.Fatalf("Setup: glob vectors/merge/*/expected.json = %v, %v, want the merged cases", merged, err)
	}
	for _, expected := range merged {
		dir := path.Dir(expected)
		t.Run(path.Base(dir), func(t *testing.T) {
			t.Parallel()

			body, err := spec.Vectors.ReadFile(path.Join(dir, "expected_exit"))
			if err != nil {
				t.Fatalf("Setup: read %s/expected_exit: %v", dir, err)
			}
			want, err := strconv.Atoi(strings.TrimSpace(string(body)))
			if err != nil {
				t.Fatalf("Setup: %s/expected_exit holds %q, not an exit code", dir, body)
			}

			r := vector(t, path.Join(path.Base(dir), "expected.json"))
			if got := verdict.Code(r, config.Deny, verdict.On); got != want {
				t.Errorf("Code(%s, deny, On) = %d, want %d", expected, got, want)
			}
		})
	}
}

// TestCodeYieldsEachVerdict pins the three verdicts a report yields, each over
// a published report: a merged report with nothing failing, a merged report
// with a deny finding, and an analyzer's report carrying the pending finding a
// merge resolves.
func TestCodeYieldsEachVerdict(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		report string
		want   int
	}{
		{report: "pending-pair-live/expected.json", want: verdict.Clean},
		{report: "one-report/expected.json", want: verdict.Findings},
		{report: "pending-pair-dead/inputs/00-go.json", want: verdict.Pending},
	} {
		t.Run(strconv.Itoa(tc.want), func(t *testing.T) {
			t.Parallel()

			if got := verdict.Code(vector(t, tc.report), config.Deny, verdict.On); got != tc.want {
				t.Errorf("Code(%s, deny, On) = %d, want %d", tc.report, got, tc.want)
			}
		})
	}
}

// TestCodePendingOutranksFindings pins that a report holding a pending finding
// beside a deny finding and a stale suppression yields the pending code, the
// higher of the two the table assigns.
func TestCodePendingOutranksFindings(t *testing.T) {
	t.Parallel()

	r := vector(t, "pending-pair-dead/inputs/00-go.json")
	stale := vector(t, "stale-suppression-carried/expected.json")
	r.StaleSuppressions = stale.StaleSuppressions
	r.Totals.StaleSuppressions = len(stale.StaleSuppressions)
	r.Totals.BySeverity.Deny = 1
	if got := verdict.Code(r, config.Deny, verdict.On); got != verdict.Pending {
		t.Errorf("Code(pending-pair-dead with a deny count and a stale suppression, deny, On) = %d, want %d",
			got, verdict.Pending)
	}
}

// TestCodeFailsOnAFindingAtOrAboveTheFailingSeverity pins that a finding below
// the failing severity leaves the run clean, so a report whose findings are all
// warn is clean under the default, and that lowering the failing severity to a
// finding's severity fails the run.
func TestCodeFailsOnAFindingAtOrAboveTheFailingSeverity(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		severity report.Severity
		failOn   config.Severity
		want     int
	}{
		{severity: report.SeverityWarn, failOn: config.Deny, want: verdict.Clean},
		{severity: report.SeverityWarn, failOn: config.Warn, want: verdict.Findings},
		{severity: report.SeverityWarn, failOn: config.Allow, want: verdict.Findings},
		{severity: report.SeverityAllow, failOn: config.Deny, want: verdict.Clean},
		{severity: report.SeverityAllow, failOn: config.Warn, want: verdict.Clean},
		{severity: report.SeverityAllow, failOn: config.Allow, want: verdict.Findings},
	} {
		t.Run(fmt.Sprintf("%s_under_%s", tc.severity, tc.failOn), func(t *testing.T) {
			t.Parallel()

			r := lowered(vector(t, "two-reports-no-edges/expected.json"), tc.severity)
			if got := verdict.Code(r, tc.failOn, verdict.On); got != tc.want {
				t.Errorf("Code(two-reports-no-edges with every finding %s, %s, On) = %d, want %d",
					tc.severity, tc.failOn, got, tc.want)
			}
		})
	}
}

// TestCodeFailsOnAStaleSuppressionWhateverTheSeverities pins that a stale
// suppression fails the run under every failing severity, beside findings of
// any severity.
func TestCodeFailsOnAStaleSuppressionWhateverTheSeverities(t *testing.T) {
	t.Parallel()

	stale := vector(t, "stale-suppression-carried/expected.json")
	for _, failOn := range []config.Severity{config.Allow, config.Warn, config.Deny} {
		if got := verdict.Code(stale, failOn, verdict.On); got != verdict.Findings {
			t.Errorf("Code(stale-suppression-carried, %s, On) = %d, want %d", failOn, got, verdict.Findings)
		}
	}

	for _, severity := range []report.Severity{report.SeverityAllow, report.SeverityWarn} {
		r := lowered(vector(t, "two-reports-no-edges/expected.json"), severity)
		r.StaleSuppressions = stale.StaleSuppressions
		r.Totals.StaleSuppressions = len(stale.StaleSuppressions)
		if got := verdict.Code(r, config.Deny, verdict.On); got != verdict.Findings {
			t.Errorf("Code(two-reports-no-edges with every finding %s and one stale suppression, deny, On) = %d, want %d",
				severity, got, verdict.Findings)
		}
	}
}

// TestCodeOffIsCleanWhateverTheVerdict pins that the switch turned off makes
// every report's exit code clean, while the verdict with the switch on is
// unchanged.
func TestCodeOffIsCleanWhateverTheVerdict(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		report  string
		verdict int
	}{
		{name: "deny_finding", report: "one-report/expected.json", verdict: verdict.Findings},
		{name: "stale_suppression", report: "stale-suppression-carried/expected.json", verdict: verdict.Findings},
		{name: "pending_finding", report: "pending-pair-dead/inputs/00-go.json", verdict: verdict.Pending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := vector(t, tc.report)
			if got := verdict.Code(r, config.Deny, verdict.Off); got != verdict.Clean {
				t.Errorf("Code(%s, deny, Off) = %d, want %d", tc.report, got, verdict.Clean)
			}
			if got := verdict.Code(r, config.Deny, verdict.On); got != tc.verdict {
				t.Errorf("Code(%s, deny, On) after Off = %d, want %d", tc.report, got, tc.verdict)
			}
		})
	}
}

// TestFails pins the order of the three severities against every failing
// severity, and that a failing severity outside them is deny.
func TestFails(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		severity report.Severity
		failOn   config.Severity
		want     bool
	}{
		{severity: report.SeverityDeny, failOn: config.Deny, want: true},
		{severity: report.SeverityWarn, failOn: config.Deny, want: false},
		{severity: report.SeverityAllow, failOn: config.Deny, want: false},
		{severity: report.SeverityDeny, failOn: config.Warn, want: true},
		{severity: report.SeverityWarn, failOn: config.Warn, want: true},
		{severity: report.SeverityAllow, failOn: config.Warn, want: false},
		{severity: report.SeverityDeny, failOn: config.Allow, want: true},
		{severity: report.SeverityWarn, failOn: config.Allow, want: true},
		{severity: report.SeverityAllow, failOn: config.Allow, want: true},
		{severity: report.SeverityWarn, failOn: "", want: false},
		{severity: report.SeverityDeny, failOn: "", want: true},
	} {
		if got := verdict.Fails(tc.severity, tc.failOn); got != tc.want {
			t.Errorf("Fails(%q, %q) = %t, want %t", tc.severity, tc.failOn, got, tc.want)
		}
	}
}

// TestForErrorMapsEveryPublishedConfigurationRefusalToItsExitCode pins the code
// of every configuration the published vectors refuse to the code the case
// declares.
func TestForErrorMapsEveryPublishedConfigurationRefusalToItsExitCode(t *testing.T) {
	t.Parallel()

	refused, err := fs.Glob(spec.Vectors, "vectors/config/*/expected-error.json")
	if err != nil || len(refused) == 0 {
		t.Fatalf("Setup: glob vectors/config/*/expected-error.json = %v, %v, want the refused cases", refused, err)
	}
	for _, expected := range refused {
		dir := path.Dir(expected)
		t.Run(path.Base(dir), func(t *testing.T) {
			t.Parallel()

			var want struct {
				ExitCode int `json:"exit_code"`
			}
			if body, err := spec.Vectors.ReadFile(expected); err != nil || json.Unmarshal(body, &want) != nil {
				t.Fatalf("Setup: read %s: %v", expected, err)
			}
			_, err := config.Resolve(&config.Inputs{
				ContractVersion: report.ContractVersion,
				Repository:      config.Document{Path: "repository.json", Data: caseFile(t, dir, "repository.json")},
				Central:         config.Document{Path: "central.json", Data: caseFile(t, dir, "central.json")},
				Flags:           caseFile(t, dir, "flags.json"),
			})
			if err == nil {
				t.Fatalf("Resolve(%s) = no error, want the refusal the case declares", dir)
			}
			if got := verdict.ForError(err); got != want.ExitCode {
				t.Errorf("ForError(Resolve(%s) = %v) = %d, want %d", dir, err, got, want.ExitCode)
			}
		})
	}
}

// caseFile reads one file of a published configuration case, or nil when the
// case has none.
func caseFile(t *testing.T, dir, name string) []byte {
	t.Helper()

	data, err := spec.Vectors.ReadFile(path.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("Setup: read %s: %v", path.Join(dir, name), err)
	}
	return data
}

// TestForError pins the code of each error a run can end on before its report
// exists: the refusals that are Usage, wrapped or not, and a report that does
// not decode, which is Failure.
func TestForError(t *testing.T) {
	t.Parallel()

	whole, err := spec.Vectors.ReadFile("vectors/merge/one-report/inputs/00-go.json")
	if err != nil {
		t.Fatalf("Setup: read vectors/merge/one-report/inputs/00-go.json: %v", err)
	}
	_, truncated := report.Decode(whole[:len(whole)/2])
	if truncated == nil {
		t.Fatal("Setup: Decode(half of a report) = no error, want the decode failure")
	}
	_, noLanguage := detect.Languages(t.TempDir(), detect.Options{})
	if !errors.Is(noLanguage, detect.ErrNoLanguage) {
		t.Fatalf("Setup: Languages(an empty tree) = error %v, want detect.ErrNoLanguage", noLanguage)
	}
	_, outside := detect.Languages(t.TempDir(), detect.Options{Filters: []string{"../elsewhere"}})
	if !errors.Is(outside, detect.ErrFilter) {
		t.Fatalf("Setup: Languages(a filter outside the target) = error %v, want detect.ErrFilter", outside)
	}
	refusal := &config.Error{Key: "reporters.fail_under", Message: "reporters.fail_under is not a key"}

	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "configuration_refusal", err: refusal, want: verdict.Usage},
		{name: "wrapped_configuration_refusal", err: fmt.Errorf("resolve: %w", refusal), want: verdict.Usage},
		{name: "no_language_in_scope", err: noLanguage, want: verdict.Usage},
		{name: "filter_outside_the_target", err: outside, want: verdict.Usage},
		{name: "truncated_report", err: truncated, want: verdict.Failure},
		{name: "any_other_error", err: errors.New("deadset-go exited 3"), want: verdict.Failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := verdict.ForError(tc.err); got != tc.want {
				t.Errorf("ForError(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
