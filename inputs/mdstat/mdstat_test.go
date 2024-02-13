package mdstat

import (
	"context"
	"glouton/inputs"
	"glouton/prometheus/model"
	"glouton/prometheus/registry"
	"glouton/types"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/influxdata/telegraf"
	dto "github.com/prometheus/client_model/go"
)

//nolint:gochecknoinits
func init() {
	timeNow = func() time.Time {
		return time.Date(2024, 2, 13, 10, 35, 0, 0, time.Local)
	}
}

func setupMdstatTest(t *testing.T, inputFileName string) (input telegraf.Input, deferFn func()) {
	t.Helper()

	tempDir, err := os.MkdirTemp("", "mdstat_test")
	if err != nil {
		t.Fatal("Failed to create temporary test dir:", err)
	}

	deferFn = func() {
		err := os.RemoveAll(tempDir)
		if err != nil {
			t.Logf("Failed to remove test dir at %s: %v", tempDir, err)
		}
	}

	mdstatFilePath := filepath.Join(tempDir, mdstatPath)

	err = os.Mkdir(filepath.Dir(mdstatFilePath), 0o700)
	if err != nil {
		deferFn()
		t.Fatal("Failed to create test dir:", err)
	}

	mdstatFile, err := os.Create(mdstatFilePath)
	if err != nil {
		deferFn()
		t.Fatal("Failed to create temporary mdstat file:", err)
	}

	defer mdstatFile.Close()

	inputFile, err := os.Open("testdata/" + inputFileName)
	if err != nil {
		deferFn()
		t.Fatal("Failed to open testdata file:", err)
	}

	defer inputFile.Close()

	_, err = io.Copy(mdstatFile, inputFile)
	if err != nil {
		deferFn()
		t.Fatalf("Failed to copy mdstat data from %s to %s: %v", inputFile.Name(), mdstatFilePath, err)
	}

	mdstatInput, _, err := New(tempDir)
	if err != nil {
		deferFn()
		t.Fatal("Failed to initialize mdstat input:", err)
	}

	if initInput, ok := mdstatInput.(telegraf.Initializer); ok {
		err = initInput.Init()
		if err != nil {
			deferFn()
			t.Fatal("Failed to initialize input:", err)
		}
	}

	return mdstatInput, deferFn
}

func TestGather(t *testing.T) { //nolint:maintidx
	testCases := []struct {
		filename        string
		expectedMetrics metricFamilies
	}{
		{ //nolint: dupl
			filename: "simple_active.txt",
			expectedMetrics: map[string][]metric{
				"mdstat_blocks_synced_finish_time_status": {
					{
						Labels: map[string]string{
							types.LabelItem:                   "md0",
							types.LabelMetaCurrentStatus:      types.StatusOk.String(),
							types.LabelMetaCurrentDescription: "",
						},
						Value: float64(types.StatusOk.NagiosCode()),
					},
				},
				"mdstat_blocks_synced_pct": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 0},
				},
				"mdstat_disks_active_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 2},
				},
				"mdstat_disks_activity_state_status": {
					{
						Labels: map[string]string{
							types.LabelItem:                   "md0",
							types.LabelMetaCurrentStatus:      types.StatusOk.String(),
							types.LabelMetaCurrentDescription: "The disk is currently active",
						},
						Value: float64(types.StatusOk.NagiosCode()),
					},
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
			},
		},
		{
			filename: "multiple_active.txt",
			expectedMetrics: map[string][]metric{
				"mdstat_blocks_synced_finish_time_status": {
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
				"mdstat_blocks_synced_pct": {
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md3"}, Value: 0},
				},
				"mdstat_disks_active_count": {
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 2},
					{Labels: map[string]string{types.LabelItem: "md2"}, Value: 2},
					{Labels: map[string]string{types.LabelItem: "md3"}, Value: 10},
				},
				"mdstat_disks_activity_state_status": {
					{
						Labels: map[string]string{
							types.LabelItem:                   "md1",
							types.LabelMetaCurrentStatus:      "ok",
							types.LabelMetaCurrentDescription: "The disk is currently active",
						},
						Value: float64(types.StatusOk.NagiosCode()),
					},
					{
						Labels: map[string]string{
							types.LabelItem:                   "md2",
							types.LabelMetaCurrentStatus:      "ok",
							types.LabelMetaCurrentDescription: "The disk is currently active",
						},
						Value: float64(types.StatusOk.NagiosCode()),
					},
					{
						Labels: map[string]string{
							types.LabelItem:                   "md3",
							types.LabelMetaCurrentStatus:      "ok",
							types.LabelMetaCurrentDescription: "The disk is currently active",
						},
						Value: float64(types.StatusOk.NagiosCode()),
					},
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
			},
		},
		{
			filename: "multiple_failed_spare.txt",
			expectedMetrics: map[string][]metric{
				"mdstat_blocks_synced_finish_time_status": {
					{
						Labels: map[string]string{
							types.LabelItem:                   "md0",
							types.LabelMetaCurrentStatus:      types.StatusOk.String(),
							types.LabelMetaCurrentDescription: "",
						},
						Value: float64(types.StatusOk.NagiosCode()),
					},
					{
						Labels: map[string]string{
							types.LabelItem:                   "md1",
							types.LabelMetaCurrentStatus:      types.StatusOk.String(),
							types.LabelMetaCurrentDescription: "",
						},
						Value: float64(types.StatusOk.NagiosCode()),
					},
				},
				"mdstat_blocks_synced_pct": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 0},
				},
				"mdstat_disks_active_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 1},
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 2},
				},
				"mdstat_disks_activity_state_status": {
					{
						Labels: map[string]string{
							types.LabelItem:                   "md0",
							types.LabelMetaCurrentStatus:      "ok",
							types.LabelMetaCurrentDescription: "The disk is currently active",
						},
						Value: float64(types.StatusOk.NagiosCode()),
					},
					{
						Labels: map[string]string{
							types.LabelItem:                   "md1",
							types.LabelMetaCurrentStatus:      "ok",
							types.LabelMetaCurrentDescription: "The disk is currently active",
						},
						Value: float64(types.StatusOk.NagiosCode()),
					},
				},
				"mdstat_disks_down_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 1},
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 0},
				},
				"mdstat_disks_failed_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 1},
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 0},
				},
				"mdstat_disks_spare_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 0},
					{Labels: map[string]string{types.LabelItem: "md1"}, Value: 1},
				},
			},
		},
		{ //nolint: dupl
			filename: "simple_recovery.txt",
			expectedMetrics: map[string][]metric{
				"mdstat_blocks_synced_finish_time_status": {
					{
						Labels: map[string]string{
							types.LabelItem:                   "md0",
							types.LabelMetaCurrentStatus:      types.StatusWarning.String(),
							types.LabelMetaCurrentDescription: "The disk should be fully synchronized in 3min (around 10:37:00)",
						},
						Value: float64(types.StatusWarning.NagiosCode()),
					},
				},
				"mdstat_blocks_synced_pct": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 9.9},
				},
				"mdstat_disks_active_count": {
					{Labels: map[string]string{types.LabelItem: "md0"}, Value: 1},
				},
				"mdstat_disks_activity_state_status": {
					{
						Labels: map[string]string{
							types.LabelItem:                   "md0",
							types.LabelMetaCurrentStatus:      types.StatusWarning.String(),
							types.LabelMetaCurrentDescription: "The disk is currently recovering",
						},
						Value: float64(types.StatusWarning.NagiosCode()),
					},
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
			},
		},
	}

	for _, testCase := range testCases {
		tc := testCase

		t.Run(tc.filename, func(t *testing.T) {
			input, deferFn := setupMdstatTest(t, tc.filename)
			defer deferFn()

			pointBuffer := new(registry.PointBuffer)
			acc := inputs.Accumulator{
				Pusher:  pointBuffer,
				Context: context.Background(),
			}

			err := input.Gather(&acc)
			if err != nil {
				t.Fatal("Failed to run gathering:", err)
			}

			mfs := model.MetricPointsToFamilies(pointBuffer.Points())
			mfs = gatherModifier(mfs, nil)

			if diff := cmp.Diff(tc.expectedMetrics, convert(mfs)); diff != "" {
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
