// Copyright 2015-2024 Bleemeo
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

package registry

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
	dto "github.com/prometheus/client_model/go"
)

// inputGatherer gathers metric from a Telegraf input.
type inputGatherer struct {
	input  telegraf.Input
	buffer *PointBuffer

	l          sync.Mutex
	lastPoints []types.MetricPoint
	lastErr    error
}

// newInputGatherer returns an initialized input gatherer.
func newInputGatherer(input telegraf.Input) *inputGatherer {
	gatherer := &inputGatherer{
		input:  input,
		buffer: &PointBuffer{},
	}

	return gatherer
}

// Gather implements prometheus.Gatherer .
func (i *inputGatherer) Gather() ([]*dto.MetricFamily, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultGatherTimeout)
	defer cancel()

	return i.GatherWithState(ctx, GatherState{T0: time.Now()})
}

func (i *inputGatherer) GatherWithState(ctx context.Context, state GatherState) ([]*dto.MetricFamily, error) {
	// Do the gather only in the scrape loop and reuse the last points on /metrics.
	if state.FromScrapeLoop {
		// Gather metric points in the buffer.
		acc := &inputs.Accumulator{
			Pusher:  i.buffer,
			Context: ctx,
		}

		err := i.input.Gather(acc)

		i.l.Lock()
		i.lastPoints = i.buffer.Points()
		i.lastErr = errors.Join(append(acc.Errors(), err)...)
		i.l.Unlock()
	}

	i.l.Lock()
	defer i.l.Unlock()

	mfs := model.MetricPointsToFamilies(i.lastPoints)

	return mfs, i.lastErr
}

// PointBuffer add points received from PushPoints to a buffer.
type PointBuffer struct {
	points []types.MetricPoint
	l      sync.Mutex
}

// PushPoints adds points to the buffer.
func (p *PointBuffer) PushPoints(_ context.Context, points []types.MetricPoint) {
	p.l.Lock()
	defer p.l.Unlock()

	p.points = append(p.points, points...)
}

// Points returns the buffer and resets it.
func (p *PointBuffer) Points() []types.MetricPoint {
	p.l.Lock()
	defer p.l.Unlock()

	points := p.points

	p.points = p.points[:0]

	return points
}
