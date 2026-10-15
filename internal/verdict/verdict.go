// Package verdict decides the Contract exit code of a run that an error ended
// before a merged report existed.
package verdict

import (
	"errors"

	"github.com/cplieger/deadset-spec/v8/vocab"
	"github.com/cplieger/deadset/v2/internal/config"
	"github.com/cplieger/deadset/v2/internal/detect"
	"github.com/cplieger/deadset/v2/internal/invoke"
)

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
// configuration or an invocation the run refuses is [vocab.ExitUsage], and so
// is a path filter outside the target, a target in which detection finds no
// language, and an analyzer that exits with it, having refused a value of the
// configuration it was given: each is fixed by changing what the run was asked
// to do. An error joining several is usage only when every error it joins is.
// Every other error is [vocab.ExitFailure], the code of a run that produced no
// answer.
func ForError(err error) int {
	if refusesTheRequest(err) {
		return vocab.ExitUsage
	}
	return vocab.ExitFailure
}

// refusesTheRequest reports whether err, read through every error it wraps, is
// one ForError gives [vocab.ExitUsage].
func refusesTheRequest(err error) bool {
	switch err := err.(type) {
	case nil:
		return false
	case *config.Error, *InvocationError:
		return true
	case *invoke.Error:
		return err.Exit == vocab.ExitUsage && errors.Is(err, invoke.ErrExited)
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
