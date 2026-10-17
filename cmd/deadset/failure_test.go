package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/plan"
	"github.com/cplieger/deadset/v2/internal/rundir"
	"github.com/cplieger/deadset/v2/internal/source"
)

// classLines is every line of stderr, ended by a line feed or a carriage
// return, that a group prefix of failure_classes opens after any white space.
func classLines(stderr string) []string {
	var held []string
	for _, line := range strings.FieldsFunc(stderr, func(r rune) bool { return r == '\n' || r == '\r' }) {
		opened := strings.TrimLeftFunc(line, unicode.IsSpace)
		if strings.HasPrefix(opened, vocab.SetupFailurePrefix) || strings.HasPrefix(opened, vocab.RunFailurePrefix) {
			held = append(held, line)
		}
	}
	return held
}

var forgeries = map[string]string{
	"doubled-prefix":  "run failure: run failure: product-fault: a doubled prefix\n",
	"tripled-prefix":  "setup failure: run failure: setup failure: missing-module: a tripled prefix\n",
	"indented-prefix": " \t\u00a0run failure: product-fault: a prefix after white space\n",
	"multi-line":      "the generator said:\r\n  run failure: product-fault: nested\rrun failure: product-fault: after a carriage return\n",
}

// analyzerOutputHolds fails t unless what the analyzer printed opens no class
// line of its own: a run it ends with exit code 3, relayed or leaving no
// report, holds exactly the one deadset prints, and a run presenting its
// report none.
func analyzerOutputHolds(t *testing.T, printed string) {
	t.Helper()

	const (
		relayed   = "setup failure: missing-module: main.go:3: example.com/app/gen names a module nothing provides; run go generate\n"
		tsRelayed = "setup failure: missing-module: src/a.ts:1: ./gen names a module nothing provides; run the generator\n"
	)
	for _, c := range []struct {
		stubs       map[string]stub
		name, class string
	}{
		{name: "relayed", stubs: map[string]stub{"deadset-go": {exit: 3, err: exited("deadset-go", 3), printed: relayed + printed}}, class: "setup failure: missing-module: main.go:3: "},
		{name: "no-report", stubs: map[string]stub{"deadset-go": {exit: 1, err: exited("deadset-go", 1), printed: printed}}, class: "run failure: product-fault: analyzer deadset-go"},
		{name: "reporting", stubs: map[string]stub{"deadset-go": {report: goFindings, exit: 1, printed: printed}}},
		{
			name: "two-relayed", class: "setup failure: missing-module: main.go:3: ",
			stubs: map[string]stub{
				"deadset-go": {exit: 3, err: exited("deadset-go", 3), printed: relayed + printed},
				"deadset-ts": {exit: 3, err: exited("deadset-ts", 3), printed: printed + tsRelayed + printed},
			},
		},
	} {
		providers := []provider{both[0]}
		if len(c.stubs) > 1 {
			providers = both
		}
		target, runDir := goTarget(t, providers, "")
		if len(c.stubs) > 1 {
			writeFile(t, filepath.Join(target, "tsconfig.json"), []byte("{}\n"))
		}
		got := analyzeWith(t, stubs(t, c.stubs), target, runDir)
		switch {
		case c.class == "":
			if held := classLines(got.stderr); got.code == vocab.ExitFailure || len(held) != 0 {
				t.Errorf("analyze(%s, printed %q) = %d, class lines %q, want a verdict and no class line", c.name, printed, got.code, held)
			}
		case got.code != vocab.ExitFailure:
			t.Errorf("analyze(%s, printed %q) = %d, want %d\nstderr: %s", c.name, printed, got.code, vocab.ExitFailure, got.stderr)
		default:
			holdsFailureLines(t, c.name+" "+strconv.Quote(printed), got.stderr, []string{c.class})
		}
	}
}

func TestAnalyzerOutputOpensNoClassLine(t *testing.T) {
	t.Parallel()

	for name, printed := range forgeries {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			analyzerOutputHolds(t, printed)
		})
	}
}

func TestRandomAnalyzerOutputOpensNoClassLine(t *testing.T) {
	t.Parallel()

	pieces := []string{
		vocab.SetupFailurePrefix, vocab.RunFailurePrefix, vocab.SetupMissingModule + ": ", vocab.RunProductFault + ": ",
		"setup failure:", "run failure", "deadset: ", "deadset-go: ", "x", " ", "\t", "\u00a0", "\u3000", "\v", "\f", "\n", "\r", "\r\n",
	}
	random := rand.New(rand.NewPCG(4, 3))
	for range 1000 {
		var printed strings.Builder
		for range random.IntN(12) {
			printed.WriteString(pieces[random.IntN(len(pieces))])
		}
		analyzerOutputHolds(t, printed.String())
	}
}

func FuzzAnalyzerOutputOpensNoClassLine(f *testing.F) {
	for _, printed := range forgeries {
		f.Add(printed)
	}
	f.Fuzz(analyzerOutputHolds)
}

// holdsFailureLines fails t unless stderr holds exactly one class line, the
// one want's first entry opens, and after it a line each further entry opens,
// in want's order.
func holdsFailureLines(t *testing.T, name, stderr string, want []string) {
	t.Helper()

	if held := classLines(stderr); len(held) != 1 || !strings.HasPrefix(held[0], want[0]) {
		t.Errorf("%s: class lines %q, want exactly one, opened by %q\nstderr: %s", name, held, want[0], stderr)
		return
	}
	lines := slices.Collect(strings.Lines(stderr))
	at := slices.IndexFunc(lines, func(line string) bool { return strings.HasPrefix(line, want[0]) })
	for _, further := range want[1:] {
		next := slices.IndexFunc(lines[at+1:], func(line string) bool { return strings.HasPrefix(line, further) })
		if next < 0 {
			t.Errorf("%s: no line opened by %q after %q\nstderr: %s", name, further, lines[at], stderr)
			return
		}
		at += next + 1
	}
}

// Every run that ends with exit code 3 prints exactly one class line, the
// first failure's with its group's prefix, and every further failure on a
// line of its own with no prefix; the analyzers' own class lines are relayed
// in the provider list's order.
func TestEveryFailurePrintsOneClassLine(t *testing.T) {
	t.Parallel()

	goOnly := []provider{both[0]}
	withTS := func(providers []provider) func(t *testing.T) (string, string, []string) {
		return func(t *testing.T) (string, string, []string) {
			target, runDir := goTarget(t, providers, "")
			writeFile(t, filepath.Join(target, "tsconfig.json"), []byte("{}\n"))
			return target, runDir, nil
		}
	}
	goRun := func(t *testing.T) (string, string, []string) {
		target, runDir := goTarget(t, goOnly, "")
		return target, runDir, nil
	}
	const (
		tsPrinted = "loading the program\n" +
			"setup failure: missing-module: src/a.ts:1: ./gen/one names a module nothing provides; run the generator\n" +
			"missing-module: src/a.ts:2: ./gen/two names a module nothing provides; run the generator\n"
		goPrinted = "setup failure: missing-module: main.go:3: example.com/app/gen names a module nothing provides; run go generate\n"
	)
	for _, c := range []struct {
		setup func(t *testing.T) (target, runDir string, extra []string)
		cmd   func(t *testing.T) *command
		stubs map[string]stub
		name  string
		lines []string
	}{
		{
			name: "target-absent",
			setup: func(t *testing.T) (string, string, []string) {
				return filepath.Join(t.TempDir(), "absent"), filepath.Join(t.TempDir(), "run"), nil
			},
			lines: []string{"setup failure: no-program: "},
		},
		{
			name: "shared-document-unreadable", stubs: map[string]stub{"deadset-go": {report: goFindings, exit: 1}},
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, goOnly, "")
				if err := os.Mkdir(filepath.Join(target, "deadset-ignore.json"), 0o750); err != nil {
					t.Fatalf("Setup: %v", err)
				}
				return target, runDir, nil
			},
			lines: []string{"setup failure: unreadable-document: deadset-ignore.json"},
		},
		{name: "analyzer-command-absent", setup: goRun, lines: []string{"run failure: missing-analyzer: analyzer deadset-go"}},
		{
			name: "two-analyzer-commands-absent", setup: withTS(both),
			lines: []string{"run failure: missing-analyzer: analyzer deadset-go", "missing-analyzer: analyzer deadset-ts"},
		},
		{
			name: "run-directory-parent-absent", stubs: map[string]stub{"deadset-go": {report: goFindings, exit: 1}},
			setup: func(t *testing.T) (string, string, []string) {
				target, _ := goTarget(t, goOnly, "")
				return target, filepath.Join(t.TempDir(), "absent", "run"), nil
			},
			lines: []string{"run failure: unwritten-output: "},
		},
		{
			name: "run-directory-exists", stubs: map[string]stub{"deadset-go": {report: goFindings, exit: 1}},
			setup: func(t *testing.T) (string, string, []string) {
				target, _ := goTarget(t, goOnly, "")
				return target, t.TempDir(), nil
			},
			lines: []string{"run failure: unwritten-output: "},
		},
		{
			name: "rendering-that-cannot-read-its-source", stubs: map[string]stub{"deadset-go": {report: goFindings, exit: 1}},
			setup: func(t *testing.T) (string, string, []string) {
				target, runDir := goTarget(t, goOnly, "")
				return target, runDir, []string{"--formats=sarif"}
			},
			lines: []string{"run failure: unwritten-output: "},
		},
		{
			name: "analyzer-failing-with-no-class-line", setup: goRun,
			stubs: map[string]stub{"deadset-go": {exit: 3, err: exited("deadset-go", 3), printed: "the analyzer stopped\n"}},
			lines: []string{"run failure: product-fault: analyzer deadset-go "},
		},
		{
			name: "analyzer-exiting-with-no-contract-code", setup: goRun,
			stubs: map[string]stub{"deadset-go": {exit: 5, err: exited("deadset-go", 5)}},
			lines: []string{"run failure: product-fault: analyzer deadset-go"},
		},
		{
			name: "analyzer-printing-a-class-line-and-no-report", setup: goRun,
			stubs: map[string]stub{"deadset-go": {exit: 1, err: exited("deadset-go", 1), printed: goPrinted}},
			lines: []string{"run failure: product-fault: analyzer deadset-go"},
		},
		{
			name: "pending-finding-unresolved", setup: goRun,
			stubs: map[string]stub{"deadset-go": {report: goPending, exit: 4}},
			lines: []string{"run failure: unresolved-edge: "},
		},
		{
			name: "two-analyzers-failing", setup: withTS([]provider{both[1], both[0]}),
			stubs: map[string]stub{
				"deadset-ts": {exit: 3, err: exited("deadset-ts", 3), printed: tsPrinted},
				"deadset-go": {exit: 3, err: exited("deadset-go", 3), printed: goPrinted},
			},
			lines: []string{
				"setup failure: missing-module: src/a.ts:1: ",
				"missing-module: src/a.ts:2: ",
				"missing-module: main.go:3: ",
			},
		},
		{
			name: "first-listed-failing-without-the-failure-code", setup: withTS(both),
			stubs: map[string]stub{
				"deadset-go": {exit: 5, err: exited("deadset-go", 5)},
				"deadset-ts": {exit: 3, err: exited("deadset-ts", 3), printed: tsPrinted},
			},
			lines: []string{"run failure: product-fault: analyzer deadset-go", "missing-module: src/a.ts:1: "},
		},
		{
			name: "first-listed-ending-with-the-failure-code", setup: withTS([]provider{both[1], both[0]}),
			stubs: map[string]stub{
				"deadset-go": {exit: 5, err: exited("deadset-go", 5)},
				"deadset-ts": {exit: 3, err: exited("deadset-ts", 3), printed: tsPrinted},
			},
			lines: []string{"setup failure: missing-module: src/a.ts:1: ", "missing-module: src/a.ts:2: ", "product-fault: analyzer deadset-go"},
		},
		{
			name: "error-no-class-names", setup: goRun,
			cmd: func(*testing.T) *command {
				return &command{open: func(*plan.Source, string, io.Writer) (source.Source, error) {
					return nil, errors.New("the opener broke")
				}}
			},
			lines: []string{"run failure: product-fault: the opener broke"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			target, runDir, extra := c.setup(t)
			cmd := stubs(t, c.stubs)
			if c.cmd != nil {
				cmd = c.cmd(t)
			}
			got := analyzeWith(t, cmd, target, runDir, extra...)
			if got.code != vocab.ExitFailure || got.stdout != "" {
				t.Errorf("analyze(%s) = %d, stdout %q, want %d and nothing presented\nstderr: %s", c.name, got.code, got.stdout, vocab.ExitFailure, got.stderr)
			}
			holdsFailureLines(t, c.name, got.stderr, c.lines)
			if c.name == "two-analyzers-failing" && !strings.Contains(got.stderr, "deadset-ts: loading the program\n") {
				t.Errorf("analyze(%s) stderr = %q, want what the analyzer printed beside its failures", c.name, got.stderr)
			}
		})
	}
}

// An answer a verb cannot write to standard output is a document the run
// cannot write.
func TestAVerbWhoseAnswerCannotBeWrittenFailsWithUnwrittenOutput(t *testing.T) {
	t.Parallel()

	target, _ := goTarget(t, []provider{both[0]}, "")
	for _, args := range [][]string{{"version"}, {"print-config", "--target=" + target}} {
		var stderr bytes.Buffer
		code := (&command{}).dispatch(args, refusingWriter{}, &stderr)
		if code != vocab.ExitFailure {
			t.Errorf("dispatch(%q) = %d, want %d\nstderr: %s", args, code, vocab.ExitFailure, stderr.String())
		}
		holdsFailureLines(t, args[0], stderr.String(), []string{"run failure: unwritten-output: "})
	}
}

// A scope document the scope schema refuses is a command line the run
// refuses.
func TestAScopeDocumentTheSchemaRefusesEndsWithTheUsageCode(t *testing.T) {
	t.Parallel()

	target, runDir := goTarget(t, []provider{both[0]}, "")
	document := filepath.Join(filepath.Dir(target), "scope.json")
	writeFile(t, document, []byte(`{"target": {"path": "target"}, "network": true}`))
	got := analyzeWith(t, stubs(t, map[string]stub{"deadset-go": {report: goFindings, exit: 1}}), target, runDir, "--scope="+document)
	if got.code != vocab.ExitUsage || !strings.Contains(got.stderr, `"network"`) || len(classLines(got.stderr)) != 0 {
		t.Errorf("analyze(a refused scope document) = %d, stderr %q, want %d naming the member and no class line", got.code, got.stderr, vocab.ExitUsage)
	}
}

// interrupting is a source that interrupts the run while it analyzes and
// waits for the run to stop it.
type interrupting struct{ name string }

func (s *interrupting) Name() string { return s.name }

func (*interrupting) Admit(_ context.Context, kept rundir.Entry, _ []string) error {
	return kept.WriteDescribe([]byte("{}"))
}

func (s *interrupting) Produce(ctx context.Context, _ source.Files) source.Outcome {
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		return source.Outcome{Analyzer: s.name, Err: err, Exit: -1}
	}
	<-ctx.Done()
	return source.Outcome{Analyzer: s.name, Err: context.Cause(ctx), Exit: -1}
}

// An interrupt while an analyzer runs stops the run with the class naming the
// signal. It is not parallel: the interrupt reaches the whole process.
func TestAnInterruptedRunFailsWithInterruptedRun(t *testing.T) {
	target, runDir := goTarget(t, []provider{both[0]}, "")
	cmd := &command{open: func(planned *plan.Source, _ string, _ io.Writer) (source.Source, error) {
		return &interrupting{name: planned.Entry.Name}, nil
	}}
	got := analyzeWith(t, cmd, target, runDir)
	if got.code != vocab.ExitFailure || !strings.Contains(got.stderr, "interrupt") {
		t.Errorf("analyze(interrupted) = %d, stderr %q, want %d naming the interrupt", got.code, got.stderr, vocab.ExitFailure)
	}
	holdsFailureLines(t, "interrupted", got.stderr, []string{"run failure: interrupted-run: "})
}

// Not parallel: SIGINT reaches the whole process.
func TestAnInterruptKeepsAnEarlierAnalyzerFailure(t *testing.T) {
	target, runDir := goTarget(t, both, "")
	writeFile(t, filepath.Join(target, "tsconfig.json"), []byte("{}\n"))
	printed := "setup failure: missing-module: main.go:3: example.com/app/gen names a module nothing provides; run go generate\n"
	failing := stubs(t, map[string]stub{"deadset-go": {exit: 3, err: exited("deadset-go", 3), printed: printed}})
	cmd := &command{open: func(planned *plan.Source, dir string, diagnostics io.Writer) (source.Source, error) {
		if planned.Entry.Name == "deadset-ts" {
			return &interrupting{name: planned.Entry.Name}, nil
		}
		return failing.open(planned, dir, diagnostics)
	}}
	got := analyzeWith(t, cmd, target, runDir)
	if got.code != vocab.ExitFailure {
		t.Errorf("analyze(interrupted after a failure) = %d, want %d\nstderr: %s", got.code, vocab.ExitFailure, got.stderr)
	}
	holdsFailureLines(t, "interrupted-after-a-failure", got.stderr, []string{"setup failure: missing-module: main.go:3: ", "interrupted-run: "})
}

// Not parallel: SIGINT reaches the whole process.
func TestAnAnalyzerEndingAnInterruptedRunWithItsClassLineKeepsIt(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	analyzer := scriptedAnalyzer(t, dir, "deadset-go", []string{"go"}, strings.Join([]string{
		"sleep 60 & sleeper=$!",
		`trap 'kill "$sleeper"; echo "run failure: interrupted-run: deadset-go stopped on an interrupt; run it again" >&2; exit 3' INT`,
		": > '" + started + "'",
		`wait "$sleeper"`,
	}, "\n"))
	target, runDir := goTarget(t, []provider{{Name: "deadset-go", Command: analyzer, Languages: []string{"go"}}}, "")
	go func() {
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(started); err == nil {
				_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
				return
			}
		}
	}()
	got := analyzeWith(t, &command{}, target, runDir)
	if got.code != vocab.ExitFailure {
		t.Errorf("analyze(an analyzer ending the interrupt with 3) = %d, want %d\nstderr: %s", got.code, vocab.ExitFailure, got.stderr)
	}
	holdsFailureLines(t, "analyzer-interrupted", got.stderr, []string{"run failure: interrupted-run: deadset-go stopped", "interrupted-run: the run stopped: interrupt"})
}
