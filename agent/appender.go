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

package agent

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"time"

	"github.com/bleemeo/glouton/discovery"
	crTypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/store"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/storage"
)

// miscAppender collects container metrics.
type miscAppender struct {
	containerRuntime crTypes.RuntimeInterface
}

func (ma miscAppender) CollectWithState(ctx context.Context, state registry.GatherState, app storage.Appender) error {
	points, err := ma.containerRuntime.Metrics(ctx, state.T0)
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
		Point: types.Point{Time: state.T0, Value: float64(countRunning)},
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
	runner            *gloutonexec.Runner
	getConfigWarnings func() prometheus.MultiError
}

func (ma miscAppenderMinute) CollectWithState(ctx context.Context, state registry.GatherState, app storage.Appender) error {
	points, err := ma.containerRuntime.MetricsMinute(ctx, state.T0)
	if err != nil {
		logger.V(2).Printf("container Runtime metrics gather failed: %v", err)
	}

	service, _ := ma.discovery.GetLatestDiscovery()

	for _, srv := range service {
		if !srv.Active {
			continue
		}

		switch srv.ServiceType { //nolint:exhaustive,nolintlint
		case discovery.PostfixService:
			n, err := postfixQueueSize(ctx, srv, ma.runner, ma.containerRuntime)
			if err != nil {
				logger.V(1).Printf("Unabled to gather postfix queue size on %s: %v", srv, err)

				continue
			}

			labels := map[string]string{
				types.LabelName: "postfix_queue_size",
				types.LabelItem: srv.Instance,
			}

			annotations := types.MetricAnnotations{
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
			n, err := eximQueueSize(ctx, srv, ma.runner, ma.containerRuntime)
			if err != nil {
				logger.V(1).Printf("Unabled to gather exim queue size on %s: %v", srv, err)

				continue
			}

			labels := map[string]string{
				types.LabelName: "exim_queue_size",
				types.LabelItem: srv.Instance,
			}

			annotations := types.MetricAnnotations{
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
			Time:  state.T0,
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
	points = append(
		points,
		statusFromLastPoint(state.T0, ma.store, "smart_device_health_ok", map[string]string{types.LabelName: "smart_device_health_status"}, smartHealthStatus)...,
	)
	points = append(
		points,
		statusFromLastPoint(state.T0, ma.store, "upsd_status_flags", map[string]string{types.LabelName: "upsd_battery_status"}, upsdBatteryStatus)...,
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

		maps.Copy(labelsCopy, targetMetric)

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
