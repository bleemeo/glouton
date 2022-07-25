package check

import (
	"context"
	"glouton/prometheus/model"
	"glouton/types"

	dto "github.com/prometheus/client_model/go"
)

type CheckGatherer struct {
	check Check
}

// Check is an interface which specifies a check.
type Check interface {
	Check(ctx context.Context, callFromSchedule bool) types.MetricPoint
}

func NewCheckGatherer(check Check) CheckGatherer {
	return CheckGatherer{check: check}
}

func (cg CheckGatherer) Gather() ([]*dto.MetricFamily, error) {
	point := cg.check.Check(context.TODO(), true) // TODO: context
	mfs := model.MetricPointsToFamilies([]types.MetricPoint{point})

	return mfs, nil
}
