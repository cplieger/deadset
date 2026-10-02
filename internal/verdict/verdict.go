// Package verdict is the Contract's exit-code table: the code a run returns,
// read from the report the run produced or from the error that ended it before
// a report existed.
package verdict

import (
	"errors"

	"github.com/cplieger/deadset/internal/config"
	"github.com/cplieger/deadset/internal/detect"
	"github.com/cplieger/deadset/internal/invoke"
	"github.com/cplieger/deadset/internal/report"
)

// The exit codes, one per row of the Contract's exit-code table. Usage and
// Failure end a run before any verdict exists; Clean, Findings and Pending are
// verdicts about a complete report.
const (
	Clean    = 0
	Findings = 1
	Usage    = 2
	Failure  = 3
	Pending  = 4
)

// Switch is whether a run's exit code carries the verdict of its report.
type Switch uint8

// The two positions of the switch. On is the zero value.
const (
	// On makes the exit code the verdict.
	On Switch = iota

	// Off makes the exit code Clean whatever the report holds. The report and
	// everything rendered from it are unchanged, and [Code] with On still
	// answers what the verdict was.
	Off
)

// Code is the exit code of a run whose report is r, under the failing severity
// failOn and the switch exitCode. A pending finding outranks everything else; a
// stale suppression yields Findings whatever any finding's severity and failOn
// are; a finding yields Findings when [Fails] says its severity fails the run.
// The counts come from r's totals rather than its arrays, so a report whose
// finding list a maximum finding count bounded keeps the whole run's verdict.
func Code(r *report.Report, failOn config.Severity, exitCode Switch) int {
	if exitCode == Off {
		return Clean
	}
	totals := &r.Totals
	switch {
	case totals.Pending > 0:
		return Pending
	case totals.StaleSuppressions > 0, Failing(&totals.BySeverity, failOn):
		return Findings
	default:
		return Clean
	}
}

// Failing reports whether any finding the counts hold fails a run whose
// failing severity is failOn.
func Failing(counts *report.BySeverity, failOn config.Severity) bool {
	return (counts.Deny > 0 && Fails(report.SeverityDeny, failOn)) ||
		(counts.Warn > 0 && Fails(report.SeverityWarn, failOn)) ||
		(counts.Allow > 0 && Fails(report.SeverityAllow, failOn))
}

// Fails reports whether a finding of the given severity fails a run whose
// failing severity is failOn: whether severity is at or above it, in the order
// allow, warn, deny. A failOn outside the three severities is deny, the
// configuration's default.
func Fails(severity report.Severity, failOn config.Severity) bool {
	return rank(severity) >= rank(report.Severity(failOn))
}

// rank is a severity's place in the order allow, warn, deny. Anything that is
// not allow or warn ranks as deny.
func rank(severity report.Severity) int {
	switch severity {
	case report.SeverityAllow:
		return 0
	case report.SeverityWarn:
		return 1
	default:
		return 2
	}
}

// InvocationError is a command line the run refuses before it reads a target:
// a flag value outside the flag's set, an argument the verb takes none of, or
// a file or directory a flag names that the run cannot use.
type InvocationError struct {
	// Err is why the invocation is refused.
	Err error

	// Flag is the flag at fault, without its dashes, or empty for an argument.
	Flag string
}

// Error names the flag and why its value is refused.
func (e *InvocationError) Error() string {
	if e.Flag == "" {
		return e.Err.Error()
	}
	return "--" + e.Flag + ": " + e.Err.Error()
}

// Unwrap is why the invocation is refused.
func (e *InvocationError) Unwrap() error { return e.Err }

// ForError is the exit code of a run that err ended before a report existed. A
// configuration or an invocation the run refuses is Usage, and so is a path
// filter outside the target, a target in which detection finds no language,
// and an analyzer that exits with Usage, having refused a value of the
// configuration it was given: each is fixed by changing what the run was asked
// to do. An error joining several is Usage only when every error it joins is.
// Every other error is Failure, the code of a run that produced no answer.
func ForError(err error) int {
	if refusesTheRequest(err) {
		return Usage
	}
	return Failure
}

// refusesTheRequest reports whether err, read through every error it wraps, is
// one ForError gives Usage.
func refusesTheRequest(err error) bool {
	switch err := err.(type) {
	case nil:
		return false
	case *config.Error, *InvocationError:
		return true
	case *invoke.Error:
		return err.Exit == Usage && errors.Is(err, invoke.ErrExited)
	case interface{ Unwrap() []error }:
		joined := err.Unwrap()
		for _, one := range joined {
			if !refusesTheRequest(one) {
				return false
			}
		}
		return len(joined) > 0
	case interface{ Unwrap() error }:
		return refusesTheRequest(err.Unwrap())
	default:
		return errors.Is(err, detect.ErrFilter) || errors.Is(err, detect.ErrNoLanguage)
	}
}
