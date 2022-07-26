package check

import (
	"context"
	"glouton/logger"
	"glouton/prometheus/model"
	"glouton/prometheus/registry"
	"glouton/types"
	"time"

	dto "github.com/prometheus/client_model/go"
)

const gatherTimeout = 10 * time.Second

// Gatherer is the gatherer used for service checks.
type Gatherer struct {
	check Check
	// The metrics produced by the check are kept to be returned when
	// the gatherer is called from /metrics.
	lastMetricFamilies []*dto.MetricFamily
	scheduleUpdate     func(runAt time.Time)
}

// Check is an interface which specifies a check.
type Check interface {
	Check(ctx context.Context, scheduleUpdate func(runAt time.Time)) types.MetricPoint
}

// NewCheckGatherer returns a new check gatherer.
func NewCheckGatherer(check Check) *Gatherer {
	return &Gatherer{check: check}
}

// GatherWithState implements GathererWithState.
func (cg *Gatherer) GatherWithState(ctx context.Context, state registry.GatherState) ([]*dto.MetricFamily, error) {
	// Return the metrics from the last check on /metrics.
	if !state.FromScrapeLoop {
		return cg.lastMetricFamilies, nil
	}

	point := cg.check.Check(ctx, cg.scheduleUpdate)
	mfs := model.MetricPointsToFamilies([]types.MetricPoint{point})
	cg.lastMetricFamilies = mfs

	return mfs, nil
}

// Gather runs the check and returns the result as metric families.
func (cg *Gatherer) Gather() ([]*dto.MetricFamily, error) {
	logger.V(2).Println("Gather() called directly on a check gatherer, this is a bug!")

	ctx, cancel := context.WithTimeout(context.Background(), gatherTimeout)
	defer cancel()

	return cg.GatherWithState(ctx, registry.GatherState{})
}

// SetScheduleUpdate implements GathererWithScheduleUpdate.
func (cg *Gatherer) SetScheduleUpdate(scheduleUpdate func(runAt time.Time)) {
	cg.scheduleUpdate = scheduleUpdate
}

// CheckNow runs the check and returns its status.
func (cg *Gatherer) CheckNow(ctx context.Context) types.StatusDescription {
	point := cg.check.Check(ctx, cg.scheduleUpdate)

	return point.Annotations.Status
}
