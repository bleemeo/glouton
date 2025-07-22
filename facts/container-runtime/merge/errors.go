// Copyright 2015-2025 Bleemeo
//
// bleemeo.com an infrastructure monitoring solution in the Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package merge

import (
	"errors"

	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/types"
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
