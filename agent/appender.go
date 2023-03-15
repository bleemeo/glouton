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

package agent

import (
	"context"
	"fmt"
	"glouton/discovery"
	crTypes "glouton/facts/container-runtime/types"
	"glouton/logger"
	"glouton/prometheus/model"
	"glouton/store"
	"glouton/types"
	"reflect"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/storage"
)

// miscAppender collects container metrics.
type miscAppender struct {
	containerRuntime crTypes.RuntimeInterface
}

func (ma miscAppender) Collect(ctx context.Context, app storage.Appender) error {
	now := time.Now()

	points, err := ma.containerRuntime.Metrics(ctx, now)
	if err != nil {
		logger.V(2).Printf("container Runtime metrics gather failed: %v", err)
	}

	// We don't really care about having up-to-date information because
	// when containers are started/stopped, the information is updated anyway.
	containers, err := ma.containerRuntime.Containers(ctx, 2*time.Hour, false)
	if err != nil {
		return fmt.Errorf("gather on DockerProvider failed: %w", err)
	}

	countRunning := 0

	for _, c := range containers {
		if c.State().IsRunning() {
			countRunning++
		}
	}

	points = append(points, types.MetricPoint{
		Point: types.Point{Time: now, Value: float64(countRunning)},
		Labels: map[string]string{
			types.LabelName: "containers_count",
		},
	})

	err = model.SendPointsToAppender(points, app)
	if err != nil {
		return fmt.Errorf("send points to appender: %w", err)
	}

	return app.Commit()
}

// miscAppenderMinute collects various metrics every minutes.
type miscAppenderMinute struct {
	containerRuntime  crTypes.RuntimeInterface
	discovery         *discovery.Discovery
	store             *store.Store
	hostRootPath      string
	getConfigWarnings func() prometheus.MultiError
}

func (ma miscAppenderMinute) Collect(ctx context.Context, app storage.Appender) error {
	now := time.Now()

	points, err := ma.containerRuntime.MetricsMinute(ctx, now)
	if err != nil {
		logger.V(2).Printf("container Runtime metrics gather failed: %v", err)
	}

	service, err := ma.discovery.Discovery(ctx, 2*time.Hour)
	if err != nil {
		logger.V(1).Printf("get service failed to every-minute metrics: %v", err)

		service = nil
	}

	for _, srv := range service {
		if !srv.Active {
			continue
		}

		switch srv.ServiceType { //nolint:exhaustive,nolintlint
		case discovery.PostfixService:
			n, err := postfixQueueSize(ctx, srv, ma.hostRootPath, ma.containerRuntime)
			if err != nil {
				logger.V(1).Printf("Unabled to gather postfix queue size on %s: %v", srv, err)

				continue
			}

			labels := map[string]string{
				types.LabelName: "postfix_queue_size",
				types.LabelItem: srv.Instance,
			}

			annotations := types.MetricAnnotations{
				BleemeoItem:     srv.Instance,
				ContainerID:     srv.ContainerID,
				ServiceName:     srv.Name,
				ServiceInstance: srv.Instance,
			}

			points = append(points, types.MetricPoint{
				Labels:      labels,
				Annotations: annotations,
				Point: types.Point{
					Time:  time.Now(),
					Value: n,
				},
			})
		case discovery.EximService:
			n, err := eximQueueSize(ctx, srv, ma.hostRootPath, ma.containerRuntime)
			if err != nil {
				logger.V(1).Printf("Unabled to gather exim queue size on %s: %v", srv, err)

				continue
			}

			labels := map[string]string{
				types.LabelName: "exim_queue_size",
				types.LabelItem: srv.Instance,
			}

			annotations := types.MetricAnnotations{
				BleemeoItem:     srv.Instance,
				ContainerID:     srv.ContainerID,
				ServiceName:     srv.Name,
				ServiceInstance: srv.Instance,
			}

			points = append(points, types.MetricPoint{
				Labels:      labels,
				Annotations: annotations,
				Point: types.Point{
					Time:  time.Now(),
					Value: n,
				},
			})
		}
	}

	desc := ma.getConfigWarnings().Error()
	status := types.StatusWarning

	if len(desc) == 0 {
		status = types.StatusOk
		desc = "configuration returned no warnings."
	}

	points = append(points, types.MetricPoint{
		Point: types.Point{
			Value: float64(status.NagiosCode()),
			Time:  now,
		},
		Labels: map[string]string{
			types.LabelName: "agent_config_warning",
		},
		Annotations: types.MetricAnnotations{
			Status: types.StatusDescription{
				StatusDescription: desc,
				CurrentStatus:     status,
			},
		},
	})

	// Add SMART status and UPSD battery status metrics.
	points = append(points, smartHealthStatusPoints(now, ma.store)...)
	points = append(
		points,
		statusFromLastPoint(now, ma.store, "upsd_status_flags", map[string]string{types.LabelName: "upsd_battery_status"}, upsdBatteryStatus)...,
	)

	err = model.SendPointsToAppender(points, app)
	if err != nil {
		return fmt.Errorf("send points to appender: %w", err)
	}

	return app.Commit()
}

// statusFromLastPoint returns points for the targetMetric based on the last point from baseMetricName.
// statusDescription must return the status description based on the last point and labels of baseMetricName.
// If statusDescription returns an unset status, the point is ignored.
func statusFromLastPoint(
	now time.Time,
	store *store.Store,
	baseMetricName string, targetMetric map[string]string,
	statusDescription func(value float64, labels map[string]string) types.StatusDescription,
) []types.MetricPoint {
	metrics, _ := store.Metrics(map[string]string{types.LabelName: baseMetricName})
	if len(metrics) == 0 {
		return nil
	}

	newPoints := make([]types.MetricPoint, 0, len(metrics))

	for _, metric := range metrics {
		// Get the last point from this metric.
		points, _ := metric.Points(now.Add(-2*time.Minute), now)
		if len(points) == 0 {
			continue
		}

		sort.Slice(
			points,
			func(i, j int) bool {
				return points[i].Time.Before(points[j].Time)
			},
		)

		lastPoint := points[len(points)-1]

		// Keep annotations from the base metric, only change the status.
		annotations := metric.Annotations()
		annotations.Status = statusDescription(lastPoint.Value, metric.Labels())

		if annotations.Status.CurrentStatus == types.StatusUnset {
			// There is no status, ignore this point.
			continue
		}

		// Keep all labels from the metric except its name.
		labelsCopy := make(map[string]string, len(metric.Labels()))

		for name, value := range metric.Labels() {
			if name == types.LabelName {
				continue
			}

			labelsCopy[name] = value
		}

		for k, v := range targetMetric {
			labelsCopy[k] = v
		}

		newPoints = append(newPoints, types.MetricPoint{
			Point: types.Point{
				Value: float64(annotations.Status.CurrentStatus.NagiosCode()),
				Time:  now,
			},
			Labels:      labelsCopy,
			Annotations: annotations,
		})
	}

	return newPoints
}

// smartHealthStatusPoints returns the "smart_device_health_status" metric points.
// The health status metric is generated from two metrics:
// - if "smart_device_health_ok" is present for a device we return a status based on this metric
// - else if "smart_device_health_ok" is missing and "smart_device_exit_status" is not 0, it means the check
// command failed and we return an unknown status.
func smartHealthStatusPoints(now time.Time, store *store.Store) []types.MetricPoint {
	smartHealthStatusLabels := map[string]string{types.LabelName: "smart_device_health_status"}

	healthPoints := statusFromLastPoint(now, store, "smart_device_health_ok", smartHealthStatusLabels, smartHealthStatus)
	exitStatusPoints := statusFromLastPoint(now, store, "smart_device_exit_status", smartHealthStatusLabels, smartExitStatus)

	// Add the exit status points to the health points if the labels are not already present.
	// This is in case a check has an exit code != 0 but still generates a "device_health_ok" metric.
	// In this case we just keep the point generated from "device_health_ok" and ignore the exit code.
	for _, exitPoint := range exitStatusPoints {
		isDuplicated := false

		for _, healthPoint := range healthPoints {
			if reflect.DeepEqual(exitPoint.Labels, healthPoint.Labels) {
				isDuplicated = true

				break
			}
		}

		if !isDuplicated {
			healthPoints = append(healthPoints, exitPoint)
		}
	}

	return healthPoints
}

// smartHealthStatus returns the "smart_device_health_status" metric description from the last value
// of the metric "smart_device_health_ok" and its labels.
func smartHealthStatus(value float64, labels map[string]string) types.StatusDescription {
	var status types.StatusDescription

	// The device_health_ok field from the SMART input is a boolean we converted to an integer.
	if value == 1 {
		status = types.StatusDescription{
			CurrentStatus: types.StatusOk,
			StatusDescription: fmt.Sprintf(
				"SMART tests passed on %s (%s)",
				labels[types.LabelDevice],
				labels[types.LabelModel],
			),
		}
	} else {
		status = types.StatusDescription{
			CurrentStatus: types.StatusCritical,
			StatusDescription: fmt.Sprintf(
				"SMART tests failed on %s (%s)",
				labels[types.LabelDevice],
				labels[types.LabelModel],
			),
		}
	}

	return status
}

// smartExitStatus returns the "smart_device_health_status" metric description from the last value
// of the metric "smart_device_exit_status" and its labels.
func smartExitStatus(value float64, labels map[string]string) types.StatusDescription {
	var status types.StatusDescription

	// Exit status 0 means the command executed successfully.
	// We only return a status unknown when the command failed.
	if value != 0 {
		status = types.StatusDescription{
			CurrentStatus: types.StatusUnknown,
			StatusDescription: fmt.Sprintf(
				"smartctl check failed with exit code %d on %s (%s)",
				int(value),
				labels[types.LabelDevice],
				labels[types.LabelModel],
			),
		}
	}

	return status
}

// upsdBatteryStatus returns the "upsd_battery_status" metric description from the last value
// of the metric "upsd_status_flags" and its labels.
// It reports a critical status when:
// - the UPS is overloaded
// - the UPS is on battery
// - the battery is low
// - the battery needs to be replaced.
func upsdBatteryStatus(value float64, _ map[string]string) types.StatusDescription {
	var status types.StatusDescription

	// The value is a composed of status bits documented in apcupsd:
	// http://www.apcupsd.org/manual/#status-bits
	// 0 	Runtime calibration occurring (Not reported by Smart UPS v/s and BackUPS Pro)
	// 1 	SmartTrim (Not reported by 1st and 2nd generation SmartUPS models)
	// 2 	SmartBoost
	// 3 	On line (this is the normal condition)
	// 4 	On battery
	// 5 	Overloaded output
	// 6 	Battery low
	// 7 	Replace battery
	statusFlags := int(value)
	onLine := statusFlags&(1<<3) > 0
	overloadedOutput := statusFlags&(1<<5) > 0
	lowBattery := statusFlags&(1<<6) > 0
	replaceBattery := statusFlags&(1<<7) > 0

	switch {
	case replaceBattery:
		status = types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: "Battery should be replaced",
		}
	case lowBattery:
		status = types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: "Battery is low",
		}
	case overloadedOutput:
		status = types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: "UPS is overloaded",
		}
	case !onLine:
		status = types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: "UPS is running on battery",
		}
	default:
		status = types.StatusDescription{
			CurrentStatus:     types.StatusOk,
			StatusDescription: "On line, battery ok",
		}
	}

	return status
}
