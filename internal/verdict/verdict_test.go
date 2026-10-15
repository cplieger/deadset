package verdict_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"testing"

	spec "github.com/cplieger/deadset-spec/v8"
	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/detect"
	"github.com/cplieger/deadset/v2/internal/invoke"
	"github.com/cplieger/deadset/v2/internal/verdict"
)

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
				ContractVersion: vocab.ContractVersion,
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
	refusal := &config.Error{Message: "reporters.fail_under is not a key"}
	outputLost := &invoke.Error{
		Err:      errors.New("copy what the analyzer printed: write /dev/full: no space left on device"),
		Analyzer: "deadset-go",
		Report:   "report.deadset-go.json",
		Exit:     vocab.ExitUsage,
	}

	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "configuration_refusal", err: refusal, want: vocab.ExitUsage},
		{name: "wrapped_configuration_refusal", err: fmt.Errorf("resolve: %w", refusal), want: vocab.ExitUsage},
		{name: "invocation_refusal", err: &verdict.InvocationError{Err: fs.ErrNotExist, Flag: "central"}, want: vocab.ExitUsage},
		{name: "wrapped_invocation_refusal", err: fmt.Errorf("read: %w", &verdict.InvocationError{Err: errors.New("x")}), want: vocab.ExitUsage},
		{name: "no_language_in_scope", err: noLanguage, want: vocab.ExitUsage},
		{name: "filter_outside_the_target", err: outside, want: vocab.ExitUsage},
		{name: "truncated_report", err: truncated, want: vocab.ExitFailure},
		{name: "any_other_error", err: errors.New("deadset-go exited 3"), want: vocab.ExitFailure},
		{name: "analyzer_refusing_its_invocation", err: analyzerExited("deadset-go", vocab.ExitUsage), want: vocab.ExitUsage},
		{name: "analyzer_failing", err: analyzerExited("deadset-go", vocab.ExitFailure), want: vocab.ExitFailure},
		{name: "analyzer_refusing_its_invocation_with_its_output_lost", err: outputLost, want: vocab.ExitFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := verdict.ForError(tc.err); got != tc.want {
				t.Errorf("ForError(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// analyzerExited is the refusal an analyzer that exited with code leaves, as
// invoke builds it.
func analyzerExited(analyzer string, code int) *invoke.Error {
	return &invoke.Error{
		Err:      fmt.Errorf("exited %d: %w", code, invoke.ErrExited),
		Analyzer: analyzer,
		Report:   "report." + analyzer + ".json",
		Exit:     code,
	}
}

// TestForErrorGivesUsageOnlyWhenEveryJoinedRefusalIsUsage pins the code of a
// run several refusals ended, joined as invoke.Run joins them: Usage when
// every refusal is one, whichever analyzer or configuration refused, and
// Failure when any one is not, in either order and under a wrapping.
func TestForErrorGivesUsageOnlyWhenEveryJoinedRefusalIsUsage(t *testing.T) {
	t.Parallel()

	goRefused, tsRefused := analyzerExited("deadset-go", vocab.ExitUsage), analyzerExited("deadset-ts", vocab.ExitUsage)
	tsFailed := analyzerExited("deadset-ts", vocab.ExitFailure)
	configuration := &config.Error{Message: "ts.entry_files is not a list"}

	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "two_analyzers_refusing", err: errors.Join(goRefused, tsRefused), want: vocab.ExitUsage},
		{name: "an_analyzer_and_the_configuration_refusing", err: errors.Join(goRefused, configuration), want: vocab.ExitUsage},
		{name: "a_refusal_then_a_failure", err: errors.Join(goRefused, tsFailed), want: vocab.ExitFailure},
		{name: "a_failure_then_a_refusal", err: errors.Join(tsFailed, goRefused), want: vocab.ExitFailure},
		{name: "a_refusal_beside_any_other_error", err: errors.Join(goRefused, errors.New("deadset-ts: killed")), want: vocab.ExitFailure},
		{name: "wrapped_refusals", err: fmt.Errorf("analyze: %w", errors.Join(goRefused, tsRefused)), want: vocab.ExitUsage},
		{name: "wrapped_refusal_and_failure", err: fmt.Errorf("analyze: %w", errors.Join(goRefused, tsFailed)), want: vocab.ExitFailure},
		{name: "a_refusal_and_a_failure_in_one_message", err: fmt.Errorf("%w; %w", goRefused, tsFailed), want: vocab.ExitFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := verdict.ForError(tc.err); got != tc.want {
				t.Errorf("ForError(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
