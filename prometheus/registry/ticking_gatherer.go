package registry

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

type tickingGathererState int

const (
	// Initialized is the initial state, no gather() calls have been received yet, the next gather() calls will succeed,
	// so that metric collection can start as soon as possible
	Initialized tickingGathererState = iota
	// FirstRun is used when one gather() call have been received, do not gather() anymore until startTime is reached, at which
	// point we will enter the Running state
	FirstRun
	// Running is used when Tick has been started, normal operating mode
	Running
	// Stopped is used when Tick has been stopped, using this gatherer will no longer work
	Stopped
)

// TickingGatherer is a prometheus gatherer that only collect metrics every once in a while.
type TickingGatherer struct {
	// we need this exposed in order to stop it (otherwise we'll leak goroutines)
	Ticker    *time.Ticker
	gatherer  prometheus.Gatherer
	rate      time.Duration
	startTime time.Time

	l     sync.Mutex
	state tickingGathererState
}

// NewTickingGatherer creates a gatherer that only collect metrics once every refreshRate instants.
func NewTickingGatherer(gatherer prometheus.Gatherer, creationDate time.Time, refreshRate time.Duration) *TickingGatherer {
	// the logic is that the point at which we should start the ticker is the first occurrence of creationDate + x * refreshRate in the future
	// (this is sadly necessary as we cannot start a ticker in the past, per go design)
	startTime := creationDate.Add(time.Since(creationDate).Truncate(refreshRate)).Add(refreshRate).Truncate(10 * time.Second)

	return &TickingGatherer{
		gatherer:  gatherer,
		rate:      refreshRate,
		startTime: startTime,

		l: sync.Mutex{},
	}
}

// Stop sets the gatherer state to stopped, meaning the gatherer won't work anymore.
func (g *TickingGatherer) Stop() {
	g.l.Lock()
	defer g.l.Unlock()

	g.state = Stopped

	if g.Ticker != nil {
		g.Ticker.Stop()
	}
}

// Gather implements prometheus.Gather.
func (g *TickingGatherer) Gather() ([]*dto.MetricFamily, error) {
	return g.GatherWithState(GatherState{})
}

// GatherWithState implements GathererWithState.
func (g *TickingGatherer) GatherWithState(state GatherState) ([]*dto.MetricFamily, error) {
	if state.NoTick {
		return g.gatherNow(state)
	}

	g.l.Lock()
	defer g.l.Unlock()

	switch g.state {
	case Initialized:
		g.state = FirstRun

		return g.gatherNow(state)
	case FirstRun:
		if time.Now().After(g.startTime) {
			// we are now synced with the date of creation of the object, start the ticker and run immediately
			g.state = Running
			g.Ticker = time.NewTicker(g.rate)

			return g.gatherNow(state)
		}
	case Running:
		select {
		case <-g.Ticker.C:
			return g.gatherNow(state)
		default:
		}
	}

	return nil, nil
}

func (g *TickingGatherer) gatherNow(state GatherState) ([]*dto.MetricFamily, error) {
	if cg, ok := g.gatherer.(GathererWithState); ok {
		return cg.GatherWithState(state)
	}

	return g.gatherer.Gather()
}
