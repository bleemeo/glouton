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

package mdstat

import (
	"fmt"
	"glouton/inputs"
	"glouton/inputs/internal"
	"glouton/logger"
	"glouton/types"
	"math"
	"strings"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/mdstat"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/proto"
)

const mdstatPath = "/proc/mdstat"

func New(mdadmPath string) (telegraf.Input, *inputs.GathererOptions, error) {
	input, ok := telegraf_inputs.Inputs["mdstat"]
	if !ok {
		return nil, nil, inputs.ErrDisabledInput
	}

	mdstatConfig, ok := input().(*mdstat.MdstatConf)
	if !ok {
		return nil, nil, inputs.ErrUnexpectedType
	}

	mdstatConfig.FileName = mdstatPath

	internalInput := &internal.Input{
		Input: mdstatConfig,
		Accumulator: internal.Accumulator{
			TransformMetrics: transformMetrics,
		},
		Name: "mdstat",
	}

	options := &inputs.GathererOptions{
		MinInterval:    60 * time.Second,
		GatherModifier: gatherModifier(mdadmPath, time.Now, callMdadm),
	}

	return internalInput, options, nil
}

var fieldsNameMapping = map[string]string{ //nolint:gochecknoglobals
	"BlocksSyncedFinishTime": "blocks_synced_finish_time", // will be used for a metric status
	"BlocksSyncedPct":        "blocks_synced_pct",
	"DisksActive":            "disks_active_count",
	"DisksDown":              "disks_down_count",
	"DisksFailed":            "disks_failed_count",
	"DisksSpare":             "disks_spare_count",
	"DisksTotal":             "disks_total_count",
}

func transformMetrics(_ internal.GatherContext, fields map[string]float64, _ map[string]interface{}) map[string]float64 {
	finalFields := make(map[string]float64, len(fieldsNameMapping))

	for field, value := range fields {
		newName, ok := fieldsNameMapping[field]
		if !ok {
			continue
		}

		finalFields[newName] = value
	}

	return finalFields
}

type arrayInfo struct {
	timestamp                   int64
	active, down, failed, total int
	activityState               string
	recoveryMinutes             float64
}

func gatherModifier(mdadmPath string, timeNow func() time.Time, mdadmDetails mdadmDetailsFunc) func(mfs []*dto.MetricFamily, _ error) []*dto.MetricFamily {
	return func(mfs []*dto.MetricFamily, _ error) []*dto.MetricFamily {
		infoPerArray := make(map[string]arrayInfo)

		for _, mf := range mfs {
			if mf == nil {
				continue
			}

			for mIdx, m := range mf.GetMetric() {
				array, activityState := parseLabels(m.GetLabel())
				m.Label = []*dto.LabelPair{
					{
						Name:  proto.String(types.LabelItem),
						Value: proto.String(array),
					},
				}

				info, exists := infoPerArray[array]
				if !exists {
					info = arrayInfo{
						timestamp:     m.GetTimestampMs(),
						activityState: activityState,
					}
				}

				switch mf.GetName() {
				case "mdstat_disks_active_count":
					info.active = int(m.GetUntyped().GetValue())
				case "mdstat_disks_down_count":
					info.down = int(m.GetUntyped().GetValue())
				case "mdstat_disks_failed_count":
					info.failed = int(m.GetUntyped().GetValue())
				case "mdstat_disks_total_count":
					info.total = int(m.GetUntyped().GetValue())
				case "mdstat_blocks_synced_finish_time":
					info.recoveryMinutes = m.GetUntyped().GetValue()
				}

				infoPerArray[array] = info
				mf.Metric[mIdx] = m
			}
		}

		disksActivityStateStatus := &dto.MetricFamily{
			Name:   proto.String("mdstat_health_status"),
			Type:   dto.MetricType_UNTYPED.Enum(),
			Metric: make([]*dto.Metric, 0, len(infoPerArray)),
		}

		for array, info := range infoPerArray {
			healthStatusMetric := makeHealthStatusMetric(array, info, mdadmPath, timeNow, mdadmDetails)
			disksActivityStateStatus.Metric = append(disksActivityStateStatus.Metric, healthStatusMetric) //nolint:protogetter
		}

		mfs = append(mfs, disksActivityStateStatus)

		return mfs
	}
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

func makeHealthStatusMetric(array string, info arrayInfo, mdadmPath string, timeNow func() time.Time, mdadmDetails mdadmDetailsFunc) *dto.Metric {
	var (
		status      types.Status
		description string
	)

	if info.active != info.total { // Something is going on, and we may not have enough information with /proc/mdstat
		details, err := mdadmDetails(array, mdadmPath)
		if err != nil {
			logger.V(1).Printf("MD: %v", err)
		}

		switch {
		case strings.Contains(details.state, "FAILED"),
			strings.Contains(details.state, "broken"):
			status = types.StatusCritical
			description = fmt.Sprintf("The array is failed, %d disks are failing", info.failed)
		case strings.Contains(details.state, "degraded"):
			status = types.StatusWarning
			s, verb := plural(info.failed)
			description = fmt.Sprintf("The array is degraded, %d disk%s %s failing", info.failed, s, verb)

			if info.activityState == "recovering" {
				description += ". " + generateFinishTimeStatusDescription(info.recoveryMinutes, timeNow)
			}
		}
	}

	if status == types.StatusUnset {
		switch info.activityState {
		case "active":
			status = types.StatusOk
		case "inactive":
			status = types.StatusCritical
			description = "The array is currently inactive"
		case "recovering":
			status = types.StatusWarning
			description = generateFinishTimeStatusDescription(info.recoveryMinutes, timeNow)
		case "checking":
			status = types.StatusOk
		case "resyncing":
			status = types.StatusWarning
			description = "The array is currently resyncing"
		default:
			status = types.StatusUnknown
			description = fmt.Sprintf("Unknown activity state %q on disk array %s", info.activityState, array)
		}
	}

	if status == types.StatusOk { // maybe not so ok
		if info.active == info.total && info.failed > 0 { // spare disks have been used
			status = types.StatusWarning
			missingSpares := (info.active + info.failed) - info.total
			s, verb := plural(missingSpares)
			description = fmt.Sprintf("%d spare disk%s %s failing on this array", missingSpares, s, verb)
		}
	}

	return &dto.Metric{
		Label: []*dto.LabelPair{
			{Name: proto.String(types.LabelItem), Value: proto.String(array)},
			{Name: proto.String(types.LabelMetaCurrentStatus), Value: proto.String(status.String())},
			{Name: proto.String(types.LabelMetaCurrentDescription), Value: proto.String(description)},
		},
		Untyped:     &dto.Untyped{Value: proto.Float64(float64(status.NagiosCode()))},
		TimestampMs: proto.Int64(info.timestamp),
	}
}

func generateFinishTimeStatusDescription(minutesLeft float64, timeNow func() time.Time) string {
	if minutesLeft <= 0 {
		return "The array should be fully synchronized in a few moments"
	}

	estimatedTime := timeNow().Add(time.Duration(minutesLeft) * time.Minute)

	return fmt.Sprintf(
		"The disk should be fully synchronized in %dmin (around %s)",
		int(math.Ceil(minutesLeft)),
		estimatedTime.Format(time.TimeOnly),
	)
}

func plural(n int) (s, verb string) {
	switch n {
	case 0:
		return "", "are"
	case 1:
		return "", "is"
	default:
		return "s", "are"
	}
}
