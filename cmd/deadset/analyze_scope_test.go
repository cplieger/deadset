package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/deadset/internal/verdict"
)

// scopeRead is the part of a scope document these tests compare.
type scopeRead struct {
	Target    scopeModuleRead   `json:"target"`
	Consumers []scopeModuleRead `json:"consumers"`
}

// scopeModuleRead is one module of a scope document as these tests read it.
type scopeModuleRead struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// consumerIDs is the id of every consumer a scope document names, empty
// where it names none.
func consumerIDs(read scopeRead) []string {
	ids := make([]string, 0, len(read.Consumers))
	for _, consumer := range read.Consumers {
		ids = append(ids, consumer.ID)
	}
	return ids
}

// readScopeDocument decodes the scope document at path.
func readScopeDocument(t *testing.T, path string) scopeRead {
	t.Helper()

	var read scopeRead
	body, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(body, &read) != nil {
		t.Fatalf("read the scope document %s: %v\n%s", path, err, body)
	}
	return read
}

// consumerPaths is the path of every consumer a scope document names.
func consumerPaths(read scopeRead) []string {
	paths := make([]string, 0, len(read.Consumers))
	for _, consumer := range read.Consumers {
		paths = append(paths, consumer.Path)
	}
	return paths
}

// mixedWorkspace is a directory holding a target with Go and TypeScript in it
// whose provider list names a fake for each language, and three consumers
// beside it: one Go module, one TypeScript project, and one holding both. It
// returns the directory, the target and the two fakes.
func mixedWorkspace(t *testing.T) (base, target string, goFake, tsFake fake) {
	t.Helper()

	goFake = fakeAnalyzer(t, "deadset-go", []string{"go"}, 1, goFindings)
	tsFake = fakeAnalyzer(t, "deadset-ts", []string{"ts"}, 1, tsFindings)
	target, _ = goTarget(t, []provider{
		{Name: "deadset-go", Command: goFake.command, Languages: []string{"go"}},
		{Name: "deadset-ts", Command: tsFake.command, Languages: []string{"ts"}},
	}, "")
	base = filepath.Dir(target)
	writeFile(t, filepath.Join(target, "tsconfig.json"), []byte("{}\n"))
	writeFile(t, filepath.Join(base, "go-consumer", "go.mod"), []byte("module example.com/goconsumer\n"))
	writeFile(t, filepath.Join(base, "ts-consumer", "tsconfig.json"), []byte("{}\n"))
	writeFile(t, filepath.Join(base, "both", "go.mod"), []byte("module example.com/both\n"))
	writeFile(t, filepath.Join(base, "both", "tsconfig.json"), []byte("{}\n"))
	return base, target, goFake, tsFake
}

// A run over a scope document routes each declared consumer to every analyzer
// claiming a language the consumer holds and to no other: the run's scope
// document names every consumer in the declared order, an analyzer handed only
// some of them reads a scope document of its own naming those, and every
// analyzer runs in the deepest directory holding the target and every
// consumer.
func TestAnalyzeRoutesEachConsumerToTheAnalyzersOfItsLanguages(t *testing.T) {
	t.Parallel()

	base, target, goFake, tsFake := mixedWorkspace(t)
	document := filepath.Join(base, "scope.json")
	writeFile(t, document, []byte(`{"target": {"path": "target"}, "consumers": [{"path": "go-consumer"}, {"path": "ts-consumer"}, {"path": "both"}]}`))
	runDir := filepath.Join(base, "run")

	got := analyze(t, target, runDir, "--scope="+document)
	if got.code != verdict.Findings {
		t.Fatalf("analyze --scope=%s = %d, want %d\nstderr: %s", document, got.code, verdict.Findings, got.stderr)
	}
	want := []string{
		"config.deadset-go.json", "config.deadset-ts.json", "describe.deadset-go.json", "describe.deadset-ts.json",
		"report.deadset-go.json", "report.deadset-ts.json", "report.json",
		"scope.deadset-go.json", "scope.deadset-ts.json", "scope.json",
	}
	if names := listing(t, runDir); !slices.Equal(names, want) {
		t.Errorf("the run directory holds %q, want %q", names, want)
	}

	run := readScopeDocument(t, filepath.Join(runDir, "scope.json"))
	every := []string{filepath.Join(base, "go-consumer"), filepath.Join(base, "ts-consumer"), filepath.Join(base, "both")}
	if run.Target.Path != target || !slices.Equal(consumerPaths(run), every) {
		t.Errorf("scope.json names the target %s and the consumers %q, want %s and %q", run.Target.Path, consumerPaths(run), target, every)
	}
	for _, c := range []struct {
		analyzer fake
		name     string
		handed   []string
	}{
		{analyzer: goFake, name: "deadset-go", handed: []string{every[0], every[2]}},
		{analyzer: tsFake, name: "deadset-ts", handed: []string{every[1], every[2]}},
	} {
		own, err := os.ReadFile(filepath.Join(runDir, "scope."+c.name+".json"))
		if err != nil {
			t.Fatalf("read scope.%s.json: %v", c.name, err)
		}
		read, err := os.ReadFile(c.analyzer.scope)
		if err != nil || string(read) != string(own) {
			t.Errorf("%s read the scope document\n%s\n(%v), want scope.%s.json\n%s", c.name, read, err, c.name, own)
		}
		if handed := consumerPaths(readScopeDocument(t, c.analyzer.scope)); !slices.Equal(handed, c.handed) {
			t.Errorf("%s was handed the consumers %q, want %q", c.name, handed, c.handed)
		}
		ranIn, err := os.ReadFile(c.analyzer.ranIn)
		if err != nil || strings.TrimSpace(string(ranIn)) != base {
			t.Errorf("%s ran in %q (%v), want %s, the directory holding the target and every consumer", c.name, ranIn, err, base)
		}
	}
}

// An analyzer whose languages claim every declared consumer reads the run's
// own scope document, and no other is written.
func TestAnalyzeHandsAnAnalyzerClaimingEveryConsumerTheRunsScope(t *testing.T) {
	t.Parallel()

	f := fakeAnalyzer(t, "deadset-go", []string{"go"}, 1, goFindings)
	target, runDir := goTarget(t, []provider{{Name: "deadset-go", Command: f.command, Languages: []string{"go"}}}, "")
	base := filepath.Dir(target)
	writeFile(t, filepath.Join(base, "consumer", "go.mod"), []byte("module example.com/consumer\n"))
	document := filepath.Join(base, "scope.json")
	writeFile(t, document, []byte(`{"target": {"path": "target"}, "consumers": [{"id": "example.com/consumer", "path": "consumer"}]}`))

	got := analyze(t, target, runDir, "--scope="+document)
	if got.code != verdict.Findings {
		t.Fatalf("analyze --scope=%s = %d, want %d\nstderr: %s", document, got.code, verdict.Findings, got.stderr)
	}
	want := []string{"config.deadset-go.json", "describe.deadset-go.json", "report.deadset-go.json", "report.json", "scope.json"}
	if names := listing(t, runDir); !slices.Equal(names, want) {
		t.Errorf("the run directory holds %q, want %q", names, want)
	}
	run, err := os.ReadFile(filepath.Join(runDir, "scope.json"))
	if err != nil {
		t.Fatalf("read scope.json: %v", err)
	}
	read, err := os.ReadFile(f.scope)
	if err != nil || string(read) != string(run) {
		t.Errorf("deadset-go read the scope document\n%s\n(%v), want scope.json\n%s", read, err, run)
	}
	wantRun := `{
  "target": {
    "role": "target",
    "path": "` + target + `"
  },
  "consumers": [
    {
      "id": "example.com/consumer",
      "role": "consumer",
      "path": "` + filepath.Join(base, "consumer") + `"
    }
  ]
}
`
	if string(run) != wantRun {
		t.Errorf("scope.json =\n%s\nwant\n%s", run, wantRun)
	}
}

// A run given no scope document, and one given a document naming the target
// alone, writes the scope document naming the target alone, byte for byte,
// and runs its analyzer in the target root.
func TestAnalyzeWithNoConsumerWritesTheTargetAlone(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name     string
		document string
	}{
		{name: "no-document"},
		{name: "target-alone", document: `{"target": {"path": "target"}, "consumers": []}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := fakeAnalyzer(t, "deadset-go", []string{"go"}, 1, goFindings)
			target, runDir := goTarget(t, []provider{{Name: "deadset-go", Command: f.command, Languages: []string{"go"}}}, "")
			var extra []string
			if c.document != "" {
				document := filepath.Join(filepath.Dir(target), "scope.json")
				writeFile(t, document, []byte(c.document))
				extra = append(extra, "--scope="+document)
			}

			if got := analyze(t, target, runDir, extra...); got.code != verdict.Findings {
				t.Fatalf("analyze(%s) = %d, want %d\nstderr: %s", c.name, got.code, verdict.Findings, got.stderr)
			}
			run, err := os.ReadFile(filepath.Join(runDir, "scope.json"))
			want := "{\n  \"target\": {\n    \"role\": \"target\",\n    \"path\": \"" + target + "\"\n  }\n}\n"
			if err != nil || string(run) != want {
				t.Errorf("analyze(%s) wrote scope.json =\n%s\n(%v), want\n%s", c.name, run, err, want)
			}
			if ranIn, err := os.ReadFile(f.ranIn); err != nil || strings.TrimSpace(string(ranIn)) != target {
				t.Errorf("analyze(%s) ran the analyzer in %q (%v), want the target root %s", c.name, ranIn, err, target)
			}
		})
	}
}

// Every way a scope document or a consumer it declares ends the run before any
// analyzer runs, each with its exit code, what it names, and no run
// directory left behind.
func TestAnalyzeRefusesAScopeItCannotRoute(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		document string
		setup    func(t *testing.T, base string)
		code     int
		names    []string
	}{
		{
			name:  "document-absent",
			code:  verdict.Failure,
			names: []string{"--scope", "scope.json", "no such file"},
		},
		{
			name:     "member-undeclared",
			document: `{"target": {"path": "target"}, "network": true}`,
			code:     verdict.Failure,
			names:    []string{"--scope", "/network", "declares no such member"},
		},
		{
			name:     "another-target",
			document: `{"target": {"path": "go-consumer"}}`,
			code:     verdict.Usage,
			names:    []string{"--scope", "go-consumer", "the target of the run"},
		},
		{
			name:     "consumer-absent",
			document: `{"target": {"path": "target"}, "consumers": [{"path": "go-consumer"}, {"path": "gone"}]}`,
			code:     verdict.Failure,
			names:    []string{"cannot be read", "no finding is computed without it", "gone"},
		},
		{
			name:     "consumer-a-file",
			document: `{"target": {"path": "target"}, "consumers": [{"path": "go-consumer/go.mod"}]}`,
			code:     verdict.Failure,
			names:    []string{"go.mod that --scope declares is not a directory"},
		},
		{
			name:     "consumer-of-no-language",
			document: `{"target": {"path": "target"}, "consumers": [{"path": "docs"}]}`,
			setup: func(t *testing.T, base string) {
				writeFile(t, filepath.Join(base, "docs", "README.md"), []byte("# docs\n"))
			},
			code:  verdict.Failure,
			names: []string{"--scope", "docs", "does not load", "no language in scope"},
		},
		{
			name:     "consumer-no-analyzer-claims",
			document: `{"target": {"path": "target"}, "consumers": [{"path": "go-consumer"}, {"path": "ts-consumer"}]}`,
			code:     verdict.Usage,
			names:    []string{"--scope", "ts-consumer holds ts", "no analyzer of the run claims"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := fakeAnalyzer(t, "deadset-go", []string{"go"}, 1, goFindings)
			target, runDir := goTarget(t, []provider{{Name: "deadset-go", Command: f.command, Languages: []string{"go"}}}, "")
			base := filepath.Dir(target)
			writeFile(t, filepath.Join(base, "go-consumer", "go.mod"), []byte("module example.com/goconsumer\n"))
			writeFile(t, filepath.Join(base, "ts-consumer", "tsconfig.json"), []byte("{}\n"))
			if c.setup != nil {
				c.setup(t, base)
			}
			document := filepath.Join(base, "scope.json")
			if c.document != "" {
				writeFile(t, document, []byte(c.document))
			}

			got := analyze(t, target, runDir, "--scope="+document)
			if got.code != c.code {
				t.Errorf("analyze(%s) = %d, want %d\nstderr: %s", c.name, got.code, c.code, got.stderr)
			}
			for _, named := range c.names {
				if !strings.Contains(got.stderr, named) {
					t.Errorf("analyze(%s) stderr = %q, want it to name %s", c.name, got.stderr, named)
				}
			}
			if _, err := os.Stat(runDir); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("analyze(%s) left the run directory %s: %v, want none", c.name, runDir, err)
			}
			if _, err := os.Stat(f.ranIn); !errors.Is(err, fs.ErrNotExist) || got.stdout != "" {
				t.Errorf("analyze(%s) ran the analyzer or printed %q, want neither", c.name, got.stdout)
			}
		})
	}
}

// A module the run loads in both languages is named in the run's scope
// document by the module path of the Go module holding it, which is what every
// analyzer's report of it then carries, unless the declared document names it;
// a module the run loads in one language, and one no go.mod governs, is
// named by nothing, and its analyzer's load names it.
func TestAnalyzeNamesAModuleLoadedInBothLanguagesByItsGoModulePath(t *testing.T) {
	t.Parallel()

	everyConsumer := `"consumers": [{"path": "go-consumer"}, {"path": "ts-consumer"}, {"path": "both"}]`
	for _, c := range []struct {
		name     string
		document string
		flags    []string
		setup    func(t *testing.T, base, target string)
		target   string
		ids      []string
	}{
		{name: "target-alone", target: "example.com/app"},
		{
			name: "consumers", document: `{"target": {"path": "target"}, ` + everyConsumer + `}`,
			target: "example.com/app", ids: []string{"", "", "example.com/both"},
		},
		{
			name:     "declared-names-kept",
			document: `{"target": {"id": "@example/app", "path": "target"}, "consumers": [{"id": "@example/both", "path": "both"}]}`,
			target:   "@example/app", ids: []string{"@example/both"},
		},
		{
			name: "one-language", document: `{"target": {"path": "target"}, "consumers": [{"path": "go-consumer"}, {"path": "both"}]}`,
			flags: []string{"--languages=go"}, ids: []string{"", ""},
		},
		{
			name: "module-above-the-target",
			setup: func(t *testing.T, base, target string) {
				removeFile(t, filepath.Join(target, "go.mod"))
				writeFile(t, filepath.Join(target, "main.go"), []byte("package main\n"))
				writeFile(t, filepath.Join(base, "go.mod"), []byte("module example.com/base\n"))
			},
			target: "example.com/base",
		},
		{
			name: "no-go-module",
			setup: func(t *testing.T, _, target string) {
				removeFile(t, filepath.Join(target, "go.mod"))
				for above := filepath.Dir(target); ; above = filepath.Dir(above) {
					if _, err := os.Stat(filepath.Join(above, "go.mod")); err == nil {
						t.Skipf("the temporary directory %s is inside the module at %s", target, above)
					}
					if filepath.Dir(above) == above {
						break
					}
				}
				writeFile(t, filepath.Join(target, "main.go"), []byte("package main\n"))
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			base, target, _, _ := mixedWorkspace(t)
			if c.setup != nil {
				c.setup(t, base, target)
			}
			flags := slices.Clone(c.flags)
			if c.document != "" {
				document := filepath.Join(base, "scope.json")
				writeFile(t, document, []byte(c.document))
				flags = append(flags, "--scope="+document)
			}
			runDir := filepath.Join(base, "run")

			if got := analyze(t, target, runDir, flags...); got.code != verdict.Findings {
				t.Fatalf("analyze(%s) = %d, want %d\nstderr: %s", c.name, got.code, verdict.Findings, got.stderr)
			}
			run := readScopeDocument(t, filepath.Join(runDir, "scope.json"))
			if run.Target.ID != c.target || !slices.Equal(consumerIDs(run), c.ids) {
				t.Errorf("analyze(%s) wrote scope.json naming the target %q and the consumers %q, want %q and %q",
					c.name, run.Target.ID, consumerIDs(run), c.target, c.ids)
			}
		})
	}
}

// A run loading the target in both languages whose go.mod holds no module
// path ends as a failure naming the file, before any analyzer runs.
func TestAnalyzeRefusesAMixedTargetWhoseGoModNamesNoModule(t *testing.T) {
	t.Parallel()

	_, target, goFake, tsFake := mixedWorkspace(t)
	writeFile(t, filepath.Join(target, "go.mod"), []byte("go 1.27\n"))
	runDir := filepath.Join(filepath.Dir(target), "run")

	got := analyze(t, target, runDir)
	if got.code != verdict.Failure || !strings.Contains(got.stderr, filepath.Join(target, "go.mod")) || !strings.Contains(got.stderr, "no module directive") {
		t.Errorf("analyze = %d with stderr %q, want %d naming %s and its missing module directive",
			got.code, got.stderr, verdict.Failure, filepath.Join(target, "go.mod"))
	}
	if _, err := os.Stat(runDir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("analyze left the run directory %s: %v, want none", runDir, err)
	}
	for _, f := range []fake{goFake, tsFake} {
		if _, err := os.Stat(f.ranIn); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("analyze ran the analyzer %s, want no analyzer run", f.command)
		}
	}
}

// removeFile removes one file a test's setup wrote.
func removeFile(t *testing.T, path string) {
	t.Helper()

	if err := os.Remove(path); err != nil {
		t.Fatalf("Setup: remove %s: %v", path, err)
	}
}
