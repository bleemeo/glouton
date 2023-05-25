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

package registry

import (
	"context"
	"glouton/inputs"
	"glouton/prometheus/model"
	"glouton/types"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	dto "github.com/prometheus/client_model/go"
)

// inputGatherer gathers metric from a Telegraf input.
type inputGatherer struct {
	input  telegraf.Input
	buffer *pointBuffer

	l          sync.Mutex
	lastPoints []types.MetricPoint
	lastErr    error
}

// newInputGatherer returns an initialized input gatherer.
func newInputGatherer(input telegraf.Input) *inputGatherer {
	gatherer := &inputGatherer{
		input:  input,
		buffer: &pointBuffer{},
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
		i.lastErr = err
		i.l.Unlock()
	}

	i.l.Lock()
	defer i.l.Unlock()

	mfs := model.MetricPointsToFamilies(i.lastPoints)

	return mfs, i.lastErr
}

// pointBuffer add points received from PushPoints to a buffer.
type pointBuffer struct {
	points []types.MetricPoint
}

// PushPoints adds points to the buffer.
func (p *pointBuffer) PushPoints(_ context.Context, points []types.MetricPoint) {
	p.points = append(p.points, points...)
}

// Points returns the buffer and resets it.
func (p *pointBuffer) Points() []types.MetricPoint {
	points := p.points

	p.points = p.points[:0]

	return points
}
