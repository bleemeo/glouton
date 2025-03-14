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

package mqtt

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/agent/state"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/types"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
)

func TestFailedPointsCache(t *testing.T) {
	t.Parallel()

	const (
		maxPendingPoints = 10
		cleanupBatchSize = 1
		labelID          = "id"
	)

	failedPoints := failedPointsCache{
		maxPendingPoints:    maxPendingPoints,
		cleanupBatchSize:    cleanupBatchSize,
		cleanupFailedPoints: func(failedPoints []types.MetricPoint) []types.MetricPoint { return failedPoints },
		metricExists:        make(map[string]struct{}),
	}

	// Add 10 metric with labels id=1, id=2, ...
	for i := range maxPendingPoints {
		labels := map[string]string{labelID: strconv.Itoa(i)}
		p := types.MetricPoint{
			Labels: labels,
		}

		failedPoints.Add(p)
	}

	// Test that all 10 metrics are present.
	for i := range maxPendingPoints {
		labels := map[string]string{labelID: strconv.Itoa(i)}
		if !failedPoints.Contains(labels) {
			t.Fatalf("Point %d is not present", i)
		}
	}

	// Add 5 more points.
	for i := maxPendingPoints; i < maxPendingPoints+5; i++ {
		labels := map[string]string{labelID: strconv.Itoa(i)}
		p := types.MetricPoint{
			Labels: labels,
		}

		failedPoints.Add(p)
	}

	// The first 6 points should be deleted (it would've been 5 without cleanupBatchSize at 1).
	if failedPoints.Len() != 9 {
		t.Fatal("Failed points should contain 9 points")
	}

	for i := range 6 {
		labels := map[string]string{labelID: strconv.Itoa(i)}

		if failedPoints.Contains(labels) {
			t.Fatalf("Point %d should be deleted", i)
		}
	}

	// Pop should empty the points.
	failedPoints.Pop()

	if failedPoints.Len() != 0 {
		t.Fatal("Failed points should be empty")
	}
}

type mockMQTTClient struct {
	publishedPoints []metricPayload
}

func (m *mockMQTTClient) Publish(topic string, payload any, retry bool) error {
	_ = topic
	_ = retry

	metrics, ok := payload.([]metricPayload)

	if !ok {
		return fmt.Errorf("%w: Payload is not a list of metrics", errors.ErrUnsupported)
	}

	m.publishedPoints = append(m.publishedPoints, metrics...)

	return nil
}

func (*mockMQTTClient) Run(context.Context)    {}
func (*mockMQTTClient) IsConnectionOpen() bool { return true }
func (*mockMQTTClient) DiagnosticArchive(context.Context, types.ArchiveWriter) error {
	return nil
}
func (*mockMQTTClient) LastReport() time.Time    { return time.Now() }
func (*mockMQTTClient) Disable(time.Time)        {}
func (*mockMQTTClient) DisabledUntil() time.Time { return time.Time{} }
func (*mockMQTTClient) Disconnect(time.Duration) {}

const agentID = "test-agent"

// mockClient returns a MQTT client with an allowlist that allows all metrics.
func mockClient(t *testing.T) *Client {
	t.Helper()

	const (
		agentConfigID   = "test-agent-config"
		accountConfigID = "test-account-config"
		agentTypeID     = "test-agent-type"
	)

	state, err := state.LoadReadOnly("", "")
	if err != nil {
		t.Fatalf("Failed to load state: %s", err)
	}

	cache := cache.Load(state)

	// Set agent config with the metric allowlist.
	cache.SetAccountConfigs([]bleemeoTypes.AccountConfig{
		{
			ID:   accountConfigID,
			Name: "agent",
		},
	})

	cache.SetAgentList([]bleemeoTypes.Agent{
		{
			ID:                     agentID,
			CurrentAccountConfigID: accountConfigID,
			AgentType:              agentTypeID,
		},
	})

	cache.SetAgentTypes([]bleemeoTypes.AgentType{
		{
			ID:   agentTypeID,
			Name: bleemeo.AgentType_Agent,
		},
	})

	cache.SetAgentConfigs([]bleemeoTypes.AgentConfig{
		{
			ID: agentConfigID,
			// Empty allowlist to allow all metrics.
			MetricsAllowlist: "",
			AccountConfig:    accountConfigID,
			AgentType:        agentTypeID,
		},
	})

	c := &Client{
		opts: Option{
			AgentID:              bleemeoTypes.AgentID(agentID),
			Cache:                cache,
			LastMetricActivation: func() time.Time { return time.Time{} },
		},
		dataStreamAvailable: true,
	}

	c.failedPoints = failedPointsCache{
		metricExists:        make(map[string]struct{}),
		maxPendingPoints:    maxPendingPoints,
		cleanupFailedPoints: c.cleanupFailedPoints,
	}

	c.mqtt = &mockMQTTClient{}

	return c
}

// TestMQTTPointOrder verifies that point are sent in the right order when an
// inactive metric becomes active.
func TestMQTTPointOrder(t *testing.T) {
	t.Parallel()

	// - metric1 is inactive at t0 and t1, active after t2
	// - metric2 is always active
	const (
		metric1ID = "test-metric-1"
		metric2ID = "test-metric-2"
	)

	metric1Labels := map[string]string{
		types.LabelName:         "container_cpu_used",
		types.LabelItem:         "inactive-then-active",
		types.LabelInstanceUUID: agentID,
	}

	metric2Labels := map[string]string{
		types.LabelName:         "container_cpu_used",
		types.LabelItem:         "always-active",
		types.LabelInstanceUUID: agentID,
	}

	client := mockClient(t)
	t0 := time.Date(2022, time.December, 14, 11, 0, 0, 0, time.UTC)
	t1 := time.Date(2022, time.December, 14, 11, 0, 10, 0, time.UTC)
	t2 := time.Date(2022, time.December, 14, 11, 0, 20, 0, time.UTC)
	t3 := time.Date(2022, time.December, 14, 11, 0, 30, 0, time.UTC)

	// Set metric1 inactive and metric2 active.
	client.opts.Cache.SetMetrics([]bleemeoTypes.Metric{
		{
			ID:            metric1ID,
			Labels:        metric1Labels,
			LabelsText:    types.LabelsToText(metric1Labels),
			AgentID:       agentID,
			DeactivatedAt: t0,
		},
		{
			ID:            metric2ID,
			Labels:        metric2Labels,
			LabelsText:    types.LabelsToText(metric2Labels),
			AgentID:       agentID,
			DeactivatedAt: time.Time{},
		},
	})

	// Send point at t0.
	// metric1 should be added to the failed points because the metric is inactive.
	// metric2 should be published.
	points := []types.MetricPoint{
		{
			Point: types.Point{
				Time: t0,
			},
			Labels: metric1Labels,
			Annotations: types.MetricAnnotations{
				BleemeoAgentID: agentID,
			},
		},
		{
			Point: types.Point{
				Time: t0,
			},
			Labels: metric2Labels,
			Annotations: types.MetricAnnotations{
				BleemeoAgentID: agentID,
			},
		},
	}

	client.addPoints(points)
	client.sendPoints()

	// Send a point at t1.
	// metric1 should be added to the failed points because the metric is inactive.
	// metric2 should be published.
	points = []types.MetricPoint{
		{
			Point: types.Point{
				Time: t1,
			},
			Labels: metric1Labels,
			Annotations: types.MetricAnnotations{
				BleemeoAgentID: agentID,
			},
		},
		{
			Point: types.Point{
				Time: t1,
			},
			Labels: metric2Labels,
			Annotations: types.MetricAnnotations{
				BleemeoAgentID: agentID,
			},
		},
	}

	client.addPoints(points)
	client.sendPoints()

	// Set metric1 active.
	client.opts.Cache.SetMetrics([]bleemeoTypes.Metric{
		{
			ID:            metric1ID,
			Labels:        metric1Labels,
			LabelsText:    types.LabelsToText(metric1Labels),
			AgentID:       agentID,
			DeactivatedAt: time.Time{},
		},
		{
			ID:            metric2ID,
			Labels:        metric2Labels,
			LabelsText:    types.LabelsToText(metric2Labels),
			AgentID:       agentID,
			DeactivatedAt: time.Time{},
		},
	})

	// Send a point at t2.
	// metric1 should be added to the failed points because the previous points failed.
	// metric2 should be published.
	points = []types.MetricPoint{
		{
			Point: types.Point{
				Time: t2,
			},
			Labels: metric1Labels,
			Annotations: types.MetricAnnotations{
				BleemeoAgentID: agentID,
			},
		},
		{
			Point: types.Point{
				Time: t2,
			},
			Labels: metric2Labels,
			Annotations: types.MetricAnnotations{
				BleemeoAgentID: agentID,
			},
		},
	}

	client.addPoints(points)
	client.sendPoints()

	// Verify that the points from metric2 were published (one point at t0, t1 and t2).
	mqtt, _ := client.mqtt.(*mockMQTTClient)
	if len(mqtt.publishedPoints) != 3 {
		t.Fatalf("Expected 3 points published, got %d", len(mqtt.publishedPoints))
	}

	// Force client to retry failed points.
	client.lastFailedPointsRetry = time.Time{}

	// Send a point at t3.
	// metric1 should be published since the failed points should be empty.
	// metric2 should be published.
	points = []types.MetricPoint{
		{
			Point: types.Point{
				Time: t3,
			},
			Labels: metric1Labels,
			Annotations: types.MetricAnnotations{
				BleemeoAgentID: agentID,
			},
		},
		{
			Point: types.Point{
				Time: t3,
			},
			Labels: metric2Labels,
			Annotations: types.MetricAnnotations{
				BleemeoAgentID: agentID,
			},
		},
	}

	client.addPoints(points)
	client.sendPoints()

	if len(mqtt.publishedPoints) != 8 {
		t.Fatalf("Expected 8 points published, got %d", len(mqtt.publishedPoints))
	}

	// Verify that the points are ordered correctly.
	lastTimestampByID := make(map[string]int64)
	for _, metric := range mqtt.publishedPoints {
		if metric.TimestampMS < lastTimestampByID[metric.UUID] {
			t.Fatalf(
				"Published points are not ordered, point with timestamp %s received before timestamp %s",
				time.UnixMilli(lastTimestampByID[metric.UUID]), time.UnixMilli(metric.TimestampMS),
			)
		}

		lastTimestampByID[metric.UUID] = metric.TimestampMS
	}
}

func BenchmarkFailedPointsDropping(b *testing.B) {
	const labelID = "id"

	failedPoints := failedPointsCache{
		maxPendingPoints:    maxPendingPoints,
		cleanupBatchSize:    cleanupBatchSize,
		cleanupFailedPoints: func(failedPoints []types.MetricPoint) []types.MetricPoint { return failedPoints },
		metricExists:        make(map[string]struct{}),
	}

	// Add 10 metrics with labels id=1, id=2, ... with 10000 points each
	for i := 1; i <= 10; i++ {
		labels := map[string]string{labelID: strconv.Itoa(i)}

		for range maxPendingPoints / 10 {
			p := types.MetricPoint{
				Labels: labels,
			}

			failedPoints.Add(p)
		}
	}

	b.ResetTimer()

	for b.Loop() {
		// Add 100 more points for each metric
		for i := 1; i <= 10; i++ {
			labels := map[string]string{labelID: strconv.Itoa(i)}

			for range cleanupBatchSize / 10 {
				p := types.MetricPoint{
					Labels: labels,
				}

				failedPoints.Add(p)
			}
		}
	}
}
