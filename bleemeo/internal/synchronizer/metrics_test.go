package synchronizer

import (
	"agentgo/types"
	"errors"
	"testing"
	"time"
)

type mockMetric struct {
	Name string
}

func (m mockMetric) Labels() map[string]string {
	return map[string]string{"__name__": m.Name}
}
func (m mockMetric) Points(start, end time.Time) ([]types.PointStatus, error) {
	return nil, errors.New("not implemented")
}

func TestPrioritizeMetrics(t *testing.T) {
	inputNames := []struct {
		Name         string
		HighPriority bool
	}{
		{"cpu_used", true},
		{"cassandra_status", false},
		{"io_utilization", true},
		{"nginx_requests", false},
		{"mem_used", true},
		{"mem_used_perc", true},
	}
	isHighPriority := make(map[string]bool)
	countHighPriority := 0
	metrics := make([]types.Metric, len(inputNames))
	for i, n := range inputNames {
		metrics[i] = mockMetric{Name: n.Name}
		if n.HighPriority {
			countHighPriority++
			isHighPriority[n.Name] = true
		}
	}
	prioritizeMetrics(metrics)
	for i, m := range metrics {
		if !isHighPriority[m.Labels()["__name__"]] && i < countHighPriority {
			t.Errorf("Found metrics %#v at index %d, want after %d", m.Labels()["__name__"], i, countHighPriority)
		}
		if isHighPriority[m.Labels()["__name__"]] && i >= countHighPriority {
			t.Errorf("Found metrics %#v at index %d, want before %d", m.Labels()["__name__"], i, countHighPriority)
		}
	}
}
