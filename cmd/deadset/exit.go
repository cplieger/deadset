package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/documents"
	"github.com/cplieger/deadset/v2/internal/failure"
	"github.com/cplieger/deadset/v2/internal/run"
	"github.com/cplieger/deadset/v2/internal/source"
)

// usageError is a command line the run refuses: a file or directory an
// option names that the run cannot use, a value the option does not take, or
// a scope the run cannot route.
type usageError struct {
	err error

	// option is the option at fault, without its dashes, or empty for none.
	option string
}

func (e *usageError) Error() string {
	if e.option == "" {
		return e.err.Error()
	}
	return "--" + e.option + ": " + e.err.Error()
}

func (e *usageError) Unwrap() error { return e.err }

// failed prints err and returns the exit code of a run it ended. What the
// run refuses ([refused]) is [vocab.ExitUsage], fixed by changing what the run
// was asked to do; the usage text follows a refused command line or
// configuration, not a refused shared document or an analyzer's refusal,
// which name their own fault. Every other run is [vocab.ExitFailure] and
// prints its failures in the order err joins them, after any refusal beside
// them. An interrupt is one interrupted-run failure where the first error the
// cancellation ended stands, or last when none did.
func failed(ctx context.Context, stderr io.Writer, err error, printUsage func()) int {
	cause := context.Cause(ctx)
	if cause == nil && refused(err) {
		fmt.Fprintf(stderr, "%s: %v\n", run.Product, err)
		_, analyzer := errors.AsType[*source.Error](err)
		if _, shared := errors.AsType[*documents.Error](err); !analyzer && !shared {
			printUsage()
		}
		return vocab.ExitUsage
	}
	interrupt := cause != nil
	var failures []failure.Failure
	for _, one := range joined(err) {
		switch {
		case cause != nil && (errors.Is(one, cause) || errors.Is(one, context.Canceled)):
			if interrupt {
				failures = append(failures, interrupted(cause)...)
				interrupt = false
			}
		case refused(one):
			fmt.Fprintf(stderr, "%s: %v\n", run.Product, one)
		default:
			failures = append(failures, failure.Of(one)...)
		}
	}
	if interrupt {
		failures = append(failures, interrupted(cause)...)
	}
	_ = failure.Write(stderr, failures)
	return vocab.ExitFailure
}

func interrupted(cause error) []failure.Failure {
	return failure.Wrap(vocab.RunInterruptedRun, fmt.Errorf("the run stopped: %w", cause)).Failures
}

func joined(err error) []error {
	several, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return []error{err}
	}
	var held []error
	for _, one := range several.Unwrap() {
		held = append(held, joined(one)...)
	}
	return held
}

// refused reports whether err, read through every error it wraps, refuses
// what the run was asked to do; an error joining several refuses only when
// every one it joins does.
func refused(err error) bool {
	switch err := err.(type) {
	case nil:
		return false
	case *config.Error, *usageError, *documents.Error:
		return true
	case *source.Error:
		return err.Exit == vocab.ExitUsage && errors.Is(err, source.ErrExited)
	case interface{ Unwrap() []error }:
		joined := err.Unwrap()
		for _, one := range joined {
			if !refused(one) {
				return false
			}
		}
		return len(joined) > 0
	case interface{ Unwrap() error }:
		return refused(err.Unwrap())
	default:
		return false
	}
}
