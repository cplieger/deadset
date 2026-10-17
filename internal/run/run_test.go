package run_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	spec "github.com/cplieger/deadset-spec/v8"
	"github.com/cplieger/deadset-spec/v8/render"
	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/plan"
	"github.com/cplieger/deadset/v2/internal/run"
	"github.com/cplieger/deadset/v2/internal/rundir"
	"github.com/cplieger/deadset/v2/internal/scope"
	"github.com/cplieger/deadset/v2/internal/source"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("Setup: %v", err)
	}
}

// recorder is a source a test states, which records the files it is handed
// and in which directory it was opened.
type recorder struct {
	admit  error
	err    error
	name   string
	dir    string
	report string
	mu     sync.Mutex
	files  source.Files
	ran    bool
}

func (r *recorder) Name() string { return r.name }

func (r *recorder) Admit(_ context.Context, kept rundir.Entry, _ []string) error {
	if r.admit != nil {
		return r.admit
	}
	return kept.WriteDescribe([]byte("{}"))
}

func (r *recorder) Produce(_ context.Context, files source.Files) source.Outcome {
	r.mu.Lock()
	r.files, r.ran = files, true
	r.mu.Unlock()
	out := source.Outcome{Analyzer: r.name, Digest: "sha256:" + strings.Repeat("0", 64), Err: r.err}
	if r.report != "" {
		body, _ := spec.Vectors.ReadFile(r.report)
		_ = os.WriteFile(files.Report, body, 0o600)
		out.Report, out.Err = report.Decode(body)
	}
	return out
}

// request is a run over target under document, opening the recorders by
// entry name; an entry without one resolves to nothing.
func request(t *testing.T, target, document string, declared *scope.Document, recorders map[string]*recorder) *run.Request {
	t.Helper()

	resolved, err := config.Resolve(&config.Inputs{
		Repository: config.Document{Path: filepath.Join(target, config.RepositoryFile), Data: []byte(document)},
	})
	if err != nil {
		t.Fatalf("Setup: resolve: %v", err)
	}
	return &run.Request{
		Resolved: resolved, Scope: declared, Root: target, RunDir: filepath.Join(t.TempDir(), "run"),
		Render: &render.Options{}, Version: "1.0.0",
		Open: func(planned *plan.Source, dir string, _ io.Writer) (source.Source, error) {
			r, ok := recorders[planned.Entry.Name]
			if !ok {
				return nil, &source.CommandError{Err: fs.ErrNotExist, Name: planned.Entry.Name, Index: planned.Index}
			}
			r.dir = dir
			return r, nil
		},
	}
}

const twoAnalyzers = `{"target": {"kind": "library"}, "ts": {"entry_files": ["src/cli.ts"]}, "providers": {"analyzers": [
	{"name": "deadset-go", "command": "deadset-go", "languages": ["go"]},
	{"name": "deadset-ts", "command": "deadset-ts", "languages": ["ts"]}]}}`

// A run over a mixed target and three consumers writes the run's scope
// document naming every module by its identity source, hands each analyzer
// the consumers of its languages, in a scope document of its own where that
// is only some of them, and the configuration of its languages alone, runs
// each in the directory holding every module, and merges their reports.
func TestAnalyzeHandsEachSourceItsFiles(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	target := filepath.Join(base, "app")
	for path, body := range map[string]string{
		"app/go.mod": "module example.com/app\n", "app/tsconfig.json": "{}",
		"go/go.mod": "module example.com/go\n", "ts/tsconfig.json": "{}", "ts/package.json": `{"name": "@example/ts"}`,
		"both/go.mod": "module example.com/both\n", "both/tsconfig.json": "{}",
	} {
		writeFile(t, filepath.Join(base, path), body)
	}
	declared := &scope.Document{Target: scope.Module{Path: target}}
	for _, consumer := range []string{"go", "ts", "both"} {
		declared.Consumers = append(declared.Consumers, scope.Module{Path: filepath.Join(base, consumer)})
	}
	recorders := map[string]*recorder{
		"deadset-go": {name: "deadset-go", report: "vectors/merge/two-reports-no-edges/inputs/00-go.json"},
		"deadset-ts": {name: "deadset-ts", report: "vectors/merge/two-reports-no-edges/inputs/01-ts.json"},
	}
	req := request(t, target, twoAnalyzers, declared, recorders)
	var started string
	req.Started = func(dir string) { started = dir }

	result, err := run.Analyze(t.Context(), req)
	if err != nil {
		t.Fatalf("Analyze() = %v", err)
	}
	if started != req.RunDir || result.Merged == nil || len(result.Merged.MergedFrom) != 2 {
		t.Fatalf("Analyze() = %+v, started %q, want the run directory %s and the merge of both reports", result, started, req.RunDir)
	}
	var whole struct {
		Target    struct{ ID string }
		Consumers []struct{ ID string }
	}
	body, err := os.ReadFile(filepath.Join(req.RunDir, "scope.json"))
	if err != nil || json.Unmarshal(body, &whole) != nil {
		t.Fatalf("read scope.json: %v", err)
	}
	ids := []string{whole.Target.ID}
	for _, consumer := range whole.Consumers {
		ids = append(ids, consumer.ID)
	}
	if want := []string{"example.com/app", "example.com/go", "@example/ts", "example.com/both"}; !slices.Equal(ids, want) {
		t.Errorf("scope.json names %q, want %q", ids, want)
	}
	for name, r := range recorders {
		own := filepath.Join(req.RunDir, "scope."+name+".json")
		if r.dir != base || r.files.Scope != own || r.files.Config != filepath.Join(req.RunDir, "config."+name+".json") {
			t.Errorf("%s ran in %s with %+v, want %s and its own scope and configuration", name, r.dir, r.files, base)
		}
		var handed map[string]json.RawMessage
		body, err := os.ReadFile(r.files.Config)
		if err != nil || json.Unmarshal(body, &handed) != nil {
			t.Fatalf("read %s: %v", r.files.Config, err)
		}
		_, goSection := handed["go"]
		_, tsSection := handed["ts"]
		_, providers := handed["providers"]
		if goSection != (name == "deadset-go") || tsSection != (name == "deadset-ts") || providers {
			t.Errorf("%s's configuration holds go %t, ts %t, providers %t, want its own language's section alone", name, goSection, tsSection, providers)
		}
	}
}

// Every source opens before the run directory exists, so one that resolves
// to nothing leaves none; every refused handshake is joined and no analysis
// runs; and a source that leaves no report leaves the run without a merge.
func TestAnalyzeStopsAtTheFirstStageThatFails(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	writeFile(t, filepath.Join(target, "go.mod"), "module example.com/app\n")
	writeFile(t, filepath.Join(target, "tsconfig.json"), "{}")
	declared := &scope.Document{Target: scope.Module{Path: target}}
	refusedGo, refusedTS := errors.New("go refused"), errors.New("ts refused")

	unresolved := request(t, target, twoAnalyzers, declared, map[string]*recorder{"deadset-go": {name: "deadset-go"}})
	if result, err := run.Analyze(t.Context(), unresolved); result != nil || !strings.Contains(err.Error(), "providers.analyzers[1]") {
		t.Errorf("Analyze(an entry resolving to nothing) = %+v, %v, want no result naming the entry", result, err)
	}
	if _, err := os.Stat(unresolved.RunDir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Analyze(an entry resolving to nothing) left the run directory: %v", err)
	}

	recorders := map[string]*recorder{"deadset-go": {name: "deadset-go", admit: refusedGo}, "deadset-ts": {name: "deadset-ts", admit: refusedTS}}
	result, err := run.Analyze(t.Context(), request(t, target, twoAnalyzers, declared, recorders))
	if !errors.Is(err, refusedGo) || !errors.Is(err, refusedTS) || result == nil || result.Merged != nil || recorders["deadset-go"].ran {
		t.Errorf("Analyze(two refused handshakes) = %+v, %v, want both refusals, the run directory and no analysis", result, err)
	}

	failing := map[string]*recorder{
		"deadset-go": {name: "deadset-go", report: "vectors/merge/two-reports-no-edges/inputs/00-go.json"},
		"deadset-ts": {name: "deadset-ts", err: refusedTS},
	}
	failingRun := request(t, target, twoAnalyzers, declared, failing)
	var shown []source.Outcome
	failingRun.Print = func(outcomes []source.Outcome) error { shown = outcomes; return nil }
	result, err = run.Analyze(t.Context(), failingRun)
	if !errors.Is(err, refusedTS) || result == nil || result.Merged != nil || len(shown) != 2 || shown[0].Report != nil {
		t.Errorf("Analyze(one source refused) = %+v, %v, want every outcome and no report", result, err)
	}
	if _, err := os.Stat(filepath.Join(failingRun.RunDir, "report.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Analyze(one source refused) wrote a merged report: %v", err)
	}
}

// A run directory that exists is refused as the request's.
func TestAnalyzeRefusesARunDirectoryThatExists(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	writeFile(t, filepath.Join(target, "go.mod"), "module example.com/app\n")
	req := request(t, target, twoAnalyzers, &scope.Document{Target: scope.Module{Path: target}}, map[string]*recorder{"deadset-go": {name: "deadset-go"}})
	req.RunDir = t.TempDir()
	if _, err := run.Analyze(t.Context(), req); !errors.Is(err, run.ErrRunDir) || !errors.Is(err, fs.ErrExist) {
		t.Errorf("Analyze(an existing run directory) = %v, want ErrRunDir and fs.ErrExist", err)
	}
}
