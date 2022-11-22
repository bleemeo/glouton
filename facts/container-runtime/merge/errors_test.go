package merge

import (
	"context"
	"errors"
	"glouton/facts"
	"glouton/types"
	"io"
	"testing"
)

func Test_fixMultiError(t *testing.T) {
	tests := []struct {
		name    string
		errs    error
		isNil   bool
		errorIs error
		errorAs interface{}
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
		tt := tt

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
