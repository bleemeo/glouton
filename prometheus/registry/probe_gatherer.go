// Copyright 2015-2022 Bleemeo
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
	"glouton/logger"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// ProbeGatherer is a specific gatherer that wraps probes, choose when to Gather() depending on the GatherState argument.
type ProbeGatherer struct {
	g                   prometheus.Gatherer
	fastUpdateOnFailure bool
	scheduleUpdate      func(runAt time.Time)

	l              sync.Mutex
	successLastRun bool
}

// NewProbeGatherer creates a new ProbeGatherer with the prometheus gatherer specified.
func NewProbeGatherer(gatherer prometheus.Gatherer, fastUpdateOnFailure bool) *ProbeGatherer {
	return &ProbeGatherer{
		g:                   gatherer,
		fastUpdateOnFailure: fastUpdateOnFailure,
	}
}

// SetScheduleUpdate is called by Registry because this gathered will be used.
func (p *ProbeGatherer) SetScheduleUpdate(fun func(runAt time.Time)) {
	p.scheduleUpdate = fun
}

// Gather some metrics with with an empty gatherer state.
// While not a critical error, this function should never be called, as callers should know about
// GatherWithState().
func (p *ProbeGatherer) Gather() ([]*dto.MetricFamily, error) {
	logger.V(2).Println("Gather() called directly on a ProbeGatherer, this is a bug!")

	ctx, cancel := context.WithTimeout(context.Background(), defaultGatherTimeout)
	defer cancel()

	return p.GatherWithState(ctx, GatherState{})
}

// GatherWithState uses the specified gather state along the gatherer to retrieve a set of metrics.
func (p *ProbeGatherer) GatherWithState(ctx context.Context, state GatherState) ([]*dto.MetricFamily, error) {
	if state.QueryType == NoProbe {
		return nil, nil
	}

	var mfs []*dto.MetricFamily

	var err error

	p.l.Lock()
	defer p.l.Unlock()

	if cg, ok := p.g.(GathererWithState); ok {
		mfs, err = cg.GatherWithState(ctx, state)
	} else {
		mfs, err = p.g.Gather()
	}

	if !state.FromScrapeLoop {
		return mfs, err
	}

	for _, mf := range mfs {
		if *mf.Name == "probe_success" {
			if len(mf.Metric) == 0 {
				logger.V(2).Println("Invalid metric family 'probe_success', got 0 values inside")

				break
			}

			success := mf.Metric[0].GetGauge().GetValue() == 1.

			if !success && p.successLastRun && p.fastUpdateOnFailure && p.scheduleUpdate != nil {
				// we changed from Ok to not ok and fast update are activated. Schedule an
				// update the next minute to quickly recover.
				t0 := state.T0
				if t0.IsZero() {
					t0 = time.Now()
				}

				p.scheduleUpdate(t0.Add(time.Minute))
			}

			p.successLastRun = success
		}
	}

	return mfs, err
}
