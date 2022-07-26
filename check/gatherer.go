package check

import (
	"context"
	"glouton/prometheus/model"
	"glouton/types"
	"time"

	dto "github.com/prometheus/client_model/go"
)

// CheckGatherer is the gatherer used for service checks.
type CheckGatherer struct {
	check          Check
	scheduleUpdate func(runAt time.Time)
}

// Check is an interface which specifies a check.
type Check interface {
	Check(ctx context.Context, scheduleUpdate func(runAt time.Time)) types.MetricPoint
}

// NewCheckGatherer returns a new check gatherer.
func NewCheckGatherer(check Check) *CheckGatherer {
	gatherer := CheckGatherer{
		check:          check,
		scheduleUpdate: func(time.Time) {},
	}

	return &gatherer
}

// Gather runs the check and returns the result as metric families.
func (cg *CheckGatherer) Gather() ([]*dto.MetricFamily, error) {
	point := cg.check.Check(context.TODO(), cg.scheduleUpdate) // TODO: context
	mfs := model.MetricPointsToFamilies([]types.MetricPoint{point})

	return mfs, nil
}

// SetScheduleUpdate implements GathererWithScheduleUpdate.
func (cg *CheckGatherer) SetScheduleUpdate(scheduleUpdate func(runAt time.Time)) {
	cg.scheduleUpdate = scheduleUpdate
}
