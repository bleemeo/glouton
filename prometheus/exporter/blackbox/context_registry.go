// Copyright 2015-2025 Bleemeo
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

package blackbox

import (
	"context"
	"sync"
	"time"

	"github.com/bleemeo/glouton/prometheus/registry"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

type gathererWithContext struct {
	reg            *prometheus.Registry
	l              sync.Mutex
	target         configTarget
	currentContext context.Context //nolint:containedctx
}

func newGatherer(target configTarget) (*gathererWithContext, error) {
	reg := prometheus.NewRegistry()
	self := &gathererWithContext{
		reg:    reg,
		target: target,
	}

	err := self.reg.Register(self)

	return self, err
}

func (g *gathererWithContext) Gather() ([]*dto.MetricFamily, error) {
	return g.GatherWithState(context.Background(), registry.GatherState{T0: time.Now()})
}

func (g *gathererWithContext) GatherWithState(ctx context.Context, _ registry.GatherState) ([]*dto.MetricFamily, error) {
	g.l.Lock()
	defer g.l.Unlock()

	g.currentContext = ctx
	mfs, err := g.reg.Gather()

	g.currentContext = nil

	return mfs, err
}

func (g *gathererWithContext) Collect(ch chan<- prometheus.Metric) {
	g.target.CollectWithContext(g.currentContext, ch)
}

func (g *gathererWithContext) Describe(ch chan<- *prometheus.Desc) {
	g.target.Describe(ch)
}
