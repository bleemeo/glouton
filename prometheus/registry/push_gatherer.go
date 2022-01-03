package registry

import (
	"context"
	"time"

	dto "github.com/prometheus/client_model/go"
)

// pushGatherer call a function an nothing more. It also only call the function
// (a pushPoint callback) when state specify "FromScrapeLoop".
type pushGatherer struct {
	fun func(context.Context, time.Time)
}

// Gather implements prometheus.Gatherer .
func (g pushGatherer) Gather() ([]*dto.MetricFamily, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultGatherTimeout)
	defer cancel()

	return g.GatherWithState(ctx, GatherState{T0: time.Now()})
}

func (g pushGatherer) GatherWithState(ctx context.Context, state GatherState) ([]*dto.MetricFamily, error) {
	if state.FromScrapeLoop {
		g.fun(ctx, state.T0)
	}

	return nil, nil
}
