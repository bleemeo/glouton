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

package windows

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/bleemeo/glouton/types"

	dto "github.com/prometheus/client_model/go"
)

var errUnparsableStatus = errors.New("can't parse status")

// status must be ordered from least important to more sever one.
// Because windows_exporter return one metric per-status, we have to handle case
// were multiple status are set at the same time. In that case, we took the most sever one.
const (
	statusOk       int = iota
	statusStressed     // I'm assuming this is an operational status
	statusDegraded
	statusPredFail
	statusStarting
	statusStopping
	statusService
	statusUnknown
	statusLostComm
	statusNoContact
	statusNonrecover
	statusError
)

func getSmartStatus(mfs []*dto.MetricFamily) []types.MetricPoint {
	driveModel := make(map[string]string)
	driveWorstStatus := make(map[string]int)
	driveTimestamp := make(map[string]time.Time)

	for _, mf := range mfs {
		if mf.GetName() == "windows_diskdrive_info" {
			for _, metric := range mf.GetMetric() {
				name, model := getNameModel(metric.GetLabel())

				if name != "" && model != "" {
					driveModel[name] = model
				}
			}
		}

		if mf.GetName() == "windows_diskdrive_status" {
			for _, metric := range mf.GetMetric() {
				if metric.GetGauge().GetValue() != 1 {
					continue
				}

				name, status := getNameStatus(metric.GetLabel())

				if name != "" && status != "" {
					statusInt, err := statusStringToInt(status)
					if err == nil {
						current, ok := driveWorstStatus[name]

						if !ok || statusInt > current {
							driveWorstStatus[name] = statusInt

							if metric.GetTimestampMs() != 0 {
								// We overwrite timestamp without priority in case of conflict, but:
								// 1) we don't expect any conflict (we expect a single metric with value 1)
								// 2) we don't expect timestamp to be different
								driveTimestamp[name] = time.UnixMilli(metric.GetTimestampMs())
							}
						}
					}
				}
			}
		}
	}

	return buildSmartMetric(driveModel, driveWorstStatus, driveTimestamp)
}

func buildSmartMetric(driveModel map[string]string, driveWorstStatus map[string]int, driveTimestamp map[string]time.Time) []types.MetricPoint {
	result := make([]types.MetricPoint, 0, len(driveWorstStatus))

	diskName := slices.Collect(maps.Keys(driveWorstStatus))
	slices.Sort(diskName)

	for _, name := range diskName {
		statusInt := driveWorstStatus[name]

		lbls := map[string]string{
			types.LabelName:   "smart_device_health_status",
			types.LabelDevice: name,
		}

		var status types.StatusDescription

		if driveModel[name] != "" {
			lbls[types.LabelModel] = driveModel[name]
		}

		switch statusInt {
		case statusOk:
			status.CurrentStatus = types.StatusOk
			status.StatusDescription = "SMART tests passed"
		case statusStressed:
			// We assume "stressed" means high IO, which is covered by io_utilization
			status.CurrentStatus = types.StatusOk
			status.StatusDescription = "SMART tests passed"
		case statusDegraded:
			status.CurrentStatus = types.StatusWarning
			status.StatusDescription = "Disk is degraded"
		case statusPredFail:
			status.CurrentStatus = types.StatusWarning
			status.StatusDescription = "SMART report predicted failure in the (near) future"
		case statusStarting:
			status.CurrentStatus = types.StatusCritical
			status.StatusDescription = "Disk is starting"
		case statusStopping:
			status.CurrentStatus = types.StatusCritical
			status.StatusDescription = "Disk is stopping"
		case statusService:
			status.CurrentStatus = types.StatusCritical
			status.StatusDescription = "Disk is offline for servicing"
		case statusUnknown:
			status.CurrentStatus = types.StatusCritical
			status.StatusDescription = "Disk is offline for unknown reason"
		case statusLostComm:
			status.CurrentStatus = types.StatusCritical
			status.StatusDescription = "Disk is offline due to lost communication with it"
		case statusNonrecover:
			status.CurrentStatus = types.StatusCritical
			status.StatusDescription = "Disk is offline due to non-recoverable error"
		case statusNoContact:
			status.CurrentStatus = types.StatusCritical
			status.StatusDescription = "Disk is offline due to unable to contact it"
		case statusError:
			status.CurrentStatus = types.StatusCritical
			status.StatusDescription = "Disk had error"
		}

		if status.CurrentStatus != types.StatusUnset {
			result = append(result, types.MetricPoint{
				Point: types.Point{
					Time:  driveTimestamp[name],
					Value: float64(status.CurrentStatus.NagiosCode()),
				},
				Labels: lbls,
				Annotations: types.MetricAnnotations{
					Status: status,
				},
			})
		}
	}

	return result
}

func getNameStatus(lbls []*dto.LabelPair) (string, string) {
	var (
		name   string
		status string
	)

	for _, pair := range lbls {
		if pair.GetName() == "name" {
			name = pair.GetValue()
		}

		if pair.GetName() == "status" {
			status = pair.GetValue()
		}
	}

	return name, status
}

func getNameModel(lbls []*dto.LabelPair) (string, string) {
	var (
		name  string
		model string
	)

	for _, pair := range lbls {
		if pair.GetName() == "name" {
			tmp, ok := strings.CutPrefix(pair.GetValue(), "\\\\.\\")
			if ok {
				name = tmp
			}
		}

		if pair.GetName() == "model" {
			model = pair.GetValue()
		}
	}

	return name, model
}

func statusStringToInt(statusStr string) (int, error) {
	switch statusStr {
	case "Degraded":
		return statusDegraded, nil
	case "Error":
		return statusError, nil
	case "Lost Comm":
		return statusLostComm, nil
	case "No Contact":
		return statusNoContact, nil
	case "Nonrecover":
		return statusNonrecover, nil
	case "OK":
		return statusOk, nil
	case "Pred fail":
		return statusPredFail, nil
	case "Service":
		return statusService, nil
	case "Starting":
		return statusStarting, nil
	case "Stopping":
		return statusStopping, nil
	case "Stressed":
		return statusStressed, nil
	case "Unknown":
		return statusUnknown, nil
	default:
		return 0, errUnparsableStatus
	}
}
