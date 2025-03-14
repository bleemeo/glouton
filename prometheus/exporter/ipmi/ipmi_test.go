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

package ipmi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/google/go-cmp/cmp"
)

var errTestCommandNotImplemented = errors.New("test don't implement this command")

// Test_GatherWithState is the principal test and indirectly test other method.
// Other test ensentially helps to diagnostic issue on sub-function / test some corner case.
// For each new server that we can test, we should write a test case for Test_GatherWithState before other tests.
// To generate testdata file, run the following command:
// (
// set -x
// TZ=UTC LANG=C ipmi-dcmi --get-enhanced-system-power-statistics
// TZ=UTC LANG=C ipmi-dcmi --get-system-power-statistics
// TZ=UTC LANG=C ipmi-sensors -W discretereading --sdr-cache-recreate
// TZ=UTC LANG=C ipmitool dcmi power reading
// )
// Then copy the output of each command in files inside testdata folder.
func Test_GatherWithState(t *testing.T) { //nolint:maintidx
	now := time.Date(2023, 7, 24, 8, 23, 53, 0, time.UTC)

	catFullPath, err := exec.LookPath("cat")
	if err != nil {
		t.Fatal(err)
	}

	folderWithCat := filepath.Dir(catFullPath)

	tests := []struct {
		name                string
		testprefix          string
		config              config.IPMI
		want                []types.MetricPoint
		wantMethod          ipmiMethod
		disableFreeIPMI     bool
		disableFreeIPMIDCMI bool
		disableIPMITool     bool
		useCatAndRunCmd     bool
		cmdNotRunAfterInit  bool
	}{
		{
			name:       "R310",
			testprefix: "dell-r310",
			wantMethod: methodFreeIPMISensors,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Time:  now,
						Value: 84, // value from ipmi-sensors, only value available
					},
				},
			},
		},
		{
			name:       "R320",
			testprefix: "dell-r320",
			wantMethod: methodFreeIPMIDCMI,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Time:  now,
						Value: 61, // value from ipmi-dcmi
					},
				},
			},
		},
		{
			name:            "R320-use-cat",
			testprefix:      "dell-r320",
			useCatAndRunCmd: true,
			wantMethod:      methodFreeIPMIDCMI,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Time:  now,
						Value: 61, // value from ipmi-dcmi
					},
				},
			},
		},
		{
			name:       "R320-use-cat-search-path",
			testprefix: "dell-r320",
			config: config.IPMI{
				BinarySearchPath: folderWithCat,
			},
			useCatAndRunCmd: true,
			wantMethod:      methodFreeIPMIDCMI,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Time:  now,
						Value: 61, // value from ipmi-dcmi
					},
				},
			},
		},
		{
			name:            "R320-ipmitool",
			testprefix:      "dell-r320",
			wantMethod:      methodIPMITool,
			disableFreeIPMI: true,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Time:  now,
						Value: 65,
					},
				},
			},
		},
		{
			name:                "R320-no-dcmi",
			testprefix:          "dell-r320",
			wantMethod:          methodFreeIPMISensors,
			disableFreeIPMIDCMI: true,
			disableIPMITool:     true,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Time:  now,
						Value: 56,
					},
				},
			},
		},
		{
			name:       "R320-2",
			testprefix: "dell-r320-2",
			wantMethod: methodFreeIPMIDCMI,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Time:  now,
						Value: 75, // value from ipmi-dcmi
					},
				},
			},
		},
		{
			name:       "R720xd",
			testprefix: "dell-r720xd",
			wantMethod: methodFreeIPMIDCMI,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Time:  now,
						Value: 391,
					},
				},
			},
		},
		{
			name:       "R720xd-2",
			testprefix: "dell-r720xd-2",
			wantMethod: methodFreeIPMIDCMIEnhanced,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Time:  now,
						Value: 170,
					},
				},
			},
		},
		{
			name:            "R720xd-2-ipmitool",
			testprefix:      "dell-r720xd-2",
			wantMethod:      methodIPMITool,
			disableFreeIPMI: true,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Time:  now,
						Value: 170,
					},
				},
			},
		},
		{
			name:       "HP Proliant DL360 G7",
			testprefix: "hp-dl360-g7",
			wantMethod: methodFreeIPMIDCMIEnhanced,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Time:  now,
						Value: 98, // value from average of 10 seconds from ipmi-dcmi
					},
				},
			},
		},
		{
			name:                "HP Proliant DL360 G7",
			testprefix:          "hp-dl360-g7",
			disableFreeIPMIDCMI: true,
			wantMethod:          methodFreeIPMISensors,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Time:  now,
						Value: 98,
					},
				},
			},
		},
		{
			name:       "HP server",
			testprefix: "hp",
			wantMethod: methodFreeIPMIDCMIEnhanced,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Time:  now,
						Value: 62, // value from average of 10 seconds from ipmi-dcmi
					},
				},
			},
		},
		{
			name:            "HP server-ipmitool",
			testprefix:      "hp",
			wantMethod:      methodIPMITool,
			disableFreeIPMI: true,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Time:  now,
						Value: 62,
					},
				},
			},
		},
		{
			name:               "failing ipmi-sensors",
			testprefix:         "wrong-output",
			wantMethod:         methodNoIPMIAvailable,
			want:               []types.MetricPoint{},
			cmdNotRunAfterInit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				runCounts              int
				cmdFreeIMPSensors      = []string{"cat", filepath.Join("testdata", tt.testprefix+"-ipmi-sensors.txt")}
				cmdFreeIMPDCMI         = []string{"cat", filepath.Join("testdata", tt.testprefix+"-ipmi-dcmi.txt")}
				cmdFreeIMPDCMIEnhanced = []string{"cat", filepath.Join("testdata", tt.testprefix+"-ipmi-dcmi-enhanced.txt")}
				cmdIPMITool            = []string{"cat", filepath.Join("testdata", tt.testprefix+"-ipmitool-dcmi.txt")}
				cmdDoesNotExists       = []string{"this-command-does-not-exists"}
			)

			realRunner := gloutonexec.New("/")
			realRunCmd := runCmdWithRunner(realRunner)

			testRunCMD := func(ctx context.Context, searchPath string, _ bool, args []string) ([]byte, error) {
				var (
					cmd      []string
					disabled bool
				)

				// we don't take lock around runCounts, because we want error to raise
				// with -race if the runCmd is called twice concurrently.
				runCounts++

				switch strings.Join(args, " ") {
				case strings.Join(freeipmiSensorsCmd, " "):
					if tt.disableFreeIPMI {
						disabled = true
					}

					cmd = cmdFreeIMPSensors
				case strings.Join(freeipmiDCMIEnhanced, " "):
					if tt.disableFreeIPMI || tt.disableFreeIPMIDCMI {
						disabled = true
					}

					cmd = cmdFreeIMPDCMIEnhanced
				case strings.Join(freeipmiDCMISimple, " "):
					if tt.disableFreeIPMI || tt.disableFreeIPMIDCMI {
						disabled = true
					}

					cmd = cmdFreeIMPDCMI
				case strings.Join(ipmitoolDCMI, " "):
					if tt.disableIPMITool {
						disabled = true
					}

					cmd = cmdIPMITool
				default:
					return nil, fmt.Errorf("%w: %s", errTestCommandNotImplemented, args[0])
				}

				if tt.useCatAndRunCmd {
					if disabled {
						return realRunCmd(ctx, searchPath, false, cmdDoesNotExists)
					}

					return realRunCmd(ctx, searchPath, false, cmd)
				}

				if disabled {
					return []byte("command not found"), errTestCommandNotImplemented
				}

				return os.ReadFile(cmd[1])
			}

			g := newGatherer(tt.config, testRunCMD, func(bool) {})

			mfs, err := g.Gather()
			if err != nil {
				t.Fatal(err)
			}

			if g.usedMethod != tt.wantMethod {
				t.Errorf("usedMethod = %s, want %s", methodName(g.usedMethod), methodName(tt.wantMethod))
			}

			got := model.FamiliesToMetricPoints(now, mfs, true)

			if diff := types.DiffMetricPoints(tt.want, got, false); diff != "" {
				t.Errorf("Gather points mismatch (-want +got):\n%s", diff)
			}

			for _, extraCall := range []int{2, 3} {
				runCounts = 0

				mfs, err = g.Gather()
				if err != nil {
					t.Fatal(err)
				}

				got = model.FamiliesToMetricPoints(now, mfs, true)

				if diff := types.DiffMetricPoints(tt.want, got, false); diff != "" {
					t.Errorf("call #%d Gather points mismatch (-want +got):\n%s", extraCall, diff)
				}

				if runCounts != 0 && tt.cmdNotRunAfterInit {
					t.Errorf("call #%d runCmd was called %d times after init, want not called", extraCall, runCounts)
				}

				if runCounts != 1 && !tt.cmdNotRunAfterInit {
					t.Errorf("call #%d runCmd was called %d times after init, want called only once", extraCall, runCounts)
				}
			}
		})
	}
}

func Test_runCmd(t *testing.T) {
	const (
		dataFile     = "testdata/dell-r310-ipmi-sensors.txt"
		smallMaxSize = 1024
	)

	testdataContent, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		args       []string
		searchPath string
		wantOutput string
		wantErr    bool
		ctxTimeout time.Duration
	}{
		{
			name: "ok-empty-output",
			args: []string{"true"},
		},
		{
			name:    "fail-empty-output",
			args:    []string{"false"},
			wantErr: true,
		},
		{
			name:       "ok-with-output",
			args:       []string{"sh", "-c", "echo test; true"},
			wantOutput: "test\n",
		},
		{
			name:       "fail-empty-output",
			args:       []string{"sh", "-c", "echo test; false"},
			wantOutput: "test\n",
			wantErr:    true,
		},
		{
			name:       "timeout-with-output",
			args:       []string{"sh", "-c", "echo test; sleep 5; echo test2"},
			ctxTimeout: time.Second,
			wantOutput: "test\n",
			wantErr:    true,
		},
		{
			name:       "cat",
			args:       []string{"cat", dataFile},
			wantOutput: string(testdataContent),
		},
		{
			name:       "searchPath-good",
			args:       []string{"sh", "-c", "echo searchPath"},
			searchPath: "/bin",
			wantOutput: "searchPath\n",
		},
		{
			name:       "searchPath-bad",
			args:       []string{"sh", "-c", "echo searchPath"},
			searchPath: "/does-not-exists",
			wantErr:    true,
		},
	}

	realRunner := gloutonexec.New("/")
	realRunCmd := runCmdWithRunner(realRunner)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()

			if tt.ctxTimeout > 0 {
				var cancel context.CancelFunc

				ctx, cancel = context.WithTimeout(ctx, tt.ctxTimeout)
				defer cancel()
			}

			output, err := realRunCmd(ctx, tt.searchPath, false, tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tt.wantErr)
			}

			outputStr := string(output)

			if diff := cmp.Diff(tt.wantOutput, outputStr); diff != "" {
				t.Errorf("output mismatch: (-want +got)\n%s", diff)
			}
		})
	}
}

func Test_readingToPoints(t *testing.T) {
	// timestamp are ignored in reading for now
	timestamp1 := time.Date(2023, 1, 2, 3, 4, 5, 0, time.UTC)
	timestamp2 := time.Date(2023, 5, 4, 3, 2, 1, 0, time.UTC)

	tests := []struct {
		name     string
		readings []powerReading
		want     []types.MetricPoint
	}{
		{
			name: "first current is the default",
			readings: []powerReading{
				{
					Current:      123,
					Average:      456,
					Minimum:      1,
					Maximum:      999,
					Timestamp:    timestamp1,
					ReportPeriod: time.Hour,
				},
				{
					Current:      234,
					Average:      567,
					Minimum:      1,
					Maximum:      999,
					Timestamp:    timestamp2,
					ReportPeriod: 24 * time.Hour,
				},
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Value: 123,
					},
				},
			},
		},
		{
			name: "timestamp doesn't matter",
			readings: []powerReading{
				{
					Current:      123,
					Average:      456,
					Minimum:      1,
					Maximum:      999,
					Timestamp:    timestamp2,
					ReportPeriod: time.Hour,
				},
				{
					Current:      234,
					Average:      567,
					Minimum:      1,
					Maximum:      999,
					Timestamp:    timestamp1,
					ReportPeriod: 24 * time.Hour,
				},
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Value: 123,
					},
				},
			},
		},
		{
			name: "average is used if reporting period is good",
			readings: []powerReading{
				{
					Current:      123,
					Average:      456,
					Minimum:      1,
					Maximum:      999,
					Timestamp:    timestamp1,
					ReportPeriod: time.Minute,
				},
				{
					Current:      234,
					Average:      567,
					Minimum:      1,
					Maximum:      999,
					Timestamp:    timestamp1,
					ReportPeriod: 24 * time.Hour,
				},
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Value: 456,
					},
				},
			},
		},
		{
			name: "average is used if reporting period is good 2",
			readings: []powerReading{
				{
					Current:      123,
					Average:      456,
					Minimum:      1,
					Maximum:      999,
					Timestamp:    timestamp1,
					ReportPeriod: 10 * time.Second,
				},
				{
					Current:      234,
					Average:      567,
					Minimum:      1,
					Maximum:      999,
					Timestamp:    timestamp1,
					ReportPeriod: 24 * time.Hour,
				},
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Value: 456,
					},
				},
			},
		},
		{
			name: "the best average is used if reporting period are good",
			readings: []powerReading{
				{
					Current:      123,
					Average:      456,
					Minimum:      1,
					Maximum:      999,
					Timestamp:    timestamp1,
					ReportPeriod: 10 * time.Second,
				},
				{
					Current:      234,
					Average:      567,
					Minimum:      1,
					Maximum:      999,
					Timestamp:    timestamp1,
					ReportPeriod: time.Minute,
				},
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Value: 567,
					},
				},
			},
		},
		{
			name: "average isn't used on strange period",
			readings: []powerReading{
				{
					Current:      123,
					Average:      456,
					Minimum:      1,
					Maximum:      999,
					Timestamp:    timestamp1,
					ReportPeriod: 1 * time.Second, // this happen on some Dell. I thing this means "cumulative since last reset of counters"
				},
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Value: 123,
					},
				},
			},
		},
		{
			name: "average isn't used on strange period 2",
			readings: []powerReading{
				{
					Current:      123,
					Average:      456,
					Minimum:      1,
					Maximum:      999,
					Timestamp:    timestamp1,
					ReportPeriod: 0 * time.Second, // this happen on some Dell. I thing this means "cumulative since last reset of counters"
				},
			},
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: metricSystemPowerConsumptionName,
					},
					Point: types.Point{
						Value: 123,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := readingToPoints(tt.readings)

			if diff := types.DiffMetricPoints(tt.want, got, false); diff != "" {
				t.Errorf("readingToPoints mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
