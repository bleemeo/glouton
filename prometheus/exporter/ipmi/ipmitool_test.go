// Copyright 2015-2023 Bleemeo
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

package ipmi

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func Test_ipmiTooldecodeDCMI(t *testing.T) {
	tests := []struct {
		name     string
		testfile string
		want     []powerReading
	}{
		{
			name:     "wrong",
			testfile: "wrong-output-ipmi-sensors.txt",
			want:     []powerReading{},
		},
		{
			name:     "r310",
			testfile: "dell-r310-ipmitool-dcmi.txt",
			want:     []powerReading{},
		},
		{
			name:     "r320",
			testfile: "dell-r320-ipmitool-dcmi.txt",
			want: []powerReading{
				{
					ReportPeriod: (26547088 * time.Second / dellReportPeriodInSecondInsteadOfMillisecond).Truncate(time.Second),
					Timestamp:    time.Date(2023, 7, 25, 15, 34, 31, 0, time.UTC),
					Active:       true,
					Current:      65,
					Minimum:      0,
					Maximum:      133,
					Average:      63,
				},
			},
		},
		{
			name:     "r320-2",
			testfile: "dell-r320-2-ipmitool-dcmi.txt",
			want: []powerReading{
				{
					ReportPeriod: (26547205 * time.Second / dellReportPeriodInSecondInsteadOfMillisecond).Truncate(time.Second),
					Timestamp:    time.Date(2023, 7, 25, 15, 38, 52, 0, time.UTC),
					Active:       true,
					Current:      71,
					Minimum:      0,
					Maximum:      159,
					Average:      73,
				},
			},
		},
		{
			name:     "R720xd",
			testfile: "dell-r720xd-ipmitool-dcmi.txt",
			want: []powerReading{
				{
					ReportPeriod: (26351204 * time.Second / dellReportPeriodInSecondInsteadOfMillisecond).Truncate(time.Second),
					Timestamp:    time.Date(2023, 7, 25, 15, 41, 53, 0, time.UTC),
					Active:       true,
					Current:      389,
					Minimum:      123,
					Maximum:      637,
					Average:      374,
				},
			},
		},
		{
			name:     "R720xd-2",
			testfile: "dell-r720xd-2-ipmitool-dcmi.txt",
			want: []powerReading{
				{
					ReportPeriod: 1 * time.Second,
					Timestamp:    time.Date(2023, 7, 25, 15, 44, 18, 0, time.UTC),
					Active:       true,
					Current:      170,
					Minimum:      69,
					Maximum:      312,
					Average:      173,
				},
			},
		},
		{
			name:     "HP Proliant DL360 G7",
			testfile: "hp-dl360-g7-ipmitool-dcmi.txt",
			want:     []powerReading{},
		},
		{
			name:     "HP",
			testfile: "hp-ipmitool-dcmi.txt",
			want: []powerReading{
				{
					ReportPeriod: 300 * time.Second,
					Timestamp:    time.Date(2023, 7, 25, 15, 23, 7, 0, time.UTC),
					Active:       true,
					Current:      62,
					Minimum:      61,
					Maximum:      86,
					Average:      62,
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(filepath.Join("testdata", tt.testfile))
			if err != nil {
				t.Fatal(err)
			}

			got, err := decodeIPMIToolDCMI(content)
			if err != nil {
				t.Error(err)
			}

			timeComparer := cmp.Comparer(func(x, y time.Time) bool {
				return x.Equal(y)
			})

			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty(), timeComparer); diff != "" {
				t.Errorf("decodeSensors() mismatch: (-want +got)\n%s", diff)
			}
		})
	}
}
