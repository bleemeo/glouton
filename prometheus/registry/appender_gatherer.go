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
	"glouton/prometheus/model"
	"sort"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
)

// appenderGatherer call a AppenderCallback.
type appenderGatherer struct {
	cb      AppenderCallback
	opt     AppenderRegistrationOption
	lastApp *model.BufferAppender
	lastErr error
	l       sync.Mutex
}

// Gather implements prometheus.Gatherer .
func (g *appenderGatherer) Gather() ([]*dto.MetricFamily, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultGatherTimeout)
	defer cancel()

	return g.GatherWithState(ctx, GatherState{T0: time.Now()})
}

func (g *appenderGatherer) GatherWithState(ctx context.Context, state GatherState) ([]*dto.MetricFamily, error) {
	var err error

	if state.FromScrapeLoop || g.opt.CallForMetricsEndpoint {
		app := model.NewBufferAppender()

		err = g.cb.Collect(ctx, app)
		if err == nil {
			_ = app.Commit()
		}

		if !g.opt.HonorTimestamp {
			now := state.T0
			if now.IsZero() {
				now = time.Now().Truncate(time.Second)
			}

			for _, samples := range app.Committed {
				for i := range samples {
					samples[i].Point.T = now.UnixMilli()
				}
			}
		}

		g.l.Lock()
		g.lastApp = app
		g.lastErr = err
		g.l.Unlock()
	}

	g.l.Lock()
	defer g.l.Unlock()

	var mfs []*dto.MetricFamily

	if g.lastApp != nil {
		for _, samples := range g.lastApp.Committed {
			mf, err := model.SamplesToMetricFamily(samples, nil)
			if err != nil {
				return nil, err
			}

			mfs = append(mfs, mf)
		}
	}

	sort.Slice(mfs, func(i, j int) bool {
		return mfs[i].GetName() < mfs[j].GetName()
	})

	return mfs, g.lastErr
}
