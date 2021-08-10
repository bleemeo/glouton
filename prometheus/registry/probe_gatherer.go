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
	g prometheus.Gatherer

	l       sync.Mutex
	failing bool
	// LastFailed tells us whether the last check was a falling edge (a new failure)
	lastFailed     bool
	lastFailedTime time.Time
}

// NewProbeGatherer creates a new ProbeGatherer with the prometheus gatherer specified.
func NewProbeGatherer(gatherer prometheus.Gatherer) *ProbeGatherer {
	return &ProbeGatherer{
		g: gatherer,
	}
}

// Gather some metrics with with an empty gatherer state.
// While not a critical error, this function should never be called, as callers should know about
// GatherWithState().
func (p *ProbeGatherer) Gather() ([]*dto.MetricFamily, error) {
	logger.V(2).Println("Gather() called directly on a ProbeGatherer, this is a bug !")

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

	// when we see a new failure, we run the check again a minute later
	if p.lastFailed && time.Since(p.lastFailedTime) > time.Minute {
		// execute the query, do not wait for the next tick
		state.NoTick = true
	}

	if cg, ok := p.g.(GathererWithState); ok {
		mfs, err = cg.GatherWithState(ctx, state)
	} else {
		mfs, err = p.g.Gather()
	}

	for _, mf := range mfs {
		if *mf.Name == "probe_success" {
			if len(mf.Metric) == 0 {
				logger.V(2).Println("Invalid metric family 'probe_success', got 0 values inside")

				break
			}

			success := mf.Metric[0].GetGauge().GetValue() == 1.

			p.lastFailed = !success && !p.failing
			p.failing = !success

			if p.lastFailed {
				p.lastFailedTime = time.Now()
			}
		}
	}

	return mfs, err
}

// NonProbeGatherer Gatherer that wraps gatherers that aren't probe and that are not themselves wrapped by labeledGatherer
// (labeledGatherer perform roughly the same job w.r.t. probes and and it is thus not necessary to wrap all
// non-probes gatherers inside this struct).
type NonProbeGatherer struct {
	G prometheus.Gatherer
}

// Gather a set of metrics from a Gatherer that is not probe.
// While not a critical error, this function should never be called, as callers should know about
// GatherWithState().
func (p NonProbeGatherer) Gather() ([]*dto.MetricFamily, error) {
	logger.V(2).Println("Gather() called directly on a NonProbeGatherer, this is a bug !")

	ctx, cancel := context.WithTimeout(context.Background(), defaultGatherTimeout)
	defer cancel()

	return p.GatherWithState(ctx, GatherState{})
}

// GatherWithState uses the specified gather state along the gatherer to retrieve a set of metrics.
func (p NonProbeGatherer) GatherWithState(ctx context.Context, state GatherState) ([]*dto.MetricFamily, error) {
	if state.QueryType == OnlyProbes {
		return nil, nil
	}

	if cg, ok := p.G.(GathererWithState); ok {
		return cg.GatherWithState(ctx, state)
	}

	return p.G.Gather()
}
