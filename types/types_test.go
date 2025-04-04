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

package types

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
)

var errTest = errors.New("test error")

func TestLabelsToText(t *testing.T) {
	type args struct {
		labels map[string]string
	}

	tests := []struct {
		name     string
		args     args
		want     string
		wantBack map[string]string
	}{
		{
			name: "simple",
			args: args{
				labels: map[string]string{
					"__name__": "node_cpu_seconds_total",
					"cpu":      "0",
					"mode":     "idle",
				},
			},
			want: `__name__="node_cpu_seconds_total",cpu="0",mode="idle"`,
		},
		{
			name: "sorted",
			args: args{
				labels: map[string]string{
					"mode":     "idle",
					"cpu":      "0",
					"__name__": "node_cpu_seconds_total",
				},
			},
			want: `__name__="node_cpu_seconds_total",cpu="0",mode="idle"`,
		},
		{
			name: "only-name",
			args: args{
				labels: map[string]string{
					"__name__": "go_goroutines",
				},
			},
			want: `__name__="go_goroutines"`,
		},
		{
			name: "escaped",
			args: args{
				labels: map[string]string{
					"__name__": "go_goroutines",
					"alabel":   `value1",blabel="value2`,
				},
			},
			want: `__name__="go_goroutines",alabel="value1\",blabel=\"value2"`,
		},
		{
			name: "escaped2",
			args: args{
				labels: map[string]string{
					"__name__": "go_goroutines",
					"alabel":   `value1\",blabel=\"value2\`,
				},
			},
			want: `__name__="go_goroutines",alabel="value1\\\",blabel=\\\"value2\\"`,
		},
		{
			name: "trim-empty-label",
			args: args{
				labels: map[string]string{
					"__name__": "go_goroutines",
					"empty":    "",
				},
			},
			want: `__name__="go_goroutines"`,
			wantBack: map[string]string{
				"__name__": "go_goroutines",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LabelsToText(tt.args.labels)
			if got != tt.want {
				t.Errorf("LabelsToText() = %v, want %v", got, tt.want)
			}

			back := TextToLabels(got)

			wantBack := tt.wantBack
			if tt.wantBack == nil {
				wantBack = tt.args.labels
			}

			if !reflect.DeepEqual(back, wantBack) {
				t.Errorf("TextToLabels(LabelsToText()) = %v, want %v", back, wantBack)
			}
		})
	}
}

func Test_MultiError_Is(t *testing.T) {
	tests := []struct {
		name   string
		errs   MultiErrors
		target error
		want   bool
	}{
		{
			name:   "nil",
			errs:   nil,
			target: errTest,
			want:   false,
		},
		{
			name:   "empty",
			errs:   MultiErrors{},
			target: errTest,
			want:   false,
		},
		{
			name:   "unrelated error",
			errs:   MultiErrors([]error{os.ErrClosed}),
			target: errTest,
			want:   false,
		},
		{
			name:   "matching error",
			errs:   MultiErrors([]error{errTest}),
			target: errTest,
			want:   true,
		},
		{
			name:   "multiple error",
			errs:   MultiErrors([]error{os.ErrClosed, errTest}),
			target: errTest,
			want:   true,
		},
		{
			name:   "multiple error2",
			errs:   MultiErrors([]error{errTest, os.ErrClosed}),
			target: errTest,
			want:   true,
		},
		{
			name:   "multiple wrapped error",
			errs:   MultiErrors([]error{os.ErrInvalid, fmt.Errorf("wrapped %w", errTest), os.ErrClosed}),
			target: errTest,
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.errs.Is(tt.target); got != tt.want {
				t.Errorf("MultiError.Is() = %v, want %v", got, tt.want)
			}
		})
	}
}
