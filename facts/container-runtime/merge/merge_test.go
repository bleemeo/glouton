// Package merge will merge multiple container runtime.
package merge

import (
	"fmt"
	"glouton/facts"
	"os"
	"testing"
)

func Test_multiError_Is(t *testing.T) {
	tests := []struct {
		name   string
		errs   multiError
		target error
		want   bool
	}{
		{
			name:   "nil",
			errs:   nil,
			target: facts.ErrContainerDoesNotExists,
			want:   false,
		},
		{
			name:   "empty",
			errs:   multiError{},
			target: facts.ErrContainerDoesNotExists,
			want:   false,
		},
		{
			name:   "unrelated error",
			errs:   multiError([]error{os.ErrClosed}),
			target: facts.ErrContainerDoesNotExists,
			want:   false,
		},
		{
			name:   "matching error",
			errs:   multiError([]error{facts.ErrContainerDoesNotExists}),
			target: facts.ErrContainerDoesNotExists,
			want:   true,
		},
		{
			name:   "multiple error",
			errs:   multiError([]error{os.ErrClosed, facts.ErrContainerDoesNotExists}),
			target: facts.ErrContainerDoesNotExists,
			want:   true,
		},
		{
			name:   "multiple error2",
			errs:   multiError([]error{facts.ErrContainerDoesNotExists, os.ErrClosed}),
			target: facts.ErrContainerDoesNotExists,
			want:   true,
		},
		{
			name:   "multiple wrapped error",
			errs:   multiError([]error{os.ErrInvalid, fmt.Errorf("wrapped %w", facts.ErrContainerDoesNotExists), os.ErrClosed}),
			target: facts.ErrContainerDoesNotExists,
			want:   true,
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			if got := tt.errs.Is(tt.target); got != tt.want {
				t.Errorf("multiError.Is() = %v, want %v", got, tt.want)
			}
		})
	}
}
