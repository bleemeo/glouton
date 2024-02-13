package mdstat

import (
	"fmt"
	"glouton/inputs"
	"glouton/inputs/internal"
	"glouton/logger"
	"glouton/types"
	"math"
	"path/filepath"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/mdstat"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/proto"
)

const mdstatPath = "/proc/mdstat"

var timeNow = time.Now //nolint:gochecknoglobals

func New(hostroot string) (telegraf.Input, *inputs.GathererOptions, error) {
	input, ok := telegraf_inputs.Inputs["mdstat"]
	if !ok {
		return nil, nil, inputs.ErrDisabledInput
	}

	mdstatConfig, ok := input().(*mdstat.MdstatConf)
	if !ok {
		return nil, nil, inputs.ErrUnexpectedType
	}

	mdstatConfig.FileName = filepath.Join(hostroot, mdstatPath)

	internalInput := &internal.Input{
		Input: mdstatConfig,
		Accumulator: internal.Accumulator{
			TransformMetrics: transformMetrics,
		},
		Name: "mdstat",
	}

	options := &inputs.GathererOptions{
		MinInterval:    60 * time.Second,
		GatherModifier: gatherModifier,
	}

	return internalInput, options, nil
}

var fieldsNameMapping = map[string]string{ //nolint:gochecknoglobals
	"BlocksSyncedFinishTime": "blocks_synced_finish_time_status", // will be used for a metric status
	"BlocksSyncedPct":        "blocks_synced_pct",
	"DisksActive":            "disks_active_count",
	"DisksDown":              "disks_down_count",
	"DisksFailed":            "disks_failed_count",
	"DisksSpare":             "disks_spare_count",
}

func transformMetrics(_ internal.GatherContext, fields map[string]float64, _ map[string]interface{}) map[string]float64 {
	finalFields := make(map[string]float64)

	for field, value := range fields {
		newName, ok := fieldsNameMapping[field]

		if !ok {
			continue
		}

		finalFields[newName] = value
	}

	return finalFields
}

func gatherModifier(mfs []*dto.MetricFamily, _ error) []*dto.MetricFamily {
	disksActivityStateStatus := &dto.MetricFamily{
		Name:   proto.String("mdstat_disks_activity_state_status"),
		Type:   dto.MetricType_UNTYPED.Enum(),
		Metric: make([]*dto.Metric, 0),
	}
	activityStatePerItem := make(map[string]bool)

	for _, mf := range mfs {
		if mf == nil {
			continue
		}

		metricCount := 0

		for mi := 0; mi < len(mf.Metric); mi++ { //nolint:protogetter
			m := mf.Metric[mi] //nolint:protogetter
			if m == nil {
				continue
			}

			item, activityState := parseLabels(m.GetLabel())

			if !activityStatePerItem[item] {
				activityStateStatusMetric := generateActivityStatusMetric(activityState, item, m.GetTimestampMs())
				disksActivityStateStatus.Metric = append(disksActivityStateStatus.Metric, activityStateStatusMetric) //nolint:protogetter
				activityStatePerItem[item] = true
			}

			m.Label = []*dto.LabelPair{
				{
					Name:  proto.String(types.LabelItem),
					Value: proto.String(item),
				},
			}

			if mf.GetName() == "mdstat_blocks_synced_finish_time_status" {
				status, labels := generateFinishTimeStatusLabels(m.GetUntyped().GetValue())
				m.Untyped = &dto.Untyped{Value: status}
				m.Label = append(m.Label, labels...) //nolint:protogetter
			}

			mf.Metric[metricCount] = m
			metricCount++
		}

		mf.Metric = mf.Metric[:metricCount] //nolint:protogetter
	}

	mfs = append(mfs, disksActivityStateStatus)

	return mfs
}

func parseLabels(labels []*dto.LabelPair) (item, activityState string) {
	for _, label := range labels {
		switch label.GetName() {
		case "Name":
			item = label.GetValue()
		case "ActivityState":
			activityState = label.GetValue()
		}
	}

	return item, activityState
}

func generateActivityStatusMetric(activityState string, item string, ts int64) *dto.Metric {
	var status types.Status

	switch activityState {
	case "active":
		status = types.StatusOk
	case "inactive":
		status = types.StatusCritical
	case "recovering":
		status = types.StatusWarning
	case "checking":
		status = types.StatusUnset
	case "resyncing":
	default:
		logger.V(2).Printf("Unknown activity state %q on disk %q", activityState, item)

		status = types.StatusUnknown
	}

	return &dto.Metric{
		Label: []*dto.LabelPair{
			{Name: proto.String(types.LabelItem), Value: proto.String(item)},
			{Name: proto.String(types.LabelMetaCurrentStatus), Value: proto.String(status.String())},
			{Name: proto.String(types.LabelMetaCurrentDescription), Value: proto.String("The disk is currently " + activityState)},
		},
		Untyped:     &dto.Untyped{Value: proto.Float64(float64(status.NagiosCode()))},
		TimestampMs: proto.Int64(ts),
	}
}

func generateFinishTimeStatusLabels(minutesLeft float64) (*float64, []*dto.LabelPair) {
	var (
		status      types.Status
		description string
	)

	if minutesLeft > 0 {
		status = types.StatusWarning
		estimatedTime := timeNow().Add(time.Duration(minutesLeft) * time.Minute)
		description = fmt.Sprintf(
			"The disk should be fully synchronized in %dmin (around %s)",
			int(math.Ceil(minutesLeft)),
			estimatedTime.Format(time.TimeOnly),
		)
	} else {
		status = types.StatusOk
	}

	return proto.Float64(float64(status.NagiosCode())), []*dto.LabelPair{
		{Name: proto.String(types.LabelMetaCurrentStatus), Value: proto.String(status.String())},
		{Name: proto.String(types.LabelMetaCurrentDescription), Value: proto.String(description)},
	}
}
