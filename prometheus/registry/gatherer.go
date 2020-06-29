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
// Gather() on a CustomGatherer, most of the time Gather() will directly call GatherWithState(GatherState{}),
// please make sure that default values are sensible. For example, respectTime *must* be false, as we want
// queries on /metrics to always probe the collectors, and respectTime chaneg that behavior).
type GatherState struct {
	// respectTime allows the gatherer to collect metrics only from time to time, and not for every call.
	// THis ia notably the way we notify TickingGatherer to actually use the ticker.
	respectTime bool
}

type CollectState GatherState

// CustomGatherer is a generalization of prometheus.Gather.
type CustomGatherer interface {
	prometheus.Gatherer
	GatherWithState(GatherState) ([]*dto.MetricFamily, error)
}

// TickingGatherer is a prometheus gatherer that only collect metrics every once in a while.
type TickingGatherer struct {
	gatherer prometheus.Gatherer
	ticker   *time.Ticker
}

// NewTickingGatherer creates a gatherer that only collect metrics once every refreshRateSeconds.
func NewTickingGatherer(gatherer prometheus.Gatherer, refreshRateSeconds int) TickingGatherer {
	res := TickingGatherer{
		gatherer: gatherer,
		ticker:   time.NewTicker(time.Duration(refreshRateSeconds) * time.Second),
	}

	return res
}

// Gather implements CustomGatherer.
func (g TickingGatherer) Gather() ([]*dto.MetricFamily, error) {
	return g.GatherWithState(GatherState{})
}

// GatherWithState implements CustomGatherer.
func (g TickingGatherer) GatherWithState(state GatherState) ([]*dto.MetricFamily, error) {
	// when we have respectTime (set when we do internal queries, but not when /metrics is probed),
	// we check with the timer to see if we must Gather().
	if !state.respectTime {
		if cg, ok := g.gatherer.(CustomGatherer); ok {
			return cg.GatherWithState(state)
		}

		return g.gatherer.Gather()
	}

	select {
	case <-g.ticker.C:
		if cg, ok := g.gatherer.(CustomGatherer); ok {
			return cg.GatherWithState(state)
		}

		return g.gatherer.Gather()
	default:
		return make([]*dto.MetricFamily, 0), nil
	}
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

// Gather implements CustomGatherer.
func (g labeledGatherer) Gather() ([]*dto.MetricFamily, error) {
	return g.GatherWithState(GatherState{})
}

// GatherWithState implements CustomGatherer.
func (g labeledGatherer) GatherWithState(state GatherState) ([]*dto.MetricFamily, error) {
	var mfs []*dto.MetricFamily

	var err error

	if cg, ok := g.source.(CustomGatherer); ok {
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

// GatherWithState implements CustomGatherer.
func (gs Gatherers) GatherWithState(state GatherState) ([]*dto.MetricFamily, error) {
	metricFamiliesByName := map[string]*dto.MetricFamily{}

	var errs prometheus.MultiError

	wg := sync.WaitGroup{}
	wg.Add(len(gs))

	mutex := sync.RWMutex{}

	for _, g := range gs {
		go func(g prometheus.Gatherer) {
			var mfs []*dto.MetricFamily

			var err error

			if cg, ok := g.(CustomGatherer); ok {
				mfs, err = cg.GatherWithState(state)
			} else {
				mfs, err = g.Gather()
			}

			if err != nil {
				errs = append(errs, err)
			}

			for _, mf := range mfs {
				mutex.RLock()
				existingMF, exists := metricFamiliesByName[mf.GetName()]
				mutex.RUnlock()

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
					mutex.Lock()
					metricFamiliesByName[mf.GetName()] = existingMF
					mutex.Unlock()
				}

				existingMF.Metric = append(existingMF.Metric, mf.Metric...)
			}

			wg.Done()
		}(g)
	}

	wg.Wait()

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

// Gather implements CustomGatherer.
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
