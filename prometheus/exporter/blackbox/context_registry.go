package blackbox

import (
	"context"
	"glouton/prometheus/registry"
	"sync"

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
	return g.GatherWithState(context.Background(), registry.GatherState{})
}

func (g *gathererWithContext) GatherWithState(ctx context.Context, state registry.GatherState) ([]*dto.MetricFamily, error) {
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
