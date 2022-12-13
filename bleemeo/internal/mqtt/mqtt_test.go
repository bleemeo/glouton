package mqtt

import (
	"fmt"
	"glouton/types"
	"testing"
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
