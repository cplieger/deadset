package invoke_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/deadset/internal/invoke"
	"github.com/cplieger/deadset/internal/report"
)

// TestAnalyzeReadsTheReportOfEveryVerdict pins that each exit code that is a
// verdict about a complete report lets the report be read, and that the report
// returned is the document the analyzer wrote.
func TestAnalyzeReadsTheReportOfEveryVerdict(t *testing.T) {
	t.Parallel()

	written := published(t, oneReport)
	want, err := report.Decode(written)
	if err != nil {
		t.Fatalf("Setup: decode %s: %v", oneReport, err)
	}
	for _, exit := range []int{0, 1, 4} {
		t.Run(strconv.Itoa(exit), func(t *testing.T) {
			t.Parallel()

			req := request(t, "deadset-go", fakeAnalyzer(t, exit, written).command)
			got, err := invoke.Analyze(t.Context(), &req)
			if err != nil {
				t.Fatalf("Analyze(an analyzer exiting %d over a written report) = %v, want the report", exit, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Analyze(an analyzer exiting %d) = %+v, want the report it wrote, %+v", exit, got, want)
			}
		})
	}
}

// TestAnalyzeRunsTheAnalyzeVerbWithTheThreePaths pins the command line the
// analyzer CLI contract gives the analyze verb, and the directory the
// analyzer runs in.
func TestAnalyzeRunsTheAnalyzeVerbWithTheThreePaths(t *testing.T) {
	t.Parallel()

	analyzer := fakeAnalyzer(t, 0, published(t, oneReport))
	req := request(t, "deadset-go", analyzer.command)
	if _, err := invoke.Analyze(t.Context(), &req); err != nil {
		t.Fatalf("Analyze() = %v, want the report", err)
	}

	recorded, err := os.ReadFile(analyzer.args)
	if err != nil {
		t.Fatalf("read the arguments the fake analyzer recorded: %v", err)
	}
	got := strings.Split(strings.TrimSuffix(string(recorded), "\n"), "\n")
	want := []string{"analyze", "--scope=" + req.Scope, "--config=" + req.Config, "--report=" + req.Report}
	if !slices.Equal(got, want) {
		t.Errorf("Analyze(%+v) ran the analyzer with %q, want %q", req, got, want)
	}

	ranIn, err := os.ReadFile(analyzer.dir)
	if err != nil {
		t.Fatalf("read the directory the fake analyzer recorded: %v", err)
	}
	if got := strings.TrimSuffix(string(ranIn), "\n"); got != req.Dir {
		t.Errorf("Analyze(%+v) ran the analyzer in %q, want %q", req, got, req.Dir)
	}
}

// TestAnalyzeReadsNoReportAfterAnExitThatCarriesNone pins that the exit code is
// read before the report is opened: an analyzer that refused its invocation,
// failed, or exited with a code the contract does not define is refused with
// its code, and the truncated document it left is never decoded.
func TestAnalyzeReadsNoReportAfterAnExitThatCarriesNone(t *testing.T) {
	t.Parallel()

	for _, exit := range []int{2, 3, 5} {
		t.Run(strconv.Itoa(exit), func(t *testing.T) {
			t.Parallel()

			req := request(t, "deadset-go", fakeAnalyzer(t, exit, truncated(t)).command)
			got, err := invoke.Analyze(t.Context(), &req)
			if got != nil {
				t.Errorf("Analyze(an analyzer exiting %d) = %+v, want no report", exit, got)
			}
			refused, ok := errors.AsType[*invoke.Error](err)
			if !ok {
				t.Fatalf("Analyze(an analyzer exiting %d) = %v, want an *invoke.Error", exit, err)
			}
			if !errors.Is(err, invoke.ErrExited) || refused.Exit != exit {
				t.Errorf("Analyze(an analyzer exiting %d) = %v with exit %d, want ErrExited with exit %d", exit, err, refused.Exit, exit)
			}
			if _, decoded := errors.AsType[*report.Error](err); decoded {
				t.Errorf("Analyze(an analyzer exiting %d) = %v, want the report never decoded", exit, err)
			}
			if refused.Analyzer != req.Analyzer || refused.Report != req.Report {
				t.Errorf("Analyze() refused analyzer %q and report %q, want %q and %q", refused.Analyzer, refused.Report, req.Analyzer, req.Report)
			}
		})
	}
}

// TestAnalyzeRefusesAnAbsentReport pins that a verdict with no report behind it
// is a refusal, not an empty result.
func TestAnalyzeRefusesAnAbsentReport(t *testing.T) {
	t.Parallel()

	req := request(t, "deadset-go", fakeAnalyzer(t, 0, nil).command)
	got, err := invoke.Analyze(t.Context(), &req)
	if got != nil || !errors.Is(err, invoke.ErrNoReport) {
		t.Fatalf("Analyze(an analyzer exiting 0 with no report) = %+v, %v, want no report and ErrNoReport", got, err)
	}
	if refused, _ := errors.AsType[*invoke.Error](err); refused == nil || refused.Exit != 0 {
		t.Errorf("Analyze(an analyzer exiting 0 with no report) = %v, want an *invoke.Error naming exit 0", err)
	}
	for _, named := range []string{req.Analyzer, req.Report} {
		if !strings.Contains(err.Error(), named) {
			t.Errorf("Analyze() = %q, want the message to name %q", err, named)
		}
	}
}

// TestAnalyzeRefusesAReportThatDoesNotDecode pins that a truncated report and a
// report carrying an undeclared member are each refused naming the entry, the
// path, the JSON Pointer of the value at fault and the reason.
func TestAnalyzeRefusesAReportThatDoesNotDecode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		written func(*testing.T) []byte
		name    string
		pointer string
	}{
		{name: "truncated", written: truncated, pointer: ""},
		{name: "undeclared-member", written: withUndeclaredMember, pointer: "/" + undeclared},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			req := request(t, "deadset-ts", fakeAnalyzer(t, 1, c.written(t)).command)
			got, err := invoke.Analyze(t.Context(), &req)
			if got != nil {
				t.Errorf("Analyze(a %s report) = %+v, want no report", c.name, got)
			}
			decoded, ok := errors.AsType[*report.Error](err)
			if !ok {
				t.Fatalf("Analyze(a %s report) = %v, want an error carrying the *report.Error", c.name, err)
			}
			if decoded.Pointer != c.pointer {
				t.Errorf("Analyze(a %s report) refuses the value at %q, want %q", c.name, decoded.Pointer, c.pointer)
			}
			if refused, _ := errors.AsType[*invoke.Error](err); refused == nil || refused.Exit != 1 {
				t.Errorf("Analyze(a %s report) = %v, want an *invoke.Error naming exit 1", c.name, err)
			}
			for _, named := range []string{req.Analyzer, req.Report, decoded.Error()} {
				if !strings.Contains(err.Error(), named) {
					t.Errorf("Analyze(a %s report) = %q, want the message to name %q", c.name, err, named)
				}
			}
		})
	}
}

// TestAnalyzeRefusesAFileAlreadyAtTheReportPath pins that a report left at the
// path before the run is never read as the analyzer's, and that the analyzer
// is not run over it.
func TestAnalyzeRefusesAFileAlreadyAtTheReportPath(t *testing.T) {
	t.Parallel()

	analyzer := fakeAnalyzer(t, 0, published(t, oneReport))
	req := request(t, "deadset-go", analyzer.command)
	if err := os.WriteFile(req.Report, published(t, oneReport), 0o600); err != nil {
		t.Fatalf("Setup: write %s: %v", req.Report, err)
	}

	got, err := invoke.Analyze(t.Context(), &req)
	if got != nil || !errors.Is(err, invoke.ErrReportExists) {
		t.Errorf("Analyze(over an existing report) = %+v, %v, want no report and ErrReportExists", got, err)
	}
	if _, err := os.Stat(analyzer.args); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("after Analyze(over an existing report), Stat(%s) = %v, want the analyzer never run", analyzer.args, err)
	}
}

// TestAnalyzeRefusesAnAnalyzerItCannotRun pins that a command that cannot be
// started is a refusal naming no exit code.
func TestAnalyzeRefusesAnAnalyzerItCannotRun(t *testing.T) {
	t.Parallel()

	req := request(t, "deadset-go", filepath.Join(t.TempDir(), "absent"))
	got, err := invoke.Analyze(t.Context(), &req)
	if got != nil || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Analyze(an absent command) = %+v, %v, want no report and an error satisfying errors.Is(err, fs.ErrNotExist)", got, err)
	}
	if refused, _ := errors.AsType[*invoke.Error](err); refused == nil || refused.Exit != -1 {
		t.Errorf("Analyze(an absent command) = %v, want an *invoke.Error naming exit -1", err)
	}
}

// TestAnalyzeRefusesACommandThatIsNotAnAbsolutePath pins that a request whose
// command would be looked up on PATH is refused without running anything, so
// the analysis runs the file the handshake ran and no other.
func TestAnalyzeRefusesACommandThatIsNotAnAbsolutePath(t *testing.T) {
	t.Parallel()

	analyzer := fakeAnalyzer(t, 0, published(t, oneReport))
	req := request(t, "deadset-go", "")
	relative, err := filepath.Rel(req.Dir, analyzer.command)
	if err != nil {
		t.Fatalf("Setup: Rel(%s, %s): %v", req.Dir, analyzer.command, err)
	}
	req.Command = relative
	got, err := invoke.Analyze(t.Context(), &req)
	if got != nil || !errors.Is(err, invoke.ErrUnresolvedCommand) {
		t.Fatalf("Analyze(command %q) = %+v, %v, want no report and ErrUnresolvedCommand", req.Command, got, err)
	}
	if _, err := os.Stat(analyzer.args); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("after Analyze(command %q), Stat(%s) = %v, want the analyzer never run", req.Command, analyzer.args, err)
	}
}

// TestAnalyzeInterruptsTheAnalyzerOfACancelledRun pins that cancelling the run
// interrupts the analyzer rather than killing it, and that the report and the
// verdict it answers the interrupt with are not read: the run was cancelled,
// so it has no report.
func TestAnalyzeInterruptsTheAnalyzerOfACancelledRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	document := filepath.Join(dir, "written.json")
	if err := os.WriteFile(document, published(t, oneReport), 0o600); err != nil {
		t.Fatalf("Setup: write %s: %v", document, err)
	}
	started, interrupted := filepath.Join(dir, "started"), filepath.Join(dir, "interrupted")
	analyzer := script(t, dir, strings.Join([]string{
		"document='" + document + "'",
		"interrupted='" + interrupted + "'",
		"sleep 60 & sleeper=$!",
		`trap 'kill "$sleeper"; cp "$document" "$report"; : > "$interrupted"; exit 1' INT`,
		": > '" + started + "'",
		`wait "$sleeper"`,
	}, "\n"))
	req := request(t, "deadset-go", analyzer.command)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		defer cancel()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(started); err == nil {
				return
			}
		}
	}()

	got, err := invoke.Analyze(ctx, &req)
	if got != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("Analyze(a cancelled run) = %+v, %v, want no report and an error satisfying errors.Is(err, context.Canceled)", got, err)
	}
	if _, err := os.Stat(interrupted); err != nil {
		t.Errorf("after Analyze(a cancelled run), Stat(%s) = %v, want the analyzer to have received an interrupt", interrupted, err)
	}
}

// TestAnalyzeCopiesWhatTheAnalyzerPrints pins that the analyzer's stderr, and
// the stdout the contract leaves empty, reach Diagnostics.
func TestAnalyzeCopiesWhatTheAnalyzerPrints(t *testing.T) {
	t.Parallel()

	analyzer := script(t, t.TempDir(), "echo 'to stdout'\necho 'to stderr' >&2\nexit 3\n")
	req := request(t, "deadset-go", analyzer.command)
	var printed bytes.Buffer
	req.Diagnostics = &printed

	if _, err := invoke.Analyze(t.Context(), &req); !errors.Is(err, invoke.ErrExited) {
		t.Fatalf("Analyze(an analyzer exiting 3) = %v, want ErrExited", err)
	}
	for _, line := range []string{"to stdout\n", "to stderr\n"} {
		if !strings.Contains(printed.String(), line) {
			t.Errorf("Analyze() copied %q to Diagnostics, want it to hold %q", printed.String(), line)
		}
	}
}

// errDiagnostics is the error every write to a refusingWriter returns.
var errDiagnostics = errors.New("the diagnostics writer refuses every write")

// refusingWriter is a Diagnostics whose every write fails.
type refusingWriter struct{}

func (refusingWriter) Write([]byte) (int, error) { return 0, errDiagnostics }

// TestAnalyzeRefusesARunWhoseOutputCannotBeCopied pins that a Diagnostics
// that refuses what the analyzer prints fails the run whatever the analyzer
// exits with: no report is read, even after a verdict, and the refusal names
// the write's error and the analyzer's exit code.
func TestAnalyzeRefusesARunWhoseOutputCannotBeCopied(t *testing.T) {
	t.Parallel()

	for _, exit := range []int{0, 1, 2, 3, 4} {
		t.Run(strconv.Itoa(exit), func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			document := filepath.Join(dir, "written.json")
			if err := os.WriteFile(document, published(t, oneReport), 0o600); err != nil {
				t.Fatalf("Setup: write %s: %v", document, err)
			}
			analyzer := script(t, dir, "echo 'a diagnostic' >&2\ncp '"+document+"' \"$report\"\nexit "+strconv.Itoa(exit)+"\n")
			req := request(t, "deadset-go", analyzer.command)
			req.Diagnostics = refusingWriter{}

			got, err := invoke.Analyze(t.Context(), &req)
			if got != nil || !errors.Is(err, errDiagnostics) {
				t.Errorf("Analyze(an analyzer exiting %d, its output refused) = %+v, %v, want no report and the write's error", exit, got, err)
			}
			if refused, _ := errors.AsType[*invoke.Error](err); refused == nil || refused.Exit != exit {
				t.Errorf("Analyze(an analyzer exiting %d, its output refused) = %v, want an *invoke.Error naming exit %d", exit, err, exit)
			}
		})
	}
}

// TestAnalyzeNamesTheWriteErrorWhenTheAnalyzerDiesOfIt pins that an analyzer
// still printing after its output was refused, which the closed pipe then
// stops with a signal, is refused naming the write's error rather than the
// signal, with exit -1.
func TestAnalyzeNamesTheWriteErrorWhenTheAnalyzerDiesOfIt(t *testing.T) {
	t.Parallel()

	analyzer := script(t, t.TempDir(), "while :; do echo 'a diagnostic' >&2; done\n")
	req := request(t, "deadset-go", analyzer.command)
	req.Diagnostics = refusingWriter{}

	got, err := invoke.Analyze(t.Context(), &req)
	if got != nil || !errors.Is(err, errDiagnostics) {
		t.Errorf("Analyze(an analyzer printing until its output is refused) = %+v, %v, want no report and the write's error", got, err)
	}
	if refused, _ := errors.AsType[*invoke.Error](err); refused == nil || refused.Exit != -1 {
		t.Errorf("Analyze(an analyzer printing until its output is refused) = %v, want an *invoke.Error naming exit -1", err)
	}
}
