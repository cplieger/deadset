// Package invoke runs an analyzer's analyze verb as a separate process and
// reads the report the analyzer writes to the file the invocation names. No
// analyzer is linked into this program as a library.
//
// The read is fail-closed. The exit code is read before the report is opened,
// and only a code that is a verdict about a complete report lets the report be
// read at all. An absent report, a report that does not decode, and a
// cancelled run each leave the run with no report from that analyzer, and
// [Run] then returns no report from any analyzer. Every refusal is an [*Error]
// naming the provider entry and the report path; the exit code the run returns
// for one is the caller's to decide.
package invoke

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"slices"
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
)

// Request is one analyze invocation: the provider entry it runs, and the
// three paths the analyzer CLI contract passes.
type Request struct {
	// Diagnostics receives everything the analyzer prints, on stderr and on
	// the stdout the contract leaves empty. A nil Diagnostics discards it.
	Diagnostics io.Writer

	// Analyzer is the name of the provider entry, which every refusal names.
	Analyzer string

	// Command is the analyzer's executable: an absolute path, or a name
	// looked up on PATH.
	Command string

	// Dir is the directory the analyzer runs in, which a report names its
	// paths relative to. An empty Dir is this process's working directory.
	Dir string

	Scope  string // the scope document, passed as --scope
	Config string // the analyzer's configuration, passed as --config
	Report string // where the analyzer writes its report, passed as --report
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
// report it wrote. It returns the report, or no report and an [*Error].
func Analyze(ctx context.Context, req *Request) (*report.Report, error) {
	refuse := func(exit int, err error) error {
		return &Error{Err: err, Analyzer: req.Analyzer, Report: req.Report, Exit: exit}
	}
	if _, err := os.Lstat(req.Report); !errors.Is(err, fs.ErrNotExist) {
		return nil, refuse(-1, cmp.Or(err, ErrReportExists))
	}

	exit, err := run(ctx, req)
	switch {
	case err != nil:
		return nil, refuse(exit, err)
	case !slices.Contains(reportCodes, exit):
		return nil, refuse(exit, fmt.Errorf("exited %d: %w", exit, ErrExited))
	}

	data, err := os.ReadFile(req.Report)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, refuse(exit, fmt.Errorf("exited %d: %w", exit, ErrNoReport))
	}
	if err != nil {
		return nil, refuse(exit, err)
	}
	read, err := report.Decode(data)
	if err != nil {
		return nil, refuse(exit, err)
	}
	return read, nil
}

// Run analyzes every request in order, each as its own process, and returns
// every report in the order of the requests. When any request leaves no
// report, it returns no report at all and an error joining every [*Error] in
// request order, so no analyzer's findings stand as the run's result beside
// another's failure.
func Run(ctx context.Context, requests []Request) ([]*report.Report, error) {
	reports := make([]*report.Report, 0, len(requests))
	var failures []error
	for i := range requests {
		read, err := Analyze(ctx, &requests[i])
		if err != nil {
			failures = append(failures, err)
			continue
		}
		reports = append(reports, read)
	}
	if len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	return reports, nil
}

// run runs the analyzer and returns its exit code. A cancelled ctx interrupts
// the analyzer and, when it has not exited within interruptGrace, kills it;
// the run then fails with the cancellation's cause whatever the analyzer
// returned. A failure to copy what the analyzer printed to Diagnostics fails
// the run whatever the exit code.
func run(ctx context.Context, req *Request) (int, error) {
	cmd := exec.CommandContext(ctx, req.Command, "analyze", //nolint:gosec // G204: running the analyzer a provider entry names is this function's job
		"--scope="+req.Scope, "--config="+req.Config, "--report="+req.Report)
	cmd.Dir = req.Dir
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = interruptGrace

	// A file, or no writer at all, reaches the analyzer with no pipe between,
	// so only another writer has a copy that can fail.
	cmd.Stdout = req.Diagnostics
	var copied *recorder
	if _, isFile := req.Diagnostics.(*os.File); req.Diagnostics != nil && !isFile {
		copied = &recorder{w: req.Diagnostics}
		cmd.Stdout = copied
	}
	cmd.Stderr = cmd.Stdout

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
