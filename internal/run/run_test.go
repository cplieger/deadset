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
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/failure"
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

// recorder is a source a test states, which records the files it is handed,
// every run, and in which directory it was opened.
type recorder struct {
	admit error
	err   error
	// later is what the source answers from its second run on, where that
	// differs from its first.
	later *recorder
	name  string
	dir   string
	// consumer is the id its report names the scope's first consumer by where
	// the scope it is handed names none, as an analyzer names one by its load.
	consumer  string
	report    string
	printed   string
	languages []vocab.Language
	runs      []source.Files
	mu        sync.Mutex
	files     source.Files
	exit      int
	ran       bool
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
	r.files, r.ran, r.runs = files, true, append(r.runs, files)
	answer := r
	if len(r.runs) > 1 && r.later != nil {
		answer = r.later
	}
	r.mu.Unlock()
	out := source.Outcome{
		Analyzer: r.name, Languages: r.languages, Digest: "sha256:" + strings.Repeat("0", 64),
		Err: answer.err, Exit: answer.exit, Printed: []byte(answer.printed),
	}
	if answer.report != "" {
		body, _ := spec.Vectors.ReadFile(answer.report)
		_ = os.WriteFile(files.Report, body, 0o600)
		out.Report, out.Err = report.Decode(body)
	}
	if out.Report != nil && answer.consumer != "" {
		out.Report.Consumers = loadedConsumer(files.Scope, answer.consumer)
	}
	return out
}

// loadedConsumer is the consumers of a report that loaded the first consumer
// of the scope document at path, by the id the document names, else by id.
func loadedConsumer(path, id string) report.Consumers {
	var document struct{ Consumers []struct{ ID string } }
	if body, err := os.ReadFile(path); err == nil && json.Unmarshal(body, &document) == nil && len(document.Consumers) > 0 && document.Consumers[0].ID != "" {
		id = document.Consumers[0].ID
	}
	return report.Consumers{
		Declared: 1, Loaded: []report.LoadedConsumer{{ID: id, Role: "consumer", Path: "consumer"}},
		Unavailable: []report.UnavailableConsumer{},
	}
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
				return nil, failure.Wrap(vocab.RunMissingAnalyzer, &source.CommandError{Err: fs.ErrNotExist, Name: planned.Entry.Name, Index: planned.Index})
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
// each in the directory holding every module, merges their reports and writes
// the SARIF rendering from the lines below the target root that their
// target-relative positions name.
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
	for _, path := range []string{"internal/store/store.go", "web/src/tabs.ts", "web/src/wire.ts"} {
		writeFile(t, filepath.Join(target, path), strings.Repeat("line\n", 41))
	}
	req := request(t, target, twoAnalyzers, declared, recorders)
	req.Resolved.Config.Reporters.Formats = []vocab.Format{vocab.FormatSARIF}
	var started string
	req.Started = func(dir string) { started = dir }

	result, err := run.Analyze(t.Context(), req)
	if err != nil {
		t.Fatalf("Analyze() = %v", err)
	}
	if started != req.RunDir || result.Merged == nil || len(result.Merged.MergedFrom) != 2 {
		t.Fatalf("Analyze() = %+v, started %q, want the run directory %s and the merge of both reports", result, started, req.RunDir)
	}
	if len(result.Written) != 1 || result.Written[0].Format != vocab.FormatSARIF {
		t.Errorf("Analyze() wrote %+v, want the SARIF rendering alone", result.Written)
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

// targetID is the id of the target the scope document at path names.
func targetID(t *testing.T, path string) string {
	t.Helper()

	var document struct{ Target struct{ ID string } }
	body, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(body, &document) != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return document.Target.ID
}

// A run that leaves TypeScript out plans again without it: where that names
// the target otherwise, as a Go module below the target root does, the Go
// analyzer runs again over the narrowed scope in the run directory's rerun
// directory and its second report is merged; where it names the target as
// before, the Go analyzer runs once.
func TestAnalyzeRunsAgainTheSourcesALeftOutLanguageNamedAModuleFor(t *testing.T) {
	t.Parallel()

	const document = `{"target": {"kind": "application"}, "providers": {"analyzers": [
	{"name": "deadset-go", "command": "deadset-go", "languages": ["go"]},
	{"name": "deadset-ts", "command": "deadset-ts", "languages": ["ts"]}]}}`
	for _, c := range []struct {
		name, module, first string
	}{
		{name: "module-below-the-root", module: "services/api/go.mod", first: "."},
		{name: "module-at-the-root", module: "go.mod", first: "example.com/api"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			target := t.TempDir()
			writeFile(t, filepath.Join(target, filepath.FromSlash(c.module)), "module example.com/api\n")
			writeFile(t, filepath.Join(target, "web", "app.js"), "export const x = 1;\n")
			recorders := map[string]*recorder{
				"deadset-go": {name: "deadset-go", languages: []vocab.Language{vocab.Go}, report: "vectors/merge/two-reports-no-edges/inputs/00-go.json"},
				"deadset-ts": {
					name: "deadset-ts", languages: []vocab.Language{vocab.TS}, exit: vocab.ExitFailure,
					printed: vocab.SetupFailurePrefix + "no-program: the target holds no JavaScript file a program is formed from\n",
				},
			}
			req := request(t, target, document, &scope.Document{Target: scope.Module{Path: target}}, recorders)
			result, err := run.Analyze(t.Context(), req)
			if err != nil || len(result.Merged.MergedFrom) != 1 || result.Merged.MergedFrom[0].Name != "deadset-go" {
				t.Fatalf("Analyze() = %+v, %v, want the Go analyzer's report alone", result, err)
			}
			if id := targetID(t, filepath.Join(req.RunDir, "scope.json")); id != c.first {
				t.Errorf("scope.json names the target %q, want %q", id, c.first)
			}
			goRuns, rerun := recorders["deadset-go"].runs, filepath.Join(req.RunDir, "rerun")
			want := []source.Files{{
				Scope: filepath.Join(req.RunDir, "scope.json"), Config: filepath.Join(req.RunDir, "config.deadset-go.json"),
				Report: filepath.Join(req.RunDir, "report.deadset-go.json"),
			}}
			if c.first == "." {
				want = append(want, source.Files{
					Scope: filepath.Join(rerun, "scope.json"), Config: filepath.Join(rerun, "config.deadset-go.json"),
					Report: filepath.Join(rerun, "report.deadset-go.json"),
				})
				if id := targetID(t, want[1].Scope); id != "" {
					t.Errorf("the narrowed scope names the target %q, want no name, as a run with no JavaScript hands", id)
				}
			}
			if !slices.Equal(goRuns, want) || len(recorders["deadset-ts"].runs) != 1 {
				t.Errorf("the Go analyzer ran with %+v and the TypeScript one %d times, want %+v and once", goRuns, len(recorders["deadset-ts"].runs), want)
			}
		})
	}
}

// An analyzer claiming Go and TypeScript that finds no program leaves out only
// TypeScript beside a Go analyzer that reports, and the narrowed plan, which
// still routes the analyzer left out, routes the kept one too: it runs again
// over the narrowed scope and its report is merged.
func TestAnalyzeRunsAgainTheKeptSourceBesideOneThatClaimedALanguageKept(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	writeFile(t, filepath.Join(target, "services", "api", "go.mod"), "module example.com/api\n")
	writeFile(t, filepath.Join(target, "web", "app.js"), "export const x = 1;\n")
	recorders := map[string]*recorder{
		"go-and-ts":  {name: "go-and-ts", languages: []vocab.Language{vocab.Go, vocab.TS}, exit: vocab.ExitFailure, printed: noProgram},
		"deadset-go": {name: "deadset-go", languages: []vocab.Language{vocab.Go}, report: "vectors/merge/two-reports-no-edges/inputs/00-go.json"},
	}
	req := request(t, target, `{"target": {"kind": "application"}, "providers": {"analyzers": [
	{"name": "go-and-ts", "command": "go-and-ts", "languages": ["go", "ts"]},
	{"name": "deadset-go", "command": "deadset-go", "languages": ["go"]}]}}`, &scope.Document{Target: scope.Module{Path: target}}, recorders)
	var shown []source.Outcome
	req.Print = func(outcomes []source.Outcome) error { shown = outcomes; return nil }
	result, err := run.Analyze(t.Context(), req)
	if err != nil || len(result.Merged.MergedFrom) != 1 || result.Merged.MergedFrom[0].Name != "deadset-go" {
		t.Fatalf("Analyze() = %+v, %v, want the Go analyzer's report alone", result, err)
	}
	if len(shown) != 2 || !shown[0].Omitted || !slices.Equal(shown[0].LeftOut, []vocab.Language{vocab.TS}) {
		t.Errorf("Analyze() showed %+v, want the first analyzer left out with ts alone", shown)
	}
	if n, m := len(recorders["go-and-ts"].runs), len(recorders["deadset-go"].runs); n != 1 || m != 2 {
		t.Errorf("the analyzer left out ran %d times and the Go one %d, want once and twice", n, m)
	}
}

// noProgram is what an analyzer that found no program prints.
const noProgram = vocab.SetupFailurePrefix + "no-program: the target holds no JavaScript file a program is formed from\n"

// leftOutTypeScript is the recorders of a run whose TypeScript analyzer finds
// no program, the Go analyzer naming the first consumer consumer where its
// scope names none.
func leftOutTypeScript(consumer string) map[string]*recorder {
	return map[string]*recorder{
		"deadset-go": {name: "deadset-go", languages: []vocab.Language{vocab.Go}, consumer: consumer, report: "vectors/merge/two-reports-no-edges/inputs/00-go.json"},
		"deadset-ts": {name: "deadset-ts", languages: []vocab.Language{vocab.TS}, exit: vocab.ExitFailure, printed: noProgram},
	}
}

// A declared consumer that holds only TypeScript, which a run whose census
// decided its languages leaves out, is left out with it: the run reports the
// Go analyzer's findings, the line naming TypeScript left out names the
// consumer, and no plan hands it to an analyzer, whether the Go analyzer runs
// again or not. A run whose analysis.languages names TypeScript ends with its
// no-program.
func TestAnalyzeLeavesOutAConsumerOfOnlyALeftOutLanguage(t *testing.T) {
	t.Parallel()

	const providers = `"providers": {"analyzers": [
	{"name": "deadset-go", "command": "deadset-go", "languages": ["go"]},
	{"name": "deadset-ts", "command": "deadset-ts", "languages": ["ts"]}]}`
	detected := `{"target": {"kind": "application"}, ` + providers + `}`
	for _, c := range []struct {
		name, module, document string
		rerun, configured      bool
	}{
		{name: "module-below-the-root", module: "services/api/go.mod", document: detected, rerun: true},
		{name: "module-at-the-root", module: "go.mod", document: detected},
		{name: "configured", module: "go.mod", document: `{"target": {"kind": "application"}, "analysis": {"languages": ["go", "ts"]}, ` + providers + `}`, configured: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			base := t.TempDir()
			target, consumer := filepath.Join(base, "app"), filepath.Join(base, "web")
			writeFile(t, filepath.Join(target, filepath.FromSlash(c.module)), "module example.com/api\n")
			writeFile(t, filepath.Join(target, ".gitignore"), "web/app.js\n")
			writeFile(t, filepath.Join(target, "web", "app.js"), "export const x = 1;\n")
			writeFile(t, filepath.Join(consumer, "tsconfig.json"), "{}")
			writeFile(t, filepath.Join(consumer, "src", "index.ts"), "export const y = 1;\n")
			recorders := leftOutTypeScript("")
			declared := &scope.Document{Target: scope.Module{Path: target}, Consumers: []scope.Module{{Path: consumer}}}
			req := request(t, target, c.document, declared, recorders)
			var shown []source.Outcome
			req.Print = func(outcomes []source.Outcome) error { shown = outcomes; return nil }
			result, err := run.Analyze(t.Context(), req)
			if c.configured {
				if held, ok := errors.AsType[*failure.Error](err); !ok || held.Failures[0].Class != vocab.SetupNoProgram || result.Merged != nil {
					t.Errorf("Analyze(TypeScript configured) = %+v, %v, want the setup failure no-program and no merged report", result, err)
				}
				return
			}
			if err != nil || len(result.Merged.MergedFrom) != 1 || result.Merged.MergedFrom[0].Name != "deadset-go" {
				t.Fatalf("Analyze() = %+v, %v, want the Go analyzer's report alone", result, err)
			}
			ts := slices.IndexFunc(shown, func(o source.Outcome) bool { return o.Analyzer == "deadset-ts" })
			if ts < 0 || !shown[ts].Omitted || !slices.Equal(shown[ts].Dropped, []string{consumer}) {
				t.Errorf("Analyze() showed %+v, want the TypeScript analyzer left out with the consumer %s", shown, consumer)
			}
			goRuns := recorders["deadset-go"].runs
			if want := map[bool]int{false: 1, true: 2}[c.rerun]; len(goRuns) != want {
				t.Fatalf("the Go analyzer ran %d times, want %d", len(goRuns), want)
			}
			if c.rerun {
				var narrowed struct{ Consumers []json.RawMessage }
				body, err := os.ReadFile(goRuns[1].Scope)
				if err != nil || json.Unmarshal(body, &narrowed) != nil || len(narrowed.Consumers) != 0 {
					t.Errorf("the Go analyzer ran again under %s holding %d consumers, %v, want none", goRuns[1].Scope, len(narrowed.Consumers), err)
				}
			}
		})
	}
}

// A run that leaves TypeScript out runs the Go analyzer again where that
// renames a consumer alone, the target's name unchanged: a consumer whose
// package.json names it, and whose only Go module sits below it, is named by
// no file of the Go row once TypeScript is out, so the merged report names it
// by the Go analyzer's load, as a run with no JavaScript does.
func TestAnalyzeRunsAgainTheSourceALeftOutLanguageNamedAConsumerFor(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	target, consumer := filepath.Join(base, "app"), filepath.Join(base, "web")
	for path, body := range map[string]string{
		"app/go.mod": "module example.com/app\n", "app/.gitignore": "web/app.js\n", "app/web/app.js": "export const x = 1;\n",
		"web/package.json": `{"name": "@example/web"}`, "web/tools/go.mod": "module example.com/web/tools\n",
		"web/.gitignore": "*.js\n", "web/dist/app.js": "export const y = 1;\n",
	} {
		writeFile(t, filepath.Join(base, filepath.FromSlash(path)), body)
	}
	recorders := leftOutTypeScript("example.com/web/tools")
	declared := &scope.Document{Target: scope.Module{Path: target}, Consumers: []scope.Module{{Path: consumer}}}
	req := request(t, target, `{"target": {"kind": "application"}, "providers": {"analyzers": [
	{"name": "deadset-go", "command": "deadset-go", "languages": ["go"]},
	{"name": "deadset-ts", "command": "deadset-ts", "languages": ["ts"]}]}}`, declared, recorders)
	result, err := run.Analyze(t.Context(), req)
	if err != nil || len(result.Merged.MergedFrom) != 1 {
		t.Fatalf("Analyze() = %+v, %v, want the Go analyzer's report alone", result, err)
	}
	goRuns := recorders["deadset-go"].runs
	if len(goRuns) != 2 || goRuns[1].Scope != filepath.Join(req.RunDir, "rerun", "scope.json") {
		t.Fatalf("the Go analyzer ran with %+v, want a second run under the narrowed scope", goRuns)
	}
	for i := range goRuns {
		if id := targetID(t, goRuns[i].Scope); id != "example.com/app" {
			t.Errorf("run %d of the Go analyzer names the target %q, want example.com/app both times", i+1, id)
		}
	}
	loaded := result.Merged.Consumers.Loaded
	if len(loaded) != 1 || loaded[0].ID != "example.com/web/tools" {
		t.Errorf("the merged report loaded the consumers %+v, want example.com/web/tools, as a run with no JavaScript names it", loaded)
	}
}

// When a source run again after TypeScript is left out fails, the run ends with
// the class line that source printed, its other lines shown under its name, as
// on a first run: its no-program then is no second omission, though another Go
// analyzer run again reports. No report is merged, and each Go analyzer ran
// once over each plan.
func TestAnalyzeEndsTheRunWithTheFailureOfASourceRunAgain(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	writeFile(t, filepath.Join(target, "services", "api", "go.mod"), "module example.com/api\n")
	writeFile(t, filepath.Join(target, "web", "app.js"), "export const x = 1;\n")
	const goReport = "vectors/merge/two-reports-no-edges/inputs/00-go.json"
	const detail = "the target holds no package this build loads: name the languages to analyze"
	recorders := map[string]*recorder{
		"deadset-go": {
			name: "deadset-go", languages: []vocab.Language{vocab.Go}, report: goReport,
			later: &recorder{exit: vocab.ExitFailure, printed: "loading the narrowed scope\n" + vocab.SetupFailurePrefix + "no-program: " + detail + "\n"},
		},
		"other-go":   {name: "other-go", languages: []vocab.Language{vocab.Go}, report: goReport},
		"deadset-ts": {name: "deadset-ts", languages: []vocab.Language{vocab.TS}, exit: vocab.ExitFailure, printed: noProgram},
	}
	req := request(t, target, `{"target": {"kind": "application"}, "providers": {"analyzers": [
	{"name": "deadset-go", "command": "deadset-go", "languages": ["go"]},
	{"name": "other-go", "command": "other-go", "languages": ["go"]},
	{"name": "deadset-ts", "command": "deadset-ts", "languages": ["ts"]}]}}`, &scope.Document{Target: scope.Module{Path: target}}, recorders)
	var shown []source.Outcome
	req.Print = func(outcomes []source.Outcome) error { shown = outcomes; return nil }
	result, err := run.Analyze(t.Context(), req)
	held, ok := errors.AsType[*failure.Error](err)
	if !ok || !slices.Equal(held.Failures, []failure.Failure{{Class: vocab.SetupNoProgram, Detail: detail}}) || result == nil || result.Merged != nil {
		t.Fatalf("Analyze() = %+v, %v, want the Go analyzer's own no-program as the one failure and no merged report", result, err)
	}
	if _, err := os.Stat(filepath.Join(req.RunDir, "report.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Analyze() wrote a merged report: %v", err)
	}
	failed := slices.IndexFunc(shown, func(o source.Outcome) bool { return o.Analyzer == "deadset-go" })
	if failed < 0 || shown[failed].Err == nil || shown[failed].Omitted || string(shown[failed].Printed) != "deadset-go: loading the narrowed scope\n" {
		t.Errorf("Analyze() showed %+v, want the Go analyzer's second run failed, its other line under its name", shown)
	}
	rerun := filepath.Join(req.RunDir, "rerun")
	for _, name := range []string{"deadset-go", "other-go"} {
		runs := recorders[name].runs
		if len(runs) != 2 || filepath.Dir(runs[0].Report) != req.RunDir || filepath.Dir(runs[1].Report) != rerun {
			t.Errorf("%s ran with %+v, want one run in the run directory and one in %s", name, runs, rerun)
		}
	}
	if n := len(recorders["deadset-ts"].runs); n != 1 {
		t.Errorf("the TypeScript analyzer ran %d times, want once", n)
	}
}

// A run directory that exists is a document the run cannot write.
func TestAnalyzeRefusesARunDirectoryThatExists(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	writeFile(t, filepath.Join(target, "go.mod"), "module example.com/app\n")
	req := request(t, target, twoAnalyzers, &scope.Document{Target: scope.Module{Path: target}}, map[string]*recorder{"deadset-go": {name: "deadset-go"}})
	req.RunDir = t.TempDir()
	_, err := run.Analyze(t.Context(), req)
	if held, ok := errors.AsType[*failure.Error](err); !ok || held.Failures[0].Class != vocab.RunUnwrittenOutput || !errors.Is(err, fs.ErrExist) {
		t.Errorf("Analyze(an existing run directory) = %v, want the run failure unwritten-output and fs.ErrExist", err)
	}
}
