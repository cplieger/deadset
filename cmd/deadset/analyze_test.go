package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	spec "github.com/cplieger/deadset-spec/v8"
	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/plan"
	"github.com/cplieger/deadset/v2/internal/rundir"
	"github.com/cplieger/deadset/v2/internal/source"
)

// The analyzer reports the stand-ins write, each a published merge input.
const (
	goFindings = "vectors/merge/one-report/inputs/00-go.json"
	goPending  = "vectors/merge/pending-pair-unevaluated/inputs/00-go.json"
	tsFindings = "vectors/merge/two-reports-no-edges/inputs/01-ts.json"
	goWarnOnly = "vectors/merge/fail-on-warn/inputs/00-go.json"
)

// writeFile writes one file a test's setup needs.
func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("Setup: create the directory of %s: %v", path, err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("Setup: write %s: %v", path, err)
	}
}

// provider is one entry of a provider list.
type provider struct {
	Name      string   `json:"name"`
	Command   string   `json:"command"`
	Languages []string `json:"languages"`
}

// goTarget is a target tree holding a Go module and the repository
// configuration naming the target kind, the given provider list and any extra
// members, and the path of a run directory beside it that does not exist yet.
func goTarget(t *testing.T, providers []provider, extra string) (target, runDir string) {
	t.Helper()

	base := t.TempDir()
	target = filepath.Join(base, "target")
	list, err := json.Marshal(providers)
	if err != nil {
		t.Fatalf("Setup: encode the provider list: %v", err)
	}
	writeFile(t, filepath.Join(target, "go.mod"), []byte("module example.com/app\n\ngo 1.27\n"))
	writeFile(t, filepath.Join(target, "deadset.json"),
		[]byte(`{"target": {"kind": "application"}, "providers": {"analyzers": `+string(list)+`}`+extra+`}`))
	return target, filepath.Join(base, "run")
}

// both is the provider list of a Go and a TypeScript analyzer.
var both = []provider{
	{Name: "deadset-go", Command: "deadset-go", Languages: []string{"go"}},
	{Name: "deadset-ts", Command: "deadset-ts", Languages: []string{"ts"}},
}

// analyzeRun is what one analyze invocation returned and printed.
type analyzeRun struct {
	stdout, stderr string
	code           int
}

// analyze runs the analyze verb over target into runDir with extra flags,
// opening the real analyzers.
func analyze(t *testing.T, target, runDir string, extra ...string) analyzeRun {
	t.Helper()

	return analyzeWith(t, &command{}, target, runDir, extra...)
}

func analyzeWith(t *testing.T, c *command, target, runDir string, extra ...string) analyzeRun {
	t.Helper()

	var stdout, stderr bytes.Buffer
	args := append([]string{"analyze", "--target=" + target, "--run-dir=" + runDir}, extra...)
	code := c.dispatch(args, &stdout, &stderr)
	return analyzeRun{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// mergedReport decodes the merged report a run wrote.
func mergedReport(t *testing.T, runDir string) *report.Report {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(runDir, "report.json"))
	if err != nil {
		t.Fatalf("read the merged report: %v", err)
	}
	r, err := report.Decode(body)
	if err != nil {
		t.Fatalf("Decode(the merged report) = %v, want a report the schemas admit", err)
	}
	return r
}

// stub is a source a test states: what its handshake answers, the published
// report it writes and returns, what it prints, its exit code and the
// refusal of its read.
type stub struct {
	admit   error
	err     error
	report  string
	printed string
	exit    int
}

// stubs is a command opening, per provider entry name, the stub it names; an
// entry it names none for resolves to no executable.
func stubs(t *testing.T, held map[string]stub) *command {
	t.Helper()

	return &command{open: func(planned *plan.Source, _ string, _ io.Writer) (source.Source, error) {
		s, ok := held[planned.Entry.Name]
		if !ok {
			return nil, &source.CommandError{Err: fs.ErrNotExist, Name: planned.Entry.Name, Index: planned.Index}
		}
		return &stubSource{t: t, stub: s, name: planned.Entry.Name, languages: planned.Languages}, nil
	}}
}

type stubSource struct {
	t         *testing.T
	name      string
	languages []vocab.Language
	stub
}

func (s *stubSource) Name() string { return s.name }

func (s *stubSource) Admit(_ context.Context, kept rundir.Entry, _ []string) error {
	if s.admit != nil {
		return s.admit
	}
	return kept.WriteDescribe([]byte("{}"))
}

func (s *stubSource) Produce(_ context.Context, files source.Files) source.Outcome {
	out := source.Outcome{
		Analyzer: s.name, Languages: s.languages, Printed: []byte(s.printed), Exit: s.exit, Err: s.err,
		Digest: "sha256:" + strings.Repeat("0", 64),
	}
	if s.report != "" {
		body, err := spec.Vectors.ReadFile(s.report)
		if err != nil {
			s.t.Errorf("Setup: read %s: %v", s.report, err)
		}
		if err := os.WriteFile(files.Report, body, 0o600); err != nil {
			s.t.Errorf("Setup: write %s: %v", files.Report, err)
		}
		if out.Err == nil {
			out.Report, out.Err = report.Decode(body)
		}
	}
	return out
}

// exited is the refusal of an analyzer that exited with code.
func exited(name string, code int) error {
	return &source.Error{Err: fmt.Errorf("exited %d: %w", code, source.ErrExited), Analyzer: name, Exit: code}
}

// Every way a run ends before or after its merged report exists, each with
// its exit code, what it names, and what stderr must not hold.
func TestAnalyzeExitCodes(t *testing.T) {
	t.Parallel()

	goOnly := []provider{both[0]}
	cases := []struct {
		setup  func(t *testing.T) (target, runDir string, extra []string)
		stubs  map[string]stub
		name   string
		names  []string
		absent []string
		code   int
	}{
		{
			name: "findings-exit-code-off", stubs: map[string]stub{"deadset-go": {report: goFindings, exit: 1}},
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, goOnly, "")
				return target, runDir, []string{"--exit-code=off"}
			},
			code: vocab.ExitClean, names: []string{"the verdict of this run is 1"},
		},
		{
			name: "exit-code-value-outside-its-set",
			setup: func(t *testing.T) (string, string, []string) {
				return t.TempDir(), filepath.Join(t.TempDir(), "run"), []string{"--exit-code=maybe"}
			},
			code: vocab.ExitUsage, names: []string{"-exit-code", `"maybe"`, "on and off", "usage: deadset analyze"},
		},
		{
			name: "argument",
			setup: func(t *testing.T) (string, string, []string) {
				return t.TempDir(), filepath.Join(t.TempDir(), "run"), []string{"extra"}
			},
			code: vocab.ExitUsage, names: []string{`"extra"`},
		},
		{
			name: "no-target-kind",
			setup: func(t *testing.T) (string, string, []string) {
				target := t.TempDir()
				writeFile(t, filepath.Join(target, "go.mod"), []byte("module example.com/app\n"))
				return target, filepath.Join(t.TempDir(), "run"), nil
			},
			code: vocab.ExitUsage, names: []string{"target.kind"},
		},
		{
			name: "missing-target",
			setup: func(t *testing.T) (string, string, []string) {
				return filepath.Join(t.TempDir(), "absent"), filepath.Join(t.TempDir(), "run"), nil
			},
			code: vocab.ExitFailure, names: []string{"the target"},
		},
		{
			name: "unreadable-central-configuration",
			setup: func(t *testing.T) (string, string, []string) {
				return t.TempDir(), filepath.Join(t.TempDir(), "run"), []string{"--central=" + t.TempDir()}
			},
			code: vocab.ExitUsage, names: []string{"--central", "is a directory"},
		},
		{
			name: "unreadable-repository-configuration",
			setup: func(t *testing.T) (string, string, []string) {
				target := t.TempDir()
				if err := os.Mkdir(filepath.Join(target, "deadset.json"), 0o750); err != nil {
					t.Fatalf("Setup: %v", err)
				}
				return target, filepath.Join(t.TempDir(), "run"), nil
			},
			code: vocab.ExitUsage, names: []string{"deadset.json", "not a regular file"},
		},
		{
			name: "template-format-without-a-template",
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, goOnly, "")
				return target, runDir, []string{"--formats=text,template"}
			},
			code: vocab.ExitUsage, names: []string{"--template", "template format"},
		},
		{
			name: "template-that-does-not-parse",
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, goOnly, "")
				template := filepath.Join(t.TempDir(), "report.tmpl")
				writeFile(t, template, []byte("{{range .findings}}"))
				return target, runDir, []string{"--formats=template", "--template=" + template}
			},
			code: vocab.ExitUsage, names: []string{"--template", "parse the template"},
		},
		{
			name: "no-program",
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, goOnly, "")
				if err := os.Remove(filepath.Join(target, "go.mod")); err != nil {
					t.Fatalf("Setup: %v", err)
				}
				writeFile(t, filepath.Join(target, "package.json"), []byte("{}"))
				writeFile(t, filepath.Join(target, "src", "a.ts"), []byte("export const a = 1;\n"))
				return target, runDir, nil
			},
			code: vocab.ExitFailure, names: []string{"setup failure: no-program: no file under", "tsconfig*.json"}, absent: []string{"deadset: "},
		},
		{
			name: "unclaimed-language",
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, []provider{both[1]}, "")
				return target, runDir, nil
			},
			code: vocab.ExitUsage, names: []string{"providers.analyzers", `"go"`},
		},
		{
			name: "existing-run-directory", stubs: map[string]stub{"deadset-go": {report: goFindings, exit: 1}},
			setup: func(t *testing.T) (string, string, []string) {
				target, _ := goTarget(t, goOnly, "")
				return target, t.TempDir(), nil
			},
			code: vocab.ExitUsage, names: []string{"--run-dir"},
		},
		{
			name: "ts-analyzer-command-absent", stubs: map[string]stub{"deadset-go": {report: goFindings, exit: 1}},
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, both, "")
				writeFile(t, filepath.Join(target, "tsconfig.json"), []byte("{}\n"))
				return target, runDir, nil
			},
			code: vocab.ExitFailure, names: []string{"deadset-ts", "providers.analyzers[1]"},
		},
		{
			name:  "handshake-refused",
			stubs: map[string]stub{"deadset-go": {admit: &source.HandshakeError{Err: source.ErrDescribedName, Analyzer: "deadset-go"}}},
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, goOnly, "")
				return target, runDir, nil
			},
			code: vocab.ExitFailure, names: []string{"deadset-go", "another name"},
		},
		{
			name:  "analyzer-refusing-its-configuration",
			stubs: map[string]stub{"deadset-go": {exit: 2, err: exited("deadset-go", 2), printed: "usage: deadset-go analyze --scope=FILE\n"}},
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, goOnly, "")
				return target, runDir, nil
			},
			code: vocab.ExitUsage, names: []string{"analyzer deadset-go", "exited 2", "usage: deadset-go analyze"}, absent: []string{"usage: deadset analyze"},
		},
		{
			name: "every-analyzer-refusing-its-configuration",
			stubs: map[string]stub{
				"deadset-go": {exit: 2, err: exited("deadset-go", 2)},
				"deadset-ts": {exit: 2, err: exited("deadset-ts", 2)},
			},
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, both, "")
				writeFile(t, filepath.Join(target, "tsconfig.json"), []byte("{}\n"))
				return target, runDir, nil
			},
			code: vocab.ExitUsage, names: []string{"analyzer deadset-go", "analyzer deadset-ts"}, absent: []string{"usage: deadset analyze"},
		},
		{
			name:  "analyzer-failing",
			stubs: map[string]stub{"deadset-go": {report: goFindings, exit: 1}, "deadset-ts": {exit: 3, err: exited("deadset-ts", 3)}},
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, both, "")
				writeFile(t, filepath.Join(target, "tsconfig.json"), []byte("{}\n"))
				return target, runDir, nil
			},
			code: vocab.ExitFailure, names: []string{"deadset-ts", "exited 3"},
		},
		{
			name: "malformed-ignore-file", stubs: map[string]stub{"deadset-go": {report: goFindings, exit: 1}},
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, goOnly, "")
				writeFile(t, filepath.Join(target, "deadset-ignore.json"), []byte(`{"ignore": [`))
				return target, runDir, nil
			},
			code: vocab.ExitUsage, names: []string{"deadset-ignore.json"}, absent: []string{"usage: deadset analyze", "run directory"},
		},
		{
			name: "baseline-row-of-another-scheme-malformed", stubs: map[string]stub{"deadset-go": {report: goFindings, exit: 1}},
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, goOnly, "")
				writeFile(t, filepath.Join(target, "deadset-baseline.json"),
					[]byte(`{"baseline": [{"code": "ds1001", "symbol": "ts://@example/app/a.ts#A", "path": "a.ts", "reason": "recorded"}]}`))
				return target, runDir, nil
			},
			code: vocab.ExitUsage, names: []string{"deadset-baseline.json", `"ds1001"`}, absent: []string{"usage: deadset analyze", "run directory"},
		},
		{
			name: "edge-naming-a-wildcard", stubs: map[string]stub{"deadset-go": {report: goFindings, exit: 1}},
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, goOnly, "")
				writeFile(t, filepath.Join(target, "deadset-edges.json"),
					[]byte(`{"edges": {"wire": {"provides": "go://example.com/app#*", "used_by": "ts://@example/app/a.ts#A"}}}`))
				return target, runDir, nil
			},
			code: vocab.ExitUsage, names: []string{"deadset-edges.json", "/edges/wire/provides"}, absent: []string{"usage: deadset analyze", "run directory"},
		},
		{
			name: "edge-identifier-written-twice", stubs: map[string]stub{"deadset-go": {report: goFindings, exit: 1}},
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, goOnly, "")
				writeFile(t, filepath.Join(target, "deadset-edges.json"), []byte(`{"edges": {`+
					`"wire": {"provides": "go://example.com/app#A", "used_by": "ts://@example/app/a.ts#A"}, `+
					`"wire": {"provides": "go://example.com/app#B", "used_by": "ts://@example/app/a.ts#B"}}}`))
				return target, runDir, nil
			},
			code: vocab.ExitUsage, names: []string{"deadset-edges.json", "wire"}, absent: []string{"usage: deadset analyze", "run directory"},
		},
		{
			name: "shared-documents-well-formed", stubs: map[string]stub{"deadset-go": {report: goFindings, exit: 1}},
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, goOnly, "")
				writeFile(t, filepath.Join(target, "deadset-ignore.json"),
					[]byte(`{"ignore": [{"code": "DS1001", "symbol": "js://example/app/a.js#A", "path": "a.js", "reason": "Its own analyzer adjudicates it."}]}`))
				writeFile(t, filepath.Join(target, "deadset-baseline.json"), []byte(`{"baseline": []}`))
				writeFile(t, filepath.Join(target, "deadset-edges.json"), []byte(`{"edges": {}}`))
				return target, runDir, nil
			},
			code: vocab.ExitFindings, names: []string{"run directory"},
		},
		{
			name: "shared-document-unreadable", stubs: map[string]stub{"deadset-go": {report: goFindings, exit: 1}},
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, goOnly, "")
				if err := os.Mkdir(filepath.Join(target, "deadset-edges.json"), 0o750); err != nil {
					t.Fatalf("Setup: %v", err)
				}
				return target, runDir, nil
			},
			code: vocab.ExitFailure, names: []string{"deadset-edges.json", "not a regular file"},
		},
		{
			name:  "pending-pair-unevaluated",
			stubs: map[string]stub{"deadset-go": {report: goPending, exit: 4}},
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, goOnly, "")
				return target, runDir, nil
			},
			code: vocab.ExitFailure, names: []string{"wire/ServerEvent", "provides", "go://example.com/app/internal/wire#ServerEvent"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			target, runDir, extra := c.setup(t)
			got := analyzeWith(t, stubs(t, c.stubs), target, runDir, extra...)
			if got.code != c.code {
				t.Errorf("analyze(%s) = %d, want %d\nstderr: %s", c.name, got.code, c.code, got.stderr)
			}
			for _, named := range c.names {
				if !strings.Contains(got.stderr, named) {
					t.Errorf("analyze(%s) stderr = %q, want it to name %s", c.name, got.stderr, named)
				}
			}
			for _, held := range c.absent {
				if strings.Contains(got.stderr, held) {
					t.Errorf("analyze(%s) stderr = %q, want no %q", c.name, got.stderr, held)
				}
			}
			produced := c.code == vocab.ExitClean || c.code == vocab.ExitFindings
			if _, err := os.Stat(filepath.Join(runDir, "report.json")); (err == nil) != produced {
				t.Errorf("analyze(%s) left a merged report: %t, want %t", c.name, err == nil, produced)
			}
			if !produced && got.stdout != "" {
				t.Errorf("analyze(%s) stdout = %q, want nothing presented as the run's result", c.name, got.stdout)
			}
		})
	}
}

// Every way a scope document or a consumer it declares ends the run before
// any analyzer runs, each with its exit code, what it names, and no run
// directory left behind.
func TestAnalyzeRefusesAScopeItCannotRoute(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name     string
		document string
		names    []string
		code     int
	}{
		{name: "document-absent", code: vocab.ExitUsage, names: []string{"--scope", "scope.json", "no such file", "usage: deadset analyze"}},
		{name: "member-undeclared", document: `{"target": {"path": "target"}, "network": true}`, code: vocab.ExitFailure, names: []string{"--scope", `"network"`}},
		{name: "another-target", document: `{"target": {"path": "go-consumer"}}`, code: vocab.ExitUsage, names: []string{"--scope", "go-consumer", "the target of the run"}},
		{
			name: "consumer-absent", document: `{"target": {"path": "target"}, "consumers": [{"path": "go-consumer"}, {"path": "gone"}]}`,
			code: vocab.ExitFailure, names: []string{"setup failure: missing-consumer: ", "gone", "cannot be read"},
		},
		{
			name: "consumer-a-file", document: `{"target": {"path": "target"}, "consumers": [{"path": "go-consumer/go.mod"}]}`,
			code: vocab.ExitFailure, names: []string{"setup failure: missing-consumer: ", "go.mod is not a directory"},
		},
		{
			name: "consumer-of-no-language", document: `{"target": {"path": "target"}, "consumers": [{"path": "docs"}]}`,
			code: vocab.ExitFailure, names: []string{"setup failure: missing-consumer: ", "docs", "no language"},
		},
		{
			name: "consumer-no-analyzer-claims", document: `{"target": {"path": "target"}, "consumers": [{"path": "go-consumer"}, {"path": "ts-consumer"}]}`,
			code: vocab.ExitUsage, names: []string{"--scope", "ts-consumer holds ts", "no analyzer of the run claims"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			target, runDir := goTarget(t, []provider{both[0]}, "")
			base := filepath.Dir(target)
			writeFile(t, filepath.Join(base, "go-consumer", "go.mod"), []byte("module example.com/goconsumer\n"))
			writeFile(t, filepath.Join(base, "ts-consumer", "tsconfig.json"), []byte("{}\n"))
			writeFile(t, filepath.Join(base, "docs", "README.md"), []byte("# docs\n"))
			document := filepath.Join(base, "scope.json")
			if c.document != "" {
				writeFile(t, document, []byte(c.document))
			}

			got := analyzeWith(t, stubs(t, map[string]stub{"deadset-go": {report: goFindings, exit: 1}}), target, runDir, "--scope="+document)
			if got.code != c.code {
				t.Errorf("analyze(%s) = %d, want %d\nstderr: %s", c.name, got.code, c.code, got.stderr)
			}
			for _, named := range c.names {
				if !strings.Contains(got.stderr, named) {
					t.Errorf("analyze(%s) stderr = %q, want it to name %s", c.name, got.stderr, named)
				}
			}
			if _, err := os.Stat(runDir); !errors.Is(err, fs.ErrNotExist) || got.stdout != "" {
				t.Errorf("analyze(%s) left the run directory (%v) or printed %q, want neither", c.name, err, got.stdout)
			}
		})
	}
}

// A warn finding fails the run only at or below a failing severity of warn,
// and only a failing run prints the remediation; the annotation follows the
// finding's severity alone.
func TestAnalyzeFailsOnAFindingAtOrAboveTheFailingSeverity(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		failOn      string
		code        int
		remediation bool
	}{
		{failOn: "deny", code: vocab.ExitClean},
		{failOn: "warn", code: vocab.ExitFindings, remediation: true},
	} {
		t.Run(c.failOn, func(t *testing.T) {
			t.Parallel()

			target, runDir := goTarget(t, []provider{both[0]}, "")
			got := analyzeWith(t, stubs(t, map[string]stub{"deadset-go": {report: goWarnOnly, exit: 1}}), target, runDir,
				"--fail-on="+c.failOn, "--formats=github,text")
			if got.code != c.code || !strings.HasPrefix(got.stdout, "::warning ") || strings.Contains(got.stdout, "remediation: ") != c.remediation {
				t.Errorf("analyze(--fail-on=%s) = %d, stdout %q, want %d, a warning annotation first and the remediation %t\nstderr: %s",
					c.failOn, got.code, got.stdout, c.code, c.remediation, got.stderr)
			}
		})
	}
}

// sourceFiles writes, below target, every source file the published reports
// the stand-ins copy name, each long enough to hold the line its finding names.
func sourceFiles(t *testing.T, target string) {
	t.Helper()

	for _, path := range []string{"internal/store/store.go", "web/src/tabs.ts", "web/src/wire.ts"} {
		var lines strings.Builder
		for line := range 64 {
			fmt.Fprintf(&lines, "// %s line %d\n", path, line+1)
		}
		writeFile(t, filepath.Join(target, filepath.FromSlash(path)), []byte(lines.String()))
	}
}

// A run naming the sarif and template formats writes each beside the merged
// report and names both paths after the summary; the SARIF log reads its line
// fingerprints below the target root, and a SARIF rendering that cannot read
// a source line fails the run after the merged report is written and before
// anything is presented as its result.
func TestAnalyzeWritesTheFileRenderingsBesideTheMergedReport(t *testing.T) {
	t.Parallel()

	held := map[string]stub{"deadset-go": {report: goFindings, exit: 1}, "deadset-ts": {report: tsFindings, exit: 1}}
	target, runDir := goTarget(t, both, "")
	writeFile(t, filepath.Join(target, "tsconfig.json"), []byte("{}\n"))
	sourceFiles(t, target)
	template := filepath.Join(t.TempDir(), "report.tmpl")
	writeFile(t, template, []byte("{{range .findings}}{{.code}} {{.position.path}}:{{.position.line}}\n{{end}}"))

	got := analyzeWith(t, stubs(t, held), target, runDir, "--formats=sarif,text,template", "--template="+template)
	if got.code != vocab.ExitFindings {
		t.Fatalf("analyze = %d, want %d\nstderr: %s", got.code, vocab.ExitFindings, got.stderr)
	}
	sarifPath, templatePath := filepath.Join(runDir, "report.json.sarif"), filepath.Join(runDir, "report.json.tmpl")
	lines := strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n")
	at := slices.IndexFunc(lines, func(line string) bool { return strings.HasPrefix(line, "summary: ") })
	if at < 0 || len(lines) < at+3 || lines[at+1] != "sarif "+sarifPath || lines[at+2] != "template "+templatePath {
		t.Errorf("analyze stdout =\n%s\nwant the summary line followed by %q and %q", got.stdout, "sarif "+sarifPath, "template "+templatePath)
	}
	if sarif, err := os.ReadFile(sarifPath); err != nil || !bytes.Contains(sarif, []byte(`"primaryLocationLineHash"`)) {
		t.Errorf("the SARIF log = %s, %v, want line fingerprints", sarif, err)
	}
	rendered, err := os.ReadFile(templatePath)
	if want := "DS1002 internal/store/store.go:41\nDS1003 web/src/tabs.ts:31\nDS1001 web/src/wire.ts:8\n"; err != nil || string(rendered) != want {
		t.Errorf("the template rendering is %q, %v, want %q", rendered, err, want)
	}

	unread, unreadRun := goTarget(t, []provider{both[0]}, "")
	failed := analyzeWith(t, stubs(t, held), unread, unreadRun, "--formats=sarif")
	if failed.code != vocab.ExitFailure || !strings.Contains(failed.stderr, "read internal/store/store.go") || failed.stdout != "" {
		t.Errorf("analyze(sources absent) = %d, stdout %q, stderr %q, want %d naming the read and no stdout", failed.code, failed.stdout, failed.stderr, vocab.ExitFailure)
	}
	if _, err := os.Stat(filepath.Join(unreadRun, "report.json")); err != nil {
		t.Errorf("analyze(sources absent) left no merged report: %v", err)
	}
}

// What each analyzer printed reaches standard error whole and in analyzer
// name order, and a setup failure of one is followed by the line naming the
// language the other was handed and that analyzer, as the two ways to
// analyze the rest; the run ends with the failure code.
func TestAnalyzePrintsWhatEachAnalyzerPrintedAndTheWorkaround(t *testing.T) {
	t.Parallel()

	const (
		setupFailure = "setup failure: missing-module: internal/gen/gen.go imports example.com/app/gen, which nothing provides; run go generate ./...\n"
		memory       = "memory exhausted: at least 9.5 GB were needed, 8.0 GB are available\n"
		workaround   = `deadset: to analyze the target without deadset-go, set analysis.languages: ["ts"] or run deadset-ts alone` + "\n"
	)
	for _, c := range []struct {
		name string
		ts   stub
		want string
	}{
		{name: "both-failing", ts: stub{exit: 3, err: exited("deadset-ts", 3), printed: memory}, want: "\n" + setupFailure + memory},
		{name: "the-other-reporting", ts: stub{report: tsFindings, exit: 1}, want: "\n" + setupFailure + workaround},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			target, runDir := goTarget(t, both, "")
			writeFile(t, filepath.Join(target, "tsconfig.json"), []byte("{}\n"))
			got := analyzeWith(t, stubs(t, map[string]stub{
				"deadset-ts": c.ts,
				"deadset-go": {exit: 3, err: exited("deadset-go", 3), printed: setupFailure},
			}), target, runDir)
			if got.code != vocab.ExitFailure || !strings.Contains(got.stderr, c.want) || got.stdout != "" {
				t.Errorf("analyze = %d, stdout %q, stderr %q, want %d with %q", got.code, got.stdout, got.stderr, vocab.ExitFailure, c.want)
			}
		})
	}
}

type refusingWriter struct{}

func (refusingWriter) Write([]byte) (int, error) {
	return 0, errors.New("the writer refuses every write")
}

func TestAnalyzeFailsWhenWhatAnAnalyzerPrintedCannotBeShown(t *testing.T) {
	t.Parallel()

	reporting := stub{report: tsFindings, exit: 1, printed: "a diagnostic\n"}
	for _, c := range []struct {
		name   string
		goStub stub
	}{
		{name: "every-analyzer-reporting", goStub: stub{report: goFindings, exit: 1, printed: "a diagnostic\n"}},
		{name: "beside-a-refusal", goStub: stub{exit: vocab.ExitUsage, err: exited("deadset-go", vocab.ExitUsage), printed: "a refusal\n"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			target, runDir := goTarget(t, both, "")
			writeFile(t, filepath.Join(target, "tsconfig.json"), []byte("{}\n"))
			cmd := stubs(t, map[string]stub{"deadset-go": c.goStub, "deadset-ts": reporting})
			var stdout bytes.Buffer
			args := []string{"analyze", "--target=" + target, "--run-dir=" + runDir}
			if code := cmd.dispatch(args, &stdout, refusingWriter{}); code != vocab.ExitFailure || stdout.Len() != 0 {
				t.Errorf("analyze(its output refused) = %d, stdout %q, want %d and nothing presented", code, stdout.String(), vocab.ExitFailure)
			}
			if _, err := os.Stat(filepath.Join(runDir, "report.json")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("analyze(its output refused) left a merged report: %v, want none", err)
			}
		})
	}
}

// A relative --run-dir names a directory below the working directory the run
// was invoked from, whatever directory the analyzers run in.
func TestAnalyzeResolvesARelativeRunDirectoryAgainstTheWorkingDirectory(t *testing.T) {
	target, runDir := goTarget(t, []provider{both[0]}, "")
	t.Chdir(filepath.Dir(runDir))

	got := analyzeWith(t, stubs(t, map[string]stub{"deadset-go": {report: goFindings, exit: 1}}), target, filepath.Base(runDir))
	if got.code != vocab.ExitFindings || !strings.Contains(got.stderr, "the run directory is "+runDir+"\n") {
		t.Errorf("analyze(--run-dir=%s) = %d, stderr %q, want %d naming %s", filepath.Base(runDir), got.code, got.stderr, vocab.ExitFindings, runDir)
	}
	if _, err := os.Stat(filepath.Join(runDir, "report.json")); err != nil {
		t.Errorf("the run directory %s holds no merged report: %v", runDir, err)
	}
}

// fakeAnalyzer writes an executable analyzer named name, claiming languages,
// whose describe verb describes it with a conformance pass and whose analyze
// verb records the directory it runs in, copies the published report written
// to the path --report names and exits with exit. It returns the command and
// the file holding the directory it ran in.
func fakeAnalyzer(t *testing.T, name string, languages []string, exit int, written string) (command, ranIn string) {
	t.Helper()

	dir := t.TempDir()
	described, err := json.Marshal(map[string]any{
		"name": name, "version": "1.0.0", "contract_version": vocab.ContractVersion,
		"schema_versions_accepted": []string{vocab.SchemaVersion}, "languages": languages,
		"conformance": map[string]string{"corpus_version": "1.9.0", "result": "pass", "digest": "sha256:" + strings.Repeat("0a", 32)},
	})
	if err != nil {
		t.Fatalf("Setup: encode the describe document: %v", err)
	}
	writeFile(t, filepath.Join(dir, "described.json"), described)
	body, err := spec.Vectors.ReadFile(written)
	if err != nil {
		t.Fatalf("Setup: read %s: %v", written, err)
	}
	writeFile(t, filepath.Join(dir, "written.json"), body)
	command, ranIn = filepath.Join(dir, "analyzer"), filepath.Join(dir, "pwd")
	script := strings.Join([]string{
		"#!/bin/sh",
		fmt.Sprintf(`if [ "$1" = describe ]; then cat '%s/described.json'; exit 0; fi`, dir),
		fmt.Sprintf(`pwd > '%s'`, ranIn),
		`for argument in "$@"; do case "$argument" in --report=*) report="${argument#--report=}" ;; esac; done`,
		fmt.Sprintf(`cp '%s/written.json' "$report"`, dir),
		"exit " + strconv.Itoa(exit),
		"",
	}, "\n")
	// A fork in a parallel test inherits the descriptor the script is written
	// through, and exec then fails with ETXTBSY until that child execs
	// (go.dev/issue/22315); a fork waits for every ForkLock reader.
	syscall.ForkLock.RLock()
	err = os.WriteFile(command, []byte(script), 0o700)
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatalf("Setup: write %s: %v", command, err)
	}
	return command, ranIn
}

// The one run through analyzer processes: every file of the evidence keyed by
// each entry's name, the merged report naming each analyzer's digest, its
// lines printed with the summary and the remediation, the merged verdict as
// the exit code, and every analyzer run in the target root, the scope's
// working directory.
func TestAnalyzeRunsEveryAnalyzerAsAProcess(t *testing.T) {
	t.Parallel()

	goCommand, goRanIn := fakeAnalyzer(t, "deadset-go", []string{"go"}, 1, goFindings)
	tsCommand, tsRanIn := fakeAnalyzer(t, "deadset-ts", []string{"ts"}, 1, tsFindings)
	target, runDir := goTarget(t, []provider{
		{Name: "deadset-go", Command: goCommand, Languages: []string{"go"}},
		{Name: "deadset-ts", Command: tsCommand, Languages: []string{"ts"}},
	}, "")
	writeFile(t, filepath.Join(target, "tsconfig.json"), []byte("{}\n"))

	got := analyze(t, target, runDir)
	if got.code != vocab.ExitFindings {
		t.Fatalf("analyze = %d, want %d\nstderr: %s", got.code, vocab.ExitFindings, got.stderr)
	}
	want := []string{
		"config.deadset-go.json", "config.deadset-ts.json", "describe.deadset-go.json", "describe.deadset-ts.json",
		"report.deadset-go.json", "report.deadset-ts.json", "report.json", "scope.json",
	}
	if names := listing(t, runDir); !slices.Equal(names, want) {
		t.Errorf("the run directory holds %q, want %q", names, want)
	}
	for _, ranIn := range []string{goRanIn, tsRanIn} {
		if dir, err := os.ReadFile(ranIn); err != nil || strings.TrimSpace(string(dir)) != target {
			t.Errorf("an analyzer ran in %q (%v), want the target root %s", dir, err, target)
		}
	}
	merged := mergedReport(t, runDir)
	for i, command := range []string{goCommand, tsCommand} {
		body, err := os.ReadFile(command)
		if err != nil {
			t.Fatalf("Setup: read %s: %v", command, err)
		}
		sum := sha256.Sum256(body)
		if digest := "sha256:" + hex.EncodeToString(sum[:]); merged.MergedFrom[i].Digest != digest {
			t.Errorf("merged_from[%d] = %+v, want the digest %s", i, merged.MergedFrom[i], digest)
		}
	}
	lines := strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n")
	if len(lines) != 3+2+1+1 || !strings.HasPrefix(lines[0], "internal/store/store.go:41:6: ") ||
		!strings.HasPrefix(lines[3], "analyzer deadset-go ") || !strings.HasPrefix(lines[5], "summary: 3 findings") || !strings.HasPrefix(lines[6], "remediation: ") {
		t.Errorf("analyze stdout =\n%s\nwant 3 finding lines, the 2 analyzer lines, the summary and the remediation", got.stdout)
	}
}

// listing is the name of every file in dir.
func listing(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
