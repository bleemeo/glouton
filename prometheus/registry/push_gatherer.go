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
	"time"

	dto "github.com/prometheus/client_model/go"
)

type pushGatherFunction func(context.Context, time.Time) error

// pushGatherer calls a function and nothing more.
// It only calls the function (a pushPoint callback)
// when the given state specifies "FromScrapeLoop".
type pushGatherer struct {
	fun     pushGatherFunction
	lastErr error
}

// Gather implements prometheus.Gatherer.
func (g *pushGatherer) Gather() ([]*dto.MetricFamily, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultGatherTimeout)
	defer cancel()

	return g.GatherWithState(ctx, GatherState{T0: time.Now()})
}

func (g *pushGatherer) GatherWithState(ctx context.Context, state GatherState) ([]*dto.MetricFamily, error) {
	if state.FromScrapeLoop {
		g.lastErr = g.fun(ctx, state.T0)
	}

	return nil, g.lastErr
}
