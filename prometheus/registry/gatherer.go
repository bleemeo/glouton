package registry

import (
	"fmt"
	"glouton/types"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/pkg/labels"
)

// GatherState is an argument given to gatherers that support it. It allows us to give extra informations
// to gatherers. Due to the way such objects are contructed when no argument is supplied (when calling
// Gather() on a GathererWithState, most of the time Gather() will directly call GatherWithState(GatherState{}),
// please make sure that default values are sensible. For example, respectTime *must* be false, as we want
// queries on /metrics to always probe the collectors, and respectTime chaneg that behavior).
type GatherState struct {
	// respectTime allows the gatherer to collect metrics only from time to time, and not for every call.
	// This is notably the way we notify TickingGatherer to actually use the ticker.
	respectTime bool
}

// GathererWithState is a generalization of prometheus.Gather.
type GathererWithState interface {
	GatherWithState(GatherState) ([]*dto.MetricFamily, error)
}

type TickingGathererState int

const (
	// Initial state, no gather() calls have been received yet, the next gather() calls will succeed,
	// so that metric colleciton can start as soon as possible
	Initialized TickingGathererState = iota
	// one gather() call have been received, do not gather() anymore until startTime is reached, at which
	// point we will enter the Running state
	FirstRun
	// Ticker have been starting, normal operating mode
	Running
	// Ticked have been stopped, using this gatherer will no longer work
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
	state TickingGathererState
}

// NewTickingGatherer creates a gatherer that only collect metrics once every refreshRate instants.
func NewTickingGatherer(gatherer prometheus.Gatherer, creationDate time.Time, refreshRate time.Duration) *TickingGatherer {
	// the logic is that the point at which we should start the ticker is the first occurence of creationDate + x * refreshRate in the future
	// (this is sadly necessary as we cannot start a ticker in the past, per go design)
	startTime := creationDate.Add(time.Now().Sub(creationDate).Truncate(refreshRate)).Add(refreshRate).Truncate(10 * time.Second)

	return &TickingGatherer{
		gatherer:  gatherer,
		rate:      refreshRate,
		startTime: startTime,

		l: sync.Mutex{},
	}
}

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
	// when we have respectTime (set when we do internal queries, but not when /metrics is probed),
	// we check with the timer to see if we must Gather().
	if !state.respectTime {
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

// labeledGatherer provide a gatherer that will add provided labels to all metrics.
// It also allow to gather to MetricPoints.
type labeledGatherer struct {
	source      prometheus.Gatherer
	labels      []*dto.LabelPair
	annotations types.MetricAnnotations
}

func newLabeledGatherer(g prometheus.Gatherer, extraLabels labels.Labels, annotations types.MetricAnnotations) labeledGatherer {
	labels := make([]*dto.LabelPair, 0, len(extraLabels))

	for _, l := range extraLabels {
		l := l
		if !strings.HasPrefix(l.Name, model.ReservedLabelPrefix) {
			labels = append(labels, &dto.LabelPair{
				Name:  &l.Name,
				Value: &l.Value,
			})
		}
	}

	return labeledGatherer{
		source:      g,
		labels:      labels,
		annotations: annotations,
	}
}

// Gather implements prometheus.Gather.
func (g labeledGatherer) Gather() ([]*dto.MetricFamily, error) {
	return g.GatherWithState(GatherState{})
}

// GatherWithState implements GathererWithState.
func (g labeledGatherer) GatherWithState(state GatherState) ([]*dto.MetricFamily, error) {
	var mfs []*dto.MetricFamily

	var err error

	if cg, ok := g.source.(GathererWithState); ok {
		mfs, err = cg.GatherWithState(state)
	} else {
		mfs, err = g.source.Gather()
	}

	if len(g.labels) == 0 {
		return mfs, err
	}

	for _, mf := range mfs {
		for i, m := range mf.Metric {
			m.Label = mergeLabels(m.Label, g.labels)
			mf.Metric[i] = m
		}
	}

	return mfs, err
}

// mergeLabels merge two sorted list of labels. In case of name conflict, value from b wins.
func mergeLabels(a []*dto.LabelPair, b []*dto.LabelPair) []*dto.LabelPair {
	result := make([]*dto.LabelPair, 0, len(a)+len(b))
	aIndex := 0

	for _, bLabel := range b {
		for aIndex < len(a) && a[aIndex].GetName() < bLabel.GetName() {
			result = append(result, a[aIndex])
			aIndex++
		}

		if aIndex < len(a) && a[aIndex].GetName() == bLabel.GetName() {
			aIndex++
		}

		result = append(result, bLabel)
	}

	for aIndex < len(a) {
		result = append(result, a[aIndex])
		aIndex++
	}

	return result
}

func (g labeledGatherer) GatherPoints(state GatherState) ([]types.MetricPoint, error) {
	mfs, err := g.GatherWithState(state)
	points := familiesToMetricPoints(mfs)

	if (g.annotations != types.MetricAnnotations{}) {
		for i := range points {
			points[i].Annotations = g.annotations
		}
	}

	return points, err
}

type sliceGatherer []*dto.MetricFamily

// Gather implements Gatherer.
func (s sliceGatherer) Gather() ([]*dto.MetricFamily, error) {
	return s, nil
}

// Gatherers do the same as prometheus.Gatherers but allow different gatherer
// to have different metric help text.
// The type must still be the same.
//
// This is useful when scrapping multiple endpoints which provide the same metric
// (like "go_gc_duration_seconds") but changed its help_text.
//
// The first help_text win.
type Gatherers []prometheus.Gatherer

// GatherWithState implements GathererWithState.
func (gs Gatherers) GatherWithState(state GatherState) ([]*dto.MetricFamily, error) {
	metricFamiliesByName := map[string]*dto.MetricFamily{}

	var errs prometheus.MultiError
	var mfs []*dto.MetricFamily

	wg := sync.WaitGroup{}
	wg.Add(len(gs))

	mutex := sync.Mutex{}

	// run gather in parallel
	for _, g := range gs {
		go func(g prometheus.Gatherer) {
			var currentMFs []*dto.MetricFamily

			var err error

			if cg, ok := g.(GathererWithState); ok {
				currentMFs, err = cg.GatherWithState(state)
			} else {
				currentMFs, err = g.Gather()
			}

			mutex.Lock()
			if err != nil {
				errs = append(errs, err)
			}
			mfs = append(mfs, currentMFs...)
			mutex.Unlock()

			wg.Done()
		}(g)
	}

	wg.Wait()

	for _, mf := range mfs {
		existingMF, exists := metricFamiliesByName[mf.GetName()]

		if exists {
			if existingMF.GetType() != mf.GetType() {
				errs = append(errs, fmt.Errorf(
					"gathered metric family %s has type %s but should have %s",
					mf.GetName(), mf.GetType(), existingMF.GetType(),
				))

				continue
			}
		} else {
			existingMF = &dto.MetricFamily{}
			existingMF.Name = mf.Name
			existingMF.Help = mf.Help
			existingMF.Type = mf.Type
			metricFamiliesByName[mf.GetName()] = existingMF
		}

		existingMF.Metric = append(existingMF.Metric, mf.Metric...)
	}

	result := make([]*dto.MetricFamily, 0, len(metricFamiliesByName))

	for _, f := range metricFamiliesByName {
		result = append(result, f)
	}

	// Still use prometheus.Gatherers because it will:
	// * Remove (and complain) about possible duplicate of metric
	// * sort output
	// * maybe other sanity check :)

	promGatherers := prometheus.Gatherers{
		sliceGatherer(result),
	}

	sortedResult, err := promGatherers.Gather()
	if err != nil {
		if multiErr, ok := err.(prometheus.MultiError); ok {
			errs = append(errs, multiErr...)
		} else {
			errs = append(errs, err)
		}
	}

	return sortedResult, errs.MaybeUnwrap()
}

// Gather implements prometheus.Gather.
func (gs Gatherers) Gather() ([]*dto.MetricFamily, error) {
	return gs.GatherWithState(GatherState{})
}

type labeledGatherers []labeledGatherer

// GatherPoints return samples as MetricPoint instead of Prometheus MetricFamily.
func (gs labeledGatherers) GatherPoints(state GatherState) ([]types.MetricPoint, error) {
	result := []types.MetricPoint{}

	var errs prometheus.MultiError

	wg := sync.WaitGroup{}
	wg.Add(len(gs))

	mutex := sync.Mutex{}

	for _, g := range gs {
		go func(g labeledGatherer) {
			points, err := g.GatherPoints(state)

			mutex.Lock()

			if err != nil {
				errs = append(errs, err)
			}

			result = append(result, points...)

			mutex.Unlock()
			wg.Done()
		}(g)
	}

	wg.Wait()

	return result, errs.MaybeUnwrap()
}
