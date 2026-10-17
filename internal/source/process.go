package source

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	spec "github.com/cplieger/deadset-spec/v8"
	"github.com/cplieger/deadset-spec/v8/document"
	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/plan"
	"github.com/cplieger/deadset/v2/internal/rundir"
)

// interruptGrace is how long an analyzer has to exit after a cancelled run
// interrupts it, before it is killed.
const interruptGrace = 10 * time.Second

// reportCodes are the exit codes an analyzer returns over a complete report:
// clean, findings and pending. Every other code ends the analyzer's run with
// no report a reader may use.
var reportCodes = []int{vocab.ExitClean, vocab.ExitFindings, vocab.ExitPending}

var describeSchema = sync.OnceValues(func() (*document.Schema, error) {
	return document.Compile(spec.Contract, "contract/describe.schema.json")
})

// The refusals a read makes, each carried by an [*Error].
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

// The refusals a handshake makes, each carried by a [*HandshakeError].
var (
	// ErrDescribeExited reports a describe verb that exited with a code other
	// than 0, so what it printed is not read.
	ErrDescribeExited = errors.New("describe did not describe the analyzer")

	// ErrDescription reports a describe document the describe schema refuses.
	ErrDescription = errors.New("describe printed no document the describe schema admits")

	// ErrNoConformancePass reports an analyzer whose describe document records
	// no conformance record, or a result other than pass.
	ErrNoConformancePass = errors.New("the analyzer records no conformance pass")

	// ErrSchemaVersion reports an analyzer that reads no report schema version
	// the run accepts.
	ErrSchemaVersion = errors.New("the analyzer reads no schema version the run accepts")

	// ErrDescribedName reports an analyzer that describes itself under a name
	// other than its provider entry's.
	ErrDescribedName = errors.New("the analyzer describes itself under another name than its provider entry's")

	// ErrDescribedLanguages reports an analyzer that does not describe itself
	// as claiming every language the run hands it.
	ErrDescribedLanguages = errors.New("the analyzer does not claim every language the run hands it")
)

// ErrRelativeCommand reports a command that is a path but not an absolute one,
// which is neither of the two forms a command takes.
var ErrRelativeCommand = errors.New("a command is a name looked up on PATH or an absolute path")

// CommandError is a provider entry whose command resolves to no executable
// file.
type CommandError struct {
	// Err is why: an [*exec.Error] from the lookup, or [ErrRelativeCommand].
	Err error

	Name  string // the entry's analyzer name
	Index int    // the entry's place in the provider list
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("analyzer %s, provider entry %s[%d]: %v", e.Name, vocab.KeyProvidersAnalyzers, e.Index, e.Err)
}

func (e *CommandError) Unwrap() error { return e.Err }

// Error is an analysis that left no report the run may read.
type Error struct {
	// Err is what went wrong: [ErrExited], [ErrNoReport], [ErrReportExists],
	// a report the report schema refuses or one naming another analyzer, the
	// cause of a cancelled run, or the error the analyzer could not be run or
	// read with.
	Err error

	Analyzer string // the provider entry
	Report   string // the report path

	// Exit is the analyzer's exit code, or -1 when it did not run or did not
	// exit on its own.
	Exit int
}

func (e *Error) Error() string {
	return fmt.Sprintf("analyzer %s, report %s: %v", e.Analyzer, e.Report, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// HandshakeError is a handshake that admitted no analyzer.
type HandshakeError struct {
	// Err is what went wrong: one of the refusals this package declares for a
	// handshake, the cause of a cancelled run, or the error the analyzer could
	// not be run with or what it printed could not be kept with.
	Err error

	Analyzer string // the provider entry
	Command  string // the executable the provider entry's command resolved to
}

func (e *HandshakeError) Error() string {
	return fmt.Sprintf("analyzer %s, command %s: %v", e.Analyzer, e.Command, e.Err)
}

func (e *HandshakeError) Unwrap() error { return e.Err }

var _ Source = (*Process)(nil)

// Process is an analyzer run as a separate process.
type Process struct {
	diagnostics io.Writer
	name        string
	command     string
	dir         string
	digest      string
	languages   []vocab.Language
}

// Open is the analyzer the planned source's entry names, run in dir. The
// entry's command is resolved here, once: a command holding a separator is a
// path, taken as it is when it is absolute, and any other is a name looked up
// on PATH, so the handshake and the analysis run one path whatever PATH
// becomes. A command that resolves to no executable is a [*CommandError]. The
// describe verb's standard error goes to diagnostics.
func Open(planned *plan.Source, dir string, diagnostics io.Writer) (*Process, error) {
	command := planned.Entry.Command
	refuse := func(err error) (*Process, error) {
		return nil, &CommandError{Err: err, Name: planned.Entry.Name, Index: planned.Index}
	}
	if strings.ContainsRune(command, '/') || strings.ContainsRune(command, filepath.Separator) {
		if !filepath.IsAbs(command) {
			return refuse(fmt.Errorf("%w: %q is a relative path", ErrRelativeCommand, command))
		}
	}
	executable, err := exec.LookPath(command)
	if err != nil {
		return refuse(err)
	}
	return &Process{diagnostics: diagnostics, name: planned.Entry.Name, command: executable, dir: dir, languages: planned.Languages}, nil
}

// Name is the provider entry's name.
func (p *Process) Name() string { return p.name }

// description is the describe document's members the handshake reads.
type description struct {
	Conformance            *report.Conformance `json:"conformance"`
	Name                   string              `json:"name"`
	SchemaVersionsAccepted []string            `json:"schema_versions_accepted"`
	Languages              []vocab.Language    `json:"languages"`
}

// Admit digests the analyzer's executable, runs its describe verb, keeps what
// it printed at kept's describe file, a command that never started keeping
// nothing, and reads the document. It admits the analyzer when the document
// is one the describe schema admits, names the provider entry's name, claims
// every language the run hands it, records a conformance pass and reads one
// of the accepted report schema versions; otherwise it returns a
// [*HandshakeError].
func (p *Process) Admit(ctx context.Context, kept rundir.Entry, accepted []string) error {
	refuse := func(err error) error { return &HandshakeError{Err: err, Analyzer: p.name, Command: p.command} }
	digest, err := artifactDigest(p.command)
	if err != nil {
		return refuse(err)
	}
	p.digest = digest

	var printed bytes.Buffer
	cmd := command(ctx, p.command, p.dir, "describe")
	cmd.Stdout = &printed
	cmd.Stderr = p.diagnostics
	exit, runErr := wait(ctx, cmd)
	if cmd.Process == nil {
		return refuse(runErr)
	}
	keepErr := kept.WriteDescribe(printed.Bytes())
	switch {
	case runErr != nil || keepErr != nil:
		return refuse(errors.Join(runErr, keepErr))
	case exit != 0:
		return refuse(fmt.Errorf("exited %d: %w", exit, ErrDescribeExited))
	}

	compiled, err := describeSchema()
	if err != nil {
		return refuse(err)
	}
	var described description
	if err := compiled.Unmarshal(printed.Bytes(), &described); err != nil {
		return refuse(fmt.Errorf("%w: %w", ErrDescription, err))
	}
	if err := p.admit(&described, accepted); err != nil {
		return refuse(err)
	}
	return nil
}

// admit refuses a description that does not name the entry, leaves a handed
// language unclaimed, records no conformance pass or reads no schema version
// among accepted.
func (p *Process) admit(described *description, accepted []string) error {
	if described.Name != p.name {
		return fmt.Errorf("%w: it names itself %q", ErrDescribedName, described.Name)
	}
	for _, language := range p.languages {
		if !slices.Contains(described.Languages, language) {
			return fmt.Errorf("%w: it claims %v, and the run hands it %s", ErrDescribedLanguages, described.Languages, language)
		}
	}
	switch conformance := described.Conformance; {
	case conformance == nil:
		return fmt.Errorf("%w: describe states no conformance record", ErrNoConformancePass)
	case conformance.Result != report.ResultPass:
		return fmt.Errorf("%w: its result over corpus version %s is %q",
			ErrNoConformancePass, conformance.CorpusVersion, conformance.Result)
	}
	for _, version := range described.SchemaVersionsAccepted {
		if slices.Contains(accepted, version) {
			return nil
		}
	}
	return fmt.Errorf("%w: it reads schema versions %s, and the run accepts %s", ErrSchemaVersion,
		strings.Join(described.SchemaVersionsAccepted, ", "), strings.Join(accepted, ", "))
}

// Produce runs the analyze verb with the three paths the analyzer CLI
// contract passes and reads the report it wrote, which must name the provider
// entry as its analyzer.
func (p *Process) Produce(ctx context.Context, files Files) Outcome {
	out := Outcome{Analyzer: p.name, Languages: p.languages, Digest: p.digest, Exit: -1}
	refuse := func(err error) Outcome {
		out.Err = &Error{Err: err, Analyzer: p.name, Report: files.Report, Exit: out.Exit}
		return out
	}
	if _, err := os.Lstat(files.Report); !errors.Is(err, fs.ErrNotExist) {
		return refuse(cmp.Or(err, ErrReportExists))
	}

	var printed bytes.Buffer
	cmd := command(ctx, p.command, p.dir, "analyze", "--scope="+files.Scope, "--config="+files.Config, "--report="+files.Report)
	cmd.Stdout = &printed
	cmd.Stderr = &printed
	exit, err := wait(ctx, cmd)
	out.Exit, out.Printed = exit, printed.Bytes()
	switch {
	case err != nil:
		return refuse(err)
	case exit == vocab.ExitUsage:
		return refuse(fmt.Errorf("exited %d, a usage or configuration error of the analyzer: %w", exit, ErrExited))
	case !slices.Contains(reportCodes, exit):
		return refuse(fmt.Errorf("exited %d: %w", exit, ErrExited))
	}

	data, err := os.ReadFile(files.Report)
	if errors.Is(err, fs.ErrNotExist) {
		return refuse(fmt.Errorf("exited %d: %w", exit, ErrNoReport))
	}
	if err != nil {
		return refuse(err)
	}
	read, err := report.Decode(data)
	if err != nil {
		return refuse(err)
	}
	if read.Analyzer.Name != p.name {
		return refuse(fmt.Errorf("the report names the analyzer %q, not its provider entry %q", read.Analyzer.Name, p.name))
	}
	out.Report = read
	return out
}

// command is the executable at path run with args in dir. A cancelled ctx
// interrupts it and, when it has not exited within interruptGrace, kills it.
func command(ctx context.Context, path, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = interruptGrace
	return cmd
}

// wait runs cmd and returns its exit code. A cancelled ctx fails the run with
// the cancellation's cause whatever the analyzer returned.
func wait(ctx context.Context, cmd *exec.Cmd) (int, error) {
	err := cmd.Run()
	state := cmd.ProcessState
	switch {
	case ctx.Err() != nil:
		return -1, context.Cause(ctx)
	case state == nil || !state.Exited():
		return -1, err
	}
	if _, exited := errors.AsType[*exec.ExitError](err); err != nil && !exited {
		return state.ExitCode(), err
	}
	return state.ExitCode(), nil
}

// artifactDigest is the digest of the analyzer artifact at path, spelled as a
// merged report's merged_from names it.
func artifactDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("digest the analyzer %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("digest the analyzer %s: %w", path, err)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
