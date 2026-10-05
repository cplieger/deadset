// Package invoke runs an analyzer as a separate process: [Describe] runs its
// describe verb and admits or refuses the analyzer from what it prints, and
// [Analyze] runs its analyze verb and reads the report the analyzer writes to
// the file the invocation names. No analyzer is linked into this program as a
// library.
//
// The read is fail-closed. The exit code is read before the report is opened,
// and only a code that is a verdict about a complete report lets the report be
// read at all. An absent report, a report that does not decode, and a
// cancelled run each leave the run with no report from that analyzer, and
// [Run] then returns no report from any analyzer. Every refusal of a read is
// an [*Error] naming the provider entry and the report path, and every refusal
// of a handshake a [*HandshakeError] naming the provider entry and its command;
// the exit code the run returns for one is the caller's to decide.
package invoke

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cplieger/deadset/internal/report"
)

// interruptGrace is how long an analyzer has to exit after a cancelled run
// interrupts it, before it is killed.
const interruptGrace = 10 * time.Second

// reportCodes are the exit codes an analyzer returns over a complete report:
// clean, findings and pending. Every other code ends the analyzer's run with
// no report a reader may use.
var reportCodes = []int{0, 1, 4}

// The refusals an invocation makes, each carried by an [*Error].
var (
	// ErrExited reports an analyzer whose exit code is not a verdict about a
	// complete report, so whatever the report path holds is not read.
	ErrExited = errors.New("the exit code carries no report")

	// ErrNoReport reports an analyzer that exited with a verdict and wrote no
	// report.
	ErrNoReport = errors.New("the analyzer wrote no report")

	// ErrReportExists reports a file already at the report path, which no read
	// could tell from a report the analyzer wrote; the analyzer is not run.
	ErrReportExists = errors.New("a file is at the report path before the analyzer ran")

	// ErrUnresolvedCommand reports a request whose command is not an absolute
	// path, which running would look up on PATH; neither verb runs it, and a
	// handshake carries it in a [*HandshakeError].
	ErrUnresolvedCommand = errors.New("the command is not the absolute path provider selection resolves")
)

// Request is one analyze invocation: the provider entry it runs, and the
// three paths the analyzer CLI contract passes.
type Request struct {
	// Diagnostics receives everything the analyzer prints, on stderr and on
	// the stdout the contract leaves empty. A nil Diagnostics discards it.
	Diagnostics io.Writer

	// Analyzer is the name of the provider entry, which every refusal names.
	Analyzer string

	// Command is the absolute path of the analyzer's executable, as provider
	// selection resolved it. Nothing here looks a command up again, so the file
	// the handshake ran is the file the analysis runs.
	Command string

	// Dir is the directory the analyzer runs in, which a report names its
	// paths relative to. An empty Dir is this process's working directory.
	Dir string

	Scope  string // the scope document, passed as --scope
	Config string // the analyzer's configuration, passed as --config
	Report string // where the analyzer writes its report, passed as --report

	// Languages are the languages of the target the run hands the analyzer.
	Languages []string
}

// Error is an invocation that left no report the run may read.
type Error struct {
	// Err is what went wrong: [ErrExited], [ErrNoReport], [ErrReportExists],
	// a [*report.Error] naming the value at fault and why, the cause of a
	// cancelled run, or the error the analyzer could not be run or read with.
	Err error

	Analyzer string // the provider entry
	Report   string // the report path

	// Exit is the analyzer's exit code, or -1 when it did not run or did not
	// exit on its own.
	Exit int
}

// Error renders the refusal as one line naming the entry and the report path.
func (e *Error) Error() string {
	return fmt.Sprintf("analyzer %s, report %s: %v", e.Analyzer, e.Report, e.Err)
}

// Unwrap is the refusal's cause.
func (e *Error) Unwrap() error { return e.Err }

// Analyze runs the analyze verb of the analyzer req names and reads the
// report it wrote. It returns the report and the analyzer's exit code, or no
// report and an [*Error].
func Analyze(ctx context.Context, req *Request) (*report.Report, int, error) {
	refuse := func(exit int, err error) (*report.Report, int, error) {
		return nil, exit, &Error{Err: err, Analyzer: req.Analyzer, Report: req.Report, Exit: exit}
	}
	if err := resolved(req.Command); err != nil {
		return refuse(-1, err)
	}
	if _, err := os.Lstat(req.Report); !errors.Is(err, fs.ErrNotExist) {
		return refuse(-1, cmp.Or(err, ErrReportExists))
	}

	cmd := command(ctx, req, req.Command, "analyze",
		"--scope="+req.Scope, "--config="+req.Config, "--report="+req.Report)
	diagnostics, copied := diagnosticsOf(req.Diagnostics)
	cmd.Stdout = diagnostics
	cmd.Stderr = diagnostics
	exit, err := wait(ctx, cmd, copied)
	switch {
	case err != nil:
		return refuse(exit, err)
	case !slices.Contains(reportCodes, exit):
		return refuse(exit, fmt.Errorf("exited %d: %w", exit, ErrExited))
	}

	data, err := os.ReadFile(req.Report)
	if errors.Is(err, fs.ErrNotExist) {
		return refuse(exit, fmt.Errorf("exited %d: %w", exit, ErrNoReport))
	}
	if err != nil {
		return refuse(exit, err)
	}
	read, err := report.Decode(data)
	if err != nil {
		return refuse(exit, err)
	}
	return read, exit, nil
}

// Run analyzes every request at once, each as its own process, and returns
// every report in the order of the requests. What each analyzer prints is held
// until every analyzer has exited and then written to its request's
// Diagnostics whole, the requests ordered by analyzer name, so two analyzers'
// lines never interleave. When any request leaves no report, it returns no
// report at all and an error joining every [*Error] in request order, so no
// analyzer's findings stand as the run's result beside another's failure. A
// setup failure's lines are followed by the [workaround] line.
func Run(ctx context.Context, requests []Request) ([]*report.Report, error) {
	outcomes := make([]outcome, len(requests))
	var running sync.WaitGroup
	for i := range requests {
		held := requests[i]
		if held.Diagnostics != nil {
			held.Diagnostics = &outcomes[i].printed
		}
		running.Go(func() { outcomes[i].read, outcomes[i].exit, outcomes[i].err = Analyze(ctx, &held) })
	}
	running.Wait()
	noteWorkarounds(requests, outcomes)

	order := make([]int, len(requests))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return strings.Compare(requests[a].Analyzer, requests[b].Analyzer) })
	for _, i := range order {
		req, done := &requests[i], &outcomes[i]
		if req.Diagnostics == nil {
			continue
		}
		if _, err := req.Diagnostics.Write(done.printed.Bytes()); err != nil && done.err == nil {
			done.read = nil
			done.err = &Error{
				Err: fmt.Errorf("copy what the analyzer printed: %w", err), Analyzer: req.Analyzer, Report: req.Report, Exit: done.exit,
			}
		}
	}

	reports := make([]*report.Report, 0, len(requests))
	var failures []error
	for i := range outcomes {
		if outcomes[i].err != nil {
			failures = append(failures, outcomes[i].err)
			continue
		}
		reports = append(reports, outcomes[i].read)
	}
	if len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	return reports, nil
}

// outcome is what one request of a [Run] left: what the analyzer printed, and
// its report and exit code or the refusal of its read.
type outcome struct {
	read    *report.Report
	err     error
	printed bytes.Buffer
	exit    int
}

// setupFailureExit is the exit code an analyzer returns when a setup failure
// ends its run, and every one of its setup failures is a printed line opening
// with setupFailurePrefix.
const (
	setupFailureExit   = 3
	setupFailurePrefix = "setup failure: "
)

// setupFailed reports whether a setup failure ended the analyzer's run.
func (o *outcome) setupFailed() bool {
	if o.exit != setupFailureExit {
		return false
	}
	for line := range bytes.Lines(o.printed.Bytes()) {
		if bytes.HasPrefix(line, []byte(setupFailurePrefix)) {
			return true
		}
	}
	return false
}

// noteWorkarounds appends the [workaround] line to what each analyzer a setup
// failure ended printed.
func noteWorkarounds(requests []Request, outcomes []outcome) {
	for i := range outcomes {
		if outcomes[i].setupFailed() {
			outcomes[i].printed.WriteString(workaround(requests, outcomes, i))
		}
	}
}

// workaround is the line telling how to analyze the target without the
// analyzer of requests[failed]: the configuration that hands the run only the
// languages of the requests that left a report, or those analyzers run alone.
// With no such request it is empty.
func workaround(requests []Request, outcomes []outcome, failed int) string {
	var languages, names []string
	for i := range requests {
		if outcomes[i].err == nil {
			languages = append(languages, requests[i].Languages...)
			names = append(names, requests[i].Analyzer)
		}
	}
	if len(names) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(languages))
	for _, language := range slices.Compact(slices.Sorted(slices.Values(languages))) {
		quoted = append(quoted, strconv.Quote(language))
	}
	slices.Sort(names)
	return fmt.Sprintf("deadset: to analyze the target without %s, set analysis.languages: [%s] or run %s alone\n",
		requests[failed].Analyzer, strings.Join(quoted, ", "), strings.Join(names, " and "))
}

// resolved refuses a command that is not an absolute path.
func resolved(command string) error {
	if !filepath.IsAbs(command) {
		return fmt.Errorf("%w: %q", ErrUnresolvedCommand, command)
	}
	return nil
}

// command is the analyzer at path run with args in the directory req names. A
// cancelled ctx interrupts the analyzer and, when it has not exited within
// interruptGrace, kills it.
func command(ctx context.Context, req *Request, path string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = req.Dir
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = interruptGrace
	return cmd
}

// diagnosticsOf is the writer an analyzer's diagnostics are copied to, and
// the recorder of that copy's first error. A file, or no writer at all,
// reaches the analyzer with no pipe between, so only another writer has a
// copy that can fail and a recorder.
func diagnosticsOf(w io.Writer) (io.Writer, *recorder) {
	if _, isFile := w.(*os.File); w == nil || isFile {
		return w, nil
	}
	copied := &recorder{w: w}
	return copied, copied
}

// wait runs cmd and returns its exit code. A cancelled ctx fails the run with
// the cancellation's cause whatever the analyzer returned, and a failure to
// copy what the analyzer printed, which copied records, fails it whatever the
// exit code.
func wait(ctx context.Context, cmd *exec.Cmd, copied *recorder) (int, error) {
	err := cmd.Run()
	state := cmd.ProcessState
	switch {
	case ctx.Err() != nil:
		return -1, context.Cause(ctx)
	case copied != nil && copied.err != nil:
		return state.ExitCode(), fmt.Errorf("copy what the analyzer printed: %w", copied.err)
	case state == nil || !state.Exited():
		return -1, err
	}
	if _, exited := errors.AsType[*exec.ExitError](err); err != nil && !exited {
		return state.ExitCode(), err
	}
	return state.ExitCode(), nil
}

// recorder is a writer that keeps the first error a write to w returned.
type recorder struct {
	w   io.Writer
	err error
}

func (r *recorder) Write(p []byte) (int, error) {
	n, err := r.w.Write(p)
	if r.err == nil {
		r.err = err
	}
	return n, err
}
