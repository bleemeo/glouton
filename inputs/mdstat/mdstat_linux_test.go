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

//go:build linux

package mdstat

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs/mdstat"
	dto "github.com/prometheus/client_model/go"
	"gopkg.in/yaml.v3"
)

var errArrayMdadmDetailsNotFound = errors.New("mdadm details not found")

// timeNow always returns the same timestamp: February 13, 2024, at 10:35am.
func timeNow() time.Time {
	return time.Date(2024, 2, 13, 10, 35, 0, 0, time.Local)
}

func setupMdstatTest(t *testing.T, name string) (input telegraf.Input, mdadmDetailsFn mdadmDetailsFunc) {
	t.Helper()

	tempDir := t.TempDir()
	mdstatFilePath := filepath.Join(tempDir, mdstatPath)

	err := os.Mkdir(filepath.Dir(mdstatFilePath), 0o700)
	if err != nil {
		t.Fatal("Failed to create test dir:", err)
	}

	mdstatFile, err := os.Create(mdstatFilePath)
	if err != nil {
		t.Fatal("Failed to create temporary mdstat file:", err)
	}

	defer mdstatFile.Close()

	mdstatInputFile, err := os.Open(filepath.Join("testdata", "mdstat", name+".txt"))
	if err != nil {
		t.Fatal("Failed to open testdata file:", err)
	}

	defer mdstatInputFile.Close()

	_, err = io.Copy(mdstatFile, mdstatInputFile)
	if err != nil {
		t.Fatalf("Failed to copy mdstat data from %s to %s: %v", mdstatInputFile.Name(), mdstatFilePath, err)
	}

	mdadmDetailsFile, err := os.Open(filepath.Join("testdata", "mdadm", name+".yml"))
	if err != nil {
		t.Fatal("Failed to open mdadm details:", err)
	}

	defer mdadmDetailsFile.Close()

	var mdadmDetails map[string]string

	err = yaml.NewDecoder(mdadmDetailsFile).Decode(&mdadmDetails)
	if err != nil {
		t.Fatal("Failed to parse mdadm details:", err)
	}

	mdstatInput, _, err := New(config.Mdstat{}) // the config won't be used
	if err != nil {
		t.Fatal("Failed to initialize mdstat input:", err)
	}

	underlyingInput := mdstatInput.(*internal.Input).Input.(*mdstat.Mdstat) //nolint: forcetypeassert
	underlyingInput.FileName = mdstatFilePath

	if initInput, ok := mdstatInput.(telegraf.Initializer); ok {
		err = initInput.Init()
		if err != nil {
			t.Fatal("Failed to initialize input:", err)
		}
	}

	mdadmDetailsFn = func(array, _ string, _ bool) (mdadmInfo, error) {
		mdadmDetailsOutput, ok := mdadmDetails[array]
		if !ok {
			return mdadmInfo{}, fmt.Errorf("%w for array %s", errArrayMdadmDetailsNotFound, array)
		}

		return parseMdadmOutput(mdadmDetailsOutput)
	}

	return mdstatInput, mdadmDetailsFn
}

func TestGather(t *testing.T) { //nolint:maintidx
	testCases := []struct {
		name            string
		expectedMetrics metricFamilies
	}{
		{ //nolint: dupl
			name: "multiple_active",
			expectedMetrics: map[string][]metric{
				"mdstat_blocks_synced_finish_time": {
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md3"}, Value: 0},
				},
				"mdstat_blocks_synced": {
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 136448},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 1.29596288e+08},
					{Labels: map[string]string{types.LabelItem: "md3"}, Value: 1.318680576e+09},
				},
				"mdstat_blocks_synced_pct": {
					{Labels: map[string]string{types.LabelItem: "md1"}},
					{Labels: map[string]string{types.LabelItem: "md2"}},
					{Labels: map[string]string{types.LabelItem: "md3"}},
				},
				"mdstat_disks_active_count": {
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 2},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 2},
					{Labels: map[string]string{types.LabelItem: "md3"}, Value: 10},
				},
				"mdstat_disks_down_count": {
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md3"}, Value: 0},
				},
				"mdstat_disks_failed_count": {
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md3"}, Value: 0},
				},
				"mdstat_disks_spare_count": {
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md3"}, Value: 0},
				},
				"mdstat_disks_total_count": {
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 2},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 2},
					{Labels: map[string]string{types.LabelItem: "md3"}, Value: 10},
				},
				"mdstat_health_status": {
					{
						Labels: map[string]string{
							types.LabelItem:                   "md1",
							types.LabelMetaCurrentStatus:      types.StatusOk.String(),
							types.LabelMetaCurrentDescription: "",
						},
						Value: float64(types.StatusOk.NagiosCode()),
					},
					{
						Labels: map[string]string{
							types.LabelItem:                   "md2",
							types.LabelMetaCurrentStatus:      types.StatusOk.String(),
							types.LabelMetaCurrentDescription: "",
						},
						Value: float64(types.StatusOk.NagiosCode()),
					},
					{
						Labels: map[string]string{
							types.LabelItem:                   "md3",
							types.LabelMetaCurrentStatus:      types.StatusOk.String(),
							types.LabelMetaCurrentDescription: "",
						},
						Value: float64(types.StatusOk.NagiosCode()),
					},
				},
			},
		},
		{
			name: "multiple_delayed",
			expectedMetrics: map[string][]metric{
				"mdstat_blocks_synced_finish_time": {
					{Labels: map[string]string{types.LabelItem: "md1"}},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 147.6},
					{Labels: map[string]string{types.LabelItem: "md3"}, Value: 0.9},
				},
				"mdstat_blocks_synced": {
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 0}, // <- important: 0 means delayed
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 4.238336e+07},
					{Labels: map[string]string{types.LabelItem: "md3"}, Value: 38528},
				},
				"mdstat_blocks_synced_pct": {
					{Labels: map[string]string{types.LabelItem: "md1"}},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 2.2},
					{Labels: map[string]string{types.LabelItem: "md3"}, Value: 37.6},
				},
				"mdstat_disks_active_count": {
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 2},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 2},
					{Labels: map[string]string{types.LabelItem: "md3"}, Value: 2},
				},
				"mdstat_disks_down_count": {
					{Labels: map[string]string{types.LabelItem: "md1"}},
					{Labels: map[string]string{types.LabelItem: "md2"}},
					{Labels: map[string]string{types.LabelItem: "md3"}},
				},
				"mdstat_disks_failed_count": {
					{Labels: map[string]string{types.LabelItem: "md1"}},
					{Labels: map[string]string{types.LabelItem: "md2"}},
					{Labels: map[string]string{types.LabelItem: "md3"}},
				},
				"mdstat_disks_spare_count": {
					{Labels: map[string]string{types.LabelItem: "md1"}},
					{Labels: map[string]string{types.LabelItem: "md2"}},
					{Labels: map[string]string{types.LabelItem: "md3"}},
				},
				"mdstat_disks_total_count": {
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 2},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 2},
					{Labels: map[string]string{types.LabelItem: "md3"}, Value: 2},
				},
				"mdstat_health_status": {
					{
						Labels: map[string]string{
							types.LabelItem:                   "md1",
							types.LabelMetaCurrentStatus:      types.StatusOk.String(),
							types.LabelMetaCurrentDescription: "",
						},
					},
					{
						Labels: map[string]string{
							types.LabelItem:                   "md2",
							types.LabelMetaCurrentStatus:      types.StatusOk.String(),
							types.LabelMetaCurrentDescription: "",
						},
					},
					{
						Labels: map[string]string{
							types.LabelItem:                   "md3",
							types.LabelMetaCurrentStatus:      types.StatusWarning.String(),
							types.LabelMetaCurrentDescription: "The array is currently resyncing, which should be done in 1 minute (around 10:35:00)",
						},
						Value: float64(types.StatusWarning.NagiosCode()),
					},
				},
			},
		},
		{ //nolint: dupl
			name: "multiple_failed_spare",
			expectedMetrics: map[string][]metric{
				"mdstat_blocks_synced_finish_time": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 0},
				},
				"mdstat_blocks_synced": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 9.76773168e+08},
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 1.42191616e+08},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 1.046528e+06},
				},
				"mdstat_blocks_synced_pct": {
					{Labels: map[string]string{types.LabelItem: "md0"}},
					{Labels: map[string]string{types.LabelItem: "md1"}},
					{Labels: map[string]string{types.LabelItem: "md2"}},
				},
				"mdstat_disks_active_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 1},
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 2},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 2},
				},
				"mdstat_disks_down_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 1},
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 0},
				},
				"mdstat_disks_failed_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 1},
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 0},
				},
				"mdstat_disks_spare_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 1},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 0},
				},
				"mdstat_disks_total_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 2},
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 2},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 2},
				},
				"mdstat_health_status": {
					{
						Labels: map[string]string{
							types.LabelItem:                   "md0",
							types.LabelMetaCurrentStatus:      types.StatusWarning.String(),
							types.LabelMetaCurrentDescription: "The array is degraded, 1 disk is failing",
						},
						Value: float64(types.StatusWarning.NagiosCode()),
					},
					{
						Labels: map[string]string{
							types.LabelItem:                   "md1",
							types.LabelMetaCurrentStatus:      types.StatusOk.String(),
							types.LabelMetaCurrentDescription: "",
						},
						Value: float64(types.StatusOk.NagiosCode()),
					},
					{
						Labels: map[string]string{
							types.LabelItem:                   "md2",
							types.LabelMetaCurrentStatus:      types.StatusOk.String(),
							types.LabelMetaCurrentDescription: "",
						},
						Value: float64(types.StatusOk.NagiosCode()),
					},
				},
			},
		},
		{ //nolint: dupl
			name: "simple_active",
			expectedMetrics: map[string][]metric{
				"mdstat_blocks_synced_finish_time": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 0},
				},
				"mdstat_blocks_synced": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 1.046528e+06},
				},
				"mdstat_blocks_synced_pct": {
					{Labels: map[string]string{types.LabelItem: "md0"}},
				},
				"mdstat_disks_active_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 2},
				},
				"mdstat_disks_down_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 0},
				},
				"mdstat_disks_failed_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 0},
				},
				"mdstat_disks_spare_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 0},
				},
				"mdstat_disks_total_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 2},
				},
				"mdstat_health_status": {
					{
						Labels: map[string]string{
							types.LabelItem:                   "md0",
							types.LabelMetaCurrentStatus:      types.StatusOk.String(),
							types.LabelMetaCurrentDescription: "",
						},
						Value: float64(types.StatusOk.NagiosCode()),
					},
				},
			},
		},
		{ //nolint: dupl
			name: "simple_failed",
			expectedMetrics: map[string][]metric{
				"mdstat_blocks_synced_finish_time": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 0},
				},
				"mdstat_blocks_synced": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 1.95352512e+09},
				},
				"mdstat_blocks_synced_pct": {
					{Labels: map[string]string{types.LabelItem: "md0"}},
				},
				"mdstat_disks_active_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 0},
				},
				"mdstat_disks_down_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 0},
				},
				"mdstat_disks_failed_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 5},
				},
				"mdstat_disks_spare_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 0},
				},
				"mdstat_disks_total_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 0},
				},
				"mdstat_health_status": {
					{
						Labels: map[string]string{
							types.LabelItem:                   "md0",
							types.LabelMetaCurrentStatus:      types.StatusCritical.String(),
							types.LabelMetaCurrentDescription: "The array is currently inactive",
						},
						Value: float64(types.StatusCritical.NagiosCode()),
					},
				},
			},
		},
		{
			name: "simple_recovery",
			expectedMetrics: map[string][]metric{
				"mdstat_blocks_synced_finish_time": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 2.8},
				},
				"mdstat_blocks_synced": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 2.423168e+06},
				},
				"mdstat_blocks_synced_pct": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 9.9},
				},
				"mdstat_disks_active_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 1},
				},
				"mdstat_disks_down_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 1},
				},
				"mdstat_disks_failed_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 0},
				},
				"mdstat_disks_spare_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 0},
				},
				"mdstat_disks_total_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 2},
				},
				"mdstat_health_status": {
					{
						Labels: map[string]string{
							types.LabelItem:                   "md0",
							types.LabelMetaCurrentStatus:      types.StatusWarning.String(),
							types.LabelMetaCurrentDescription: "The array is degraded. The array should be fully synchronized in 3 minutes (around 10:37:00)",
						},
						Value: float64(types.StatusWarning.NagiosCode()),
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			input, mdadmDetailsFn := setupMdstatTest(t, tc.name)

			pointBuffer := new(registry.PointBuffer)
			acc := inputs.Accumulator{
				Pusher:  pointBuffer,
				Context: t.Context(),
			}

			err := input.Gather(&acc)
			if err != nil {
				t.Fatal("Failed to run gathering:", err)
			}

			mfs := model.MetricPointsToFamilies(pointBuffer.Points())
			mfs = gatherModifier("won't be used", false, timeNow, mdadmDetailsFn)(mfs, nil)

			if diff := cmp.Diff(tc.expectedMetrics, convert(mfs), cmpopts.SortSlices(metricSorter)); diff != "" {
				t.Fatalf("Unexpected metrics (-want +got):\n%s", diff)
			}
		})
	}
}

type metricFamilies map[string][]metric

type metric struct {
	Labels map[string]string
	Value  float64
}

func convert(mfs []*dto.MetricFamily) metricFamilies {
	families := make(metricFamilies, len(mfs))

	for _, mf := range mfs {
		if mf == nil {
			continue
		}

		if _, exists := families[mf.GetName()]; !exists {
			families[mf.GetName()] = make([]metric, 0, len(mf.GetMetric()))
		}

		for _, m := range mf.GetMetric() {
			if m == nil {
				continue
			}

			labels := make(map[string]string, len(m.GetLabel()))

			for _, pair := range m.GetLabel() {
				labels[pair.GetName()] = pair.GetValue()
			}

			metric := metric{
				Labels: labels,
				Value:  m.GetUntyped().GetValue(),
			}

			families[mf.GetName()] = append(families[mf.GetName()], metric)
		}
	}

	return families
}

func metricSorter(m1, m2 metric) bool {
	return m1.Labels[types.LabelItem] < m2.Labels[types.LabelItem]
}

func TestFormatRemainingTime(t *testing.T) {
	cases := []struct {
		timeLeft time.Duration
		expected string
	}{
		{
			// This is assumed to be a very high value, likely unreachable
			// (that duration is a 4 TB raid with 50MB/s synchronization).
			// For now, we don't show the day even if it changes.
			timeLeft: 1398 * time.Minute,
			expected: "1398 minutes (around 09:53:00)",
		},
		{
			timeLeft: 95 * time.Minute,
			expected: "95 minutes (around 12:10:00)",
		},
		{
			timeLeft: 5 * time.Minute,
			expected: "5 minutes (around 10:40:00)",
		},
		{
			timeLeft: 150 * time.Second,
			expected: "3 minutes (around 10:37:00)",
		},
		{
			timeLeft: 5 * time.Second,
			expected: "1 minute (around 10:35:00)",
		},
		{
			timeLeft: 0,
			expected: "a few moments",
		},
	}

	for _, tc := range cases {
		t.Run(tc.timeLeft.String(), func(t *testing.T) {
			result := formatRemainingTime(tc.timeLeft.Minutes(), timeNow)
			if result != tc.expected {
				t.Fatalf("Unexpected result of formatRemainingTime(%s, _): want %q, got %q", tc.timeLeft, tc.expected, result)
			}
		})
	}
}
