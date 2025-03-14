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

package gloutonexec

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestErrTimedoutFormat ensure our timeout error string is compatible with the
// one Telegraf does.
func TestErrTimedoutFormat(t *testing.T) {
	// Because our code rely on this format, kept it unchanged
	if ErrTimeout.Error() != "command timed out" {
		t.Error("ErrTimeout format isn't 'command timed out'")
	}
}

func TestGraceDelay(t *testing.T) {
	t.Parallel()

	runner := New("/")

	cases := []struct {
		Name         string
		ContextDelay time.Duration
		GraceDelay   time.Duration
		Shell        string
		WantOut      string
		WantErr      bool
		ErrIsTimeout bool
	}{
		{
			Name:         "no-timeout-no-grace",
			ContextDelay: 500 * time.Millisecond,
			Shell:        "t() { echo term_received; exit 0; }; trap t TERM; sleep 0.2;echo good; exit 0",
			WantOut:      "good\n",
			WantErr:      false,
		},
		{
			Name:         "no-timeout-grace",
			ContextDelay: 500 * time.Millisecond,
			GraceDelay:   500 * time.Millisecond,
			Shell:        "t() { echo term_received; exit 0; }; trap t TERM; sleep 0.2;echo good; exit 0",
			WantOut:      "good\n",
			WantErr:      false,
		},
		{
			Name:         "error-no-timeout-no-grace",
			ContextDelay: 500 * time.Millisecond,
			Shell:        "t() { echo term_received; exit 0; }; trap t TERM; sleep 0.2;echo good; exit 1",
			WantOut:      "good\n",
			WantErr:      true,
		},
		{
			Name:         "timeout-grace",
			ContextDelay: 500 * time.Millisecond,
			GraceDelay:   500 * time.Millisecond,
			Shell:        "t() { echo term_received; exit 0; }; trap t TERM; sleep 0.6;echo good; exit 1",
			WantOut:      "term_received\n",
			WantErr:      false,
		},
		{
			Name:         "timeout-grace-no-term",
			ContextDelay: 500 * time.Millisecond,
			GraceDelay:   500 * time.Millisecond,
			Shell:        "t() { echo term_received; sleep 1; exit 0; }; trap t TERM; sleep 0.6;echo good; exit 0",
			WantOut:      "term_received\n",
			WantErr:      true,
			ErrIsTimeout: true,
		},
		{
			Name:         "err-timeout-grace",
			ContextDelay: 500 * time.Millisecond,
			GraceDelay:   500 * time.Millisecond,
			Shell:        "t() { echo term_received; exit 1; }; trap t TERM; sleep 0.6;echo good; exit 1",
			WantOut:      "term_received\n",
			WantErr:      true,
			ErrIsTimeout: true,
		},
		{
			Name:         "timeout-no-grace",
			ContextDelay: 500 * time.Millisecond,
			Shell:        "t() { echo term_received; exit 0; }; trap t TERM; sleep 0.6;echo good; exit 1",
			WantOut:      "",
			WantErr:      true,
		},
		{
			Name:         "err-timeout-no-grace",
			ContextDelay: 500 * time.Millisecond,
			Shell:        "t() { echo term_received; exit 1; }; trap t TERM; sleep 0.6;echo good; exit 1",
			WantOut:      "",
			WantErr:      true,
		},
	}

	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(t.Context(), tt.ContextDelay)
			defer cancel()

			out, err := runner.Run(
				ctx,
				Option{GraceDelay: tt.GraceDelay, CombinedOutput: true},
				"sh",
				"-c",
				tt.Shell,
			)

			if !tt.WantErr != (err == nil) {
				t.Errorf("err = %v, wantErr = %v", err, tt.WantErr)
			}

			if tt.WantErr && tt.ErrIsTimeout && !errors.Is(err, ErrTimeout) {
				t.Errorf("err = %v, want ErrIsTimeout", err)
			}

			if string(out) != tt.WantOut {
				t.Errorf("out = %q, want = %q", out, tt.WantOut)
			}
		})
	}
}
