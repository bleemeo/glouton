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
	"context"
	"errors"
	"io"
	"testing"

	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/types"
)

func Test_fixMultiError(t *testing.T) {
	tests := []struct {
		name    string
		errs    error
		isNil   bool
		errorIs error
		errorAs any
	}{
		{
			name:  "nil",
			errs:  nil,
			isNil: true,
		},
		{
			name:    "simple-is",
			errs:    context.DeadlineExceeded,
			errorIs: context.DeadlineExceeded,
		},
		{
			name:    "simple-as",
			errs:    facts.NewNoRuntimeError(io.EOF),
			errorAs: &facts.NoRuntimeError{},
		},
		{
			name:  "empty-list",
			errs:  types.MultiErrors{},
			isNil: true,
		},
		{
			name:    "single-value-is",
			errs:    types.MultiErrors{context.DeadlineExceeded},
			errorIs: context.DeadlineExceeded,
		},
		{
			name:    "single-value-as",
			errs:    types.MultiErrors{facts.NewNoRuntimeError(io.EOF)},
			errorAs: &facts.NoRuntimeError{},
		},
		{
			name:    "multi-value-no-runtime",
			errs:    types.MultiErrors{facts.NewNoRuntimeError(io.EOF), facts.NewNoRuntimeError(context.DeadlineExceeded)},
			errorAs: &facts.NoRuntimeError{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := fixMultiError(tt.errs)
			if tt.isNil && got != nil {
				t.Errorf("fixMultiError() = %v, want nil", got)
			}

			if !tt.isNil {
				if tt.errorIs != nil && !errors.Is(got, tt.errorIs) {
					t.Errorf("fixMultiError() = %v, want is %v", got, tt.errorIs)
				}

				if tt.errorAs != nil && !errors.As(got, tt.errorAs) {
					t.Errorf("fixMultiError() = %v, want as %#v", got, tt.errorAs)
				}
			}
		})
	}
}
