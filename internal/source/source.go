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
// a handshake a [*HandshakeError] naming the provider entry and its command,
// each wrapped in the class of failure it is where it names one.
package source

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/cplieger/deadset-spec/v8/report"
	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/failure"
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
// sources' lines never interleave. After [Run], Printed is what the source
// printed beside the failure lines Run relays, [failure.Quoted].
type Outcome struct {
	Report *report.Report
	Err    error

	// Analyzer is the source's name, and Languages the languages it was
	// handed.
	Analyzer  string
	Languages []vocab.Language

	// LeftOut is, for an [Outcome.Omitted] source, those of its languages no
	// source the run keeps was handed, which the run leaves out of scope.
	LeftOut []vocab.Language

	// Dropped is, for an [Outcome.Omitted] source, the paths of the declared
	// consumers the run leaves out with its LeftOut.
	Dropped []string

	// Digest is the digest of the artifact that produced the report, as the
	// merged report's merged_from names it.
	Digest string

	Printed []byte

	// Exit is the exit code, or -1 where no process exited on its own.
	Exit int

	// Omitted reports a source [Run] left out of the run, with no report and
	// no refusal: it found no program of the languages a census handed it.
	Omitted bool
}

// SetupFailed reports whether a setup failure ended the source's run: the
// failure code and a class line of the setup group.
func (o *Outcome) SetupFailed() bool {
	relayed, ok := errors.AsType[*failure.Error](o.Err)
	return ok && o.Exit == vocab.ExitFailure && relayed.Setup()
}

// foundNoProgram reports whether the source's one failure is the setup
// failure no-program.
func (o *Outcome) foundNoProgram() bool {
	relayed, ok := errors.AsType[*failure.Error](o.Err)
	return ok && o.Exit == vocab.ExitFailure && len(relayed.Failures) == 1 && relayed.Failures[0].Class == vocab.SetupNoProgram
}

// Run produces every source's outcome at once, each handed files[i], in the
// order of sources. A source that ended with the failure code refuses with the
// failures it printed, relayed; all else every source printed is kept
// [failure.Quoted]. Where detected, a census put the languages in scope, and a
// source whose one failure is no-program is [Outcome.Omitted], as
// languages.json states, unless every source is. Any other refusal drops every
// report, and the error joins the refusals in order, so no source's findings
// stand beside another's failure.
func Run(ctx context.Context, sources []Source, files []Files, detected bool) ([]Outcome, error) {
	outcomes := make([]Outcome, len(sources))
	var running sync.WaitGroup
	for i := range sources {
		running.Go(func() { outcomes[i] = sources[i].Produce(ctx, files[i]) })
	}
	running.Wait()
	printed := make([][]byte, len(outcomes))
	noProgram := make([]bool, len(outcomes))
	for i := range outcomes {
		o := &outcomes[i]
		printed[i] = o.Printed
		if o.Exit == vocab.ExitFailure {
			o.Err, o.Printed = failure.Relayed(o.Analyzer, o.Printed)
		} else {
			o.Printed = failure.Quoted(o.Analyzer, o.Printed)
		}
		noProgram[i] = o.foundNoProgram()
	}
	if detected && slices.Contains(noProgram, false) {
		omit(outcomes, noProgram, printed)
	}
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

// omit leaves out every outcome noProgram marks, keeping what it printed
// quoted, with the languages of it no kept outcome was handed.
func omit(outcomes []Outcome, noProgram []bool, printed [][]byte) {
	var kept []vocab.Language
	for i := range outcomes {
		if !noProgram[i] {
			kept = append(kept, outcomes[i].Languages...)
		}
	}
	for i := range outcomes {
		if noProgram[i] {
			o := &outcomes[i]
			o.Err, o.Printed, o.Omitted = nil, failure.Quoted(o.Analyzer, printed[i]), true
			o.LeftOut = slices.DeleteFunc(slices.Clone(o.Languages), func(l vocab.Language) bool { return slices.Contains(kept, l) })
		}
	}
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
