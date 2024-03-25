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

package check

import (
	"context"
	"glouton/logger"
	"glouton/prometheus/model"
	"glouton/prometheus/registry"
	"glouton/types"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
)

const defaultGatherTimeout = 10 * time.Second

// Gatherer is the gatherer used for service checks.
type Gatherer struct {
	check          checker
	scheduleUpdate func(runAt time.Time)

	l sync.Mutex
	// The last metric point produced by the check is kept to be
	// returned when the gatherer is called from /metrics.
	lastMetricPoint types.MetricPoint
}

// checker is an interface which specifies a check.
type checker interface {
	Check(ctx context.Context, scheduleUpdate func(runAt time.Time)) types.MetricPoint
	DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error
	Close()
}

// NewCheckGatherer returns a new check gatherer.
func NewCheckGatherer(check checker) *Gatherer {
	return &Gatherer{check: check}
}

// GatherWithState implements GathererWithState.
func (cg *Gatherer) GatherWithState(ctx context.Context, state registry.GatherState) ([]*dto.MetricFamily, error) {
	cg.l.Lock()
	lastMetricPoint := cg.lastMetricPoint
	cg.l.Unlock()

	// Return the metrics from the last check on /metrics (unless we don't have one yet).
	if !state.FromScrapeLoop && lastMetricPoint.Labels != nil {
		mfs := model.MetricPointsToFamilies([]types.MetricPoint{lastMetricPoint})

		return mfs, nil
	}

	point := cg.check.Check(ctx, cg.scheduleUpdate)

	// Keep the last point. We don't keep the metric families because
	// they might be mutated later and cause data races.
	cg.l.Lock()
	cg.lastMetricPoint = point
	cg.l.Unlock()

	mfs := model.MetricPointsToFamilies([]types.MetricPoint{point})

	return mfs, nil
}

// Gather runs the check and returns the result as metric families.
func (cg *Gatherer) Gather() ([]*dto.MetricFamily, error) {
	logger.V(2).Println("Gather() called directly on a check gatherer, this is a bug!")

	ctx, cancel := context.WithTimeout(context.Background(), defaultGatherTimeout)
	defer cancel()

	return cg.GatherWithState(ctx, registry.GatherState{T0: time.Now()})
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

func (cg *Gatherer) Close() {
	cg.check.Close()
}

func (cg *Gatherer) DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	return cg.check.DiagnosticArchive(ctx, archive)
}
