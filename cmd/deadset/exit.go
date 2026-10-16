package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/plan"
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

// failed prints err and returns the exit code of a run it ended. A setup
// failure prints its setup-failure line. A configuration or a command line
// the run refuses, and an analyzer exiting with the usage code, having
// refused a value of the configuration it was given, is [vocab.ExitUsage]:
// each is fixed by changing what the run was asked to do. The usage text
// follows a refusal of this command's own, not an analyzer's, which printed
// its own. Every other error is [vocab.ExitFailure], the code of a run that
// produced no answer.
func failed(stderr io.Writer, err error, printUsage func()) int {
	if setup, ok := errors.AsType[*plan.SetupError](err); ok {
		fmt.Fprintf(stderr, "%s%s\n", vocab.SetupFailurePrefix, setup)
		return vocab.ExitFailure
	}
	fmt.Fprintf(stderr, "%s: %v\n", run.Product, err)
	if !refused(err) {
		return vocab.ExitFailure
	}
	if _, analyzer := errors.AsType[*source.Error](err); !analyzer {
		printUsage()
	}
	return vocab.ExitUsage
}

// refused reports whether err, read through every error it wraps, refuses
// what the run was asked to do; an error joining several refuses only when
// every one it joins does.
func refused(err error) bool {
	switch err := err.(type) {
	case nil:
		return false
	case *config.Error, *usageError:
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
