// Package source obtains the report of each language a run analyzes. A
// [Source] is one producer of a report; [Process] is an analyzer the provider
// list names, run as a separate process through the analyzer CLI contract. No
// analyzer is linked into this program as a library.
//
// The read is fail-closed. The exit code is read before the report is opened,
// and only a code that is a verdict about a complete report lets the report be
// read at all. An absent report, a report that does not decode, and a
// cancelled run each leave the run with no report from that source, and [Run]
// then returns no report from any source. Every refusal of a read is an
// [*Error] naming the provider entry and the report path, and every refusal of
// a handshake a [*HandshakeError] naming the provider entry and its command;
// the exit code the run returns for one is the caller's to decide.
package source

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/rundir"
)

// Source is one producer of a report for some of a run's languages.
type Source interface {
	// Name is the provider entry's name, which keys the source's files in
	// the run directory and which its report names as its analyzer.
	Name() string

	// Admit runs the handshake before any analysis: it keeps what the
	// source described itself as at kept's describe file, and refuses a
	// source that reads none of the accepted report schema versions.
	Admit(ctx context.Context, kept rundir.Entry, accepted []string) error

	// Produce analyzes and returns the outcome: the report, or the refusal
	// of its read.
	Produce(ctx context.Context, files Files) Outcome
}

// Files are the paths one source is handed: the scope document it reads, its
// configuration, and where its report is written.
type Files struct {
	Scope, Config, Report string
}

// Outcome is what one source left: its report or the refusal of its read,
// and what it printed, which is held whole rather than streamed so two
// sources' lines never interleave.
type Outcome struct {
	Report *report.Report
	Err    error

	// Analyzer is the source's name, and Languages the languages it was
	// handed.
	Analyzer  string
	Languages []vocab.Language

	// Digest is the digest of the artifact that produced the report, as the
	// merged report's merged_from names it.
	Digest string

	Printed []byte

	// Exit is the exit code, or -1 where no process exited on its own.
	Exit int
}

// SetupFailed reports whether a setup failure ended the source's run: the
// failure code and a line the setup-failure prefix opens.
func (o *Outcome) SetupFailed() bool {
	if o.Exit != vocab.ExitFailure {
		return false
	}
	for line := range bytes.Lines(o.Printed) {
		if bytes.HasPrefix(line, []byte(vocab.SetupFailurePrefix)) {
			return true
		}
	}
	return false
}

// Run produces every source's outcome at once, each handed files[i], and
// returns them in the order of sources. When any source leaves no report,
// every outcome's report is dropped and the error joins every refusal in that
// order, so no source's findings stand as the run's result beside another's
// failure.
func Run(ctx context.Context, sources []Source, files []Files) ([]Outcome, error) {
	outcomes := make([]Outcome, len(sources))
	var running sync.WaitGroup
	for i := range sources {
		running.Go(func() { outcomes[i] = sources[i].Produce(ctx, files[i]) })
	}
	running.Wait()
	var failures []error
	for i := range outcomes {
		if outcomes[i].Err != nil {
			failures = append(failures, outcomes[i].Err)
		}
	}
	if len(failures) == 0 {
		return outcomes, nil
	}
	for i := range outcomes {
		outcomes[i].Report = nil
	}
	return outcomes, errors.Join(failures...)
}

// ByName is every outcome in analyzer-name order, the order their printed
// lines are shown in.
func ByName(outcomes []Outcome) []*Outcome {
	held := make([]*Outcome, len(outcomes))
	for i := range outcomes {
		held[i] = &outcomes[i]
	}
	slices.SortStableFunc(held, func(a, b *Outcome) int { return strings.Compare(a.Analyzer, b.Analyzer) })
	return held
}
