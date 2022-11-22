package merge

import (
	"errors"
	"glouton/facts"
	"glouton/types"
)

// fixMultiError ensure that NoRuntimeError isn't lost by MultiErrors.
func fixMultiError(err error) error {
	var errs types.MultiErrors

	if !errors.As(err, &errs) {
		return err
	}

	if len(errs) == 0 {
		return nil
	}

	if len(errs) == 1 {
		return errs[0]
	}

	// If all errors are NoRuntimeError, we want to return a NoRuntimeError
	var newMultiErr types.MultiErrors

	for _, err := range errs {
		var noRuntime facts.NoRuntimeError

		if !errors.As(err, &noRuntime) {
			return errs
		}

		newMultiErr = append(newMultiErr, noRuntime.Unwrap())
	}

	return facts.NewNoRuntimeError(newMultiErr)
}
