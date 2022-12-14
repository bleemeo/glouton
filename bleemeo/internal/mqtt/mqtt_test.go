package mqtt

import (
	"context"
	"fmt"
	"glouton/agent/state"
	"glouton/bleemeo/internal/cache"
	"glouton/mqtt"
	"glouton/types"
	"log"
	"testing"
	"time"

	bleemeoTypes "glouton/bleemeo/types"
)

func TestFailedPointsCache(t *testing.T) {
	t.Parallel()

	const (
		maxPendingPoints = 10
		labelID          = "id"
	)

	failedPoints := failedPointsCache{
		maxPendingPoints:    maxPendingPoints,
		cleanupFailedPoints: func(failedPoints []types.MetricPoint) []types.MetricPoint { return failedPoints },
		metricExists:        make(map[string]struct{}),
	}

	// Add 10 metric with labels id=1, id=2, ...
	for i := 0; i < maxPendingPoints; i++ {
		labels := map[string]string{labelID: fmt.Sprint(i)}
		p := types.MetricPoint{
			Labels: labels,
		}

		failedPoints.Add(p)
	}

	// Test that all 10 metrics are present.
	for i := 0; i < maxPendingPoints; i++ {
		labels := map[string]string{labelID: fmt.Sprint(i)}
		if !failedPoints.Contains(labels) {
			t.Fatalf("Point %d is not present", i)
		}
	}

	// Add 5 more points.
	for i := maxPendingPoints; i < maxPendingPoints+5; i++ {
		labels := map[string]string{labelID: fmt.Sprint(i)}
		p := types.MetricPoint{
			Labels: labels,
		}

		failedPoints.Add(p)
	}

	// The first 5 points should be deleted.
	if failedPoints.Len() != 10 {
		t.Fatal("Failed points should contain 10 points")
	}

	for i := 0; i < 5; i++ {
		labels := map[string]string{labelID: fmt.Sprint(i)}

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
	encoder         mqtt.Encoder
	publishedPoints []metricPayload
}

func (m *mockMQTTClient) Publish(topic string, payload interface{}, retry bool) {
	var metrics []metricPayload

	payloadBytes, _ := payload.([]byte)

	err := m.encoder.Decode(payloadBytes, &metrics)
	if err != nil {
		log.Printf("Failed to decode payload: %s\n", err)
	}

	m.publishedPoints = append(m.publishedPoints, metrics...)
}

func (*mockMQTTClient) Run(ctx context.Context) {}
func (*mockMQTTClient) IsConnectionOpen() bool  { return true }
func (*mockMQTTClient) DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	return nil
}
func (*mockMQTTClient) LastReport() time.Time            { return time.Now() }
func (*mockMQTTClient) Disable(until time.Time)          {}
func (*mockMQTTClient) DisabledUntil() time.Time         { return time.Time{} }
func (*mockMQTTClient) Disconnect(timeout time.Duration) {}

const agentID = "test-agent"

// mockClient returns a MQTT client with an allowlist that allows all metrics.
func mockClient(t *testing.T) *Client {
	t.Helper()

	const (
		agentConfigID   = "test-agent-config"
		accountConfigID = "test-account-config"
		agentTypeID     = "test-agent-type"
	)

	state, err := state.Load("", "")
	if err != nil {
		t.Fatalf("Failed to load state: %s", err)
	}

	cache := cache.Load(state)

	// Set agent config with the metric allowlist.
	cache.SetAccountConfigs([]bleemeoTypes.AccountConfig{
		{
			Name: bleemeoTypes.AgentTypeAgent,
			ID:   accountConfigID,
		},
	})

	cache.SetAgentList([]bleemeoTypes.Agent{
		{
			ID:              agentID,
			CurrentConfigID: accountConfigID,
			AgentType:       agentTypeID,
		},
	})

	cache.SetAgentTypes([]bleemeoTypes.AgentType{
		{
			ID:   agentTypeID,
			Name: bleemeoTypes.AgentTypeAgent,
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
			AgentID: bleemeoTypes.AgentID(agentID),
			Cache:   cache,
		},
	}

	c.failedPoints = failedPointsCache{
		metricExists:        make(map[string]struct{}),
		maxPendingPoints:    maxPendingPoints,
		cleanupFailedPoints: c.cleanupFailedPoints,
	}

	c.mqtt = &mockMQTTClient{}

	return c
}

func TestMQTT(t *testing.T) {
	t.Parallel()

	const metricID = "test-metric"

	metricLabels := map[string]string{
		types.LabelName:         "cpu_used",
		types.LabelInstanceUUID: agentID,
	}

	client := mockClient(t)
	t0 := time.Date(2022, time.December, 14, 11, 0, 0, 0, time.UTC)
	t1 := time.Date(2022, time.December, 14, 11, 0, 10, 0, time.UTC)
	t2 := time.Date(2022, time.December, 14, 11, 0, 20, 0, time.UTC)
	t3 := time.Date(2022, time.December, 14, 11, 0, 30, 0, time.UTC)

	// Set metric inactive.
	client.opts.Cache.SetMetrics([]bleemeoTypes.Metric{
		{
			ID:            metricID,
			Labels:        metricLabels,
			LabelsText:    types.LabelsToText(metricLabels),
			AgentID:       agentID,
			DeactivatedAt: t0,
		},
	})

	// Send a point at t0.
	// It should be added to the failed points because the metric is inactive.
	points := []types.MetricPoint{
		{
			Point: types.Point{
				Time: t0,
			},
			Labels: metricLabels,
			Annotations: types.MetricAnnotations{
				BleemeoAgentID: agentID,
			},
		},
	}

	client.addPoints(points)
	client.sendPoints()

	// Send a point at t1.
	// It should be added to the failed points because the metric is inactive.
	points = []types.MetricPoint{
		{
			Point: types.Point{
				Time: t1,
			},
			Labels: metricLabels,
			Annotations: types.MetricAnnotations{
				BleemeoAgentID: agentID,
			},
		},
	}

	client.addPoints(points)
	client.sendPoints()

	// Set metric active.
	client.opts.Cache.SetMetrics([]bleemeoTypes.Metric{
		{
			ID:            metricID,
			Labels:        metricLabels,
			LabelsText:    types.LabelsToText(metricLabels),
			AgentID:       agentID,
			DeactivatedAt: time.Time{},
		},
	})

	// Send a point at t2.
	// It should be added to the failed points because the previous points failed.
	points = []types.MetricPoint{
		{
			Point: types.Point{
				Time: t2,
			},
			Labels: metricLabels,
			Annotations: types.MetricAnnotations{
				BleemeoAgentID: agentID,
			},
		},
	}

	client.addPoints(points)
	client.sendPoints()

	// Verify that no points were published.
	mqtt, _ := client.mqtt.(*mockMQTTClient)
	if len(mqtt.publishedPoints) > 0 {
		t.Fatal("A point was sent too soon")
	}

	// Force client to retry failed points.
	client.lastFailedPointsRetry = time.Time{}

	// Send a point at t3.
	// It should not be added to the failed points since the failed points should be empty.
	points = []types.MetricPoint{
		{
			Point: types.Point{
				Time: t3,
			},
			Labels: metricLabels,
			Annotations: types.MetricAnnotations{
				BleemeoAgentID: agentID,
			},
		},
	}

	client.addPoints(points)
	client.sendPoints()

	if len(mqtt.publishedPoints) != 4 {
		t.Fatalf("Expected 4 points published, got %d", len(mqtt.publishedPoints))
	}

	// Verify that the points are ordered correctly.
	lastTimestamp := int64(0)
	for _, metric := range mqtt.publishedPoints {
		if metric.TimestampMS < lastTimestamp {
			t.Fatalf(
				"Published points are not ordered, point with timestamp %s received before timestamp %s",
				time.UnixMilli(lastTimestamp), time.UnixMilli(metric.TimestampMS),
			)
		}

		lastTimestamp = metric.TimestampMS
	}
}
