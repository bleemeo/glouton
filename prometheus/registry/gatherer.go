package registry

import (
	"fmt"
	"glouton/types"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/pkg/labels"
)

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

func (g labeledGatherer) Gather() ([]*dto.MetricFamily, error) {
	mfs, err := g.source.Gather()

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

func (g labeledGatherer) GatherPoints() ([]types.MetricPoint, error) {
	mfs, err := g.Gather()
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

// Gather implements Gatherer.
func (gs Gatherers) Gather() ([]*dto.MetricFamily, error) {
	metricFamiliesByName := map[string]*dto.MetricFamily{}

	var errs prometheus.MultiError
	var mfs []*dto.MetricFamily

	wg := sync.WaitGroup{}
	wg.Add(len(gs))

	mutex := sync.Mutex{}

	// run gather in parallel
	for _, g := range gs {
		go func(g prometheus.Gatherer) {
			currentMFs, err := g.Gather()

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

type labeledGatherers []labeledGatherer

// GatherPoints return samples as MetricPoint instead of Prometheus MetricFamily.
func (gs labeledGatherers) GatherPoints() ([]types.MetricPoint, error) {
	result := []types.MetricPoint{}

	var errs prometheus.MultiError

	for _, g := range gs {
		points, err := g.GatherPoints()
		if err != nil {
			errs = append(errs, err)
		}

		result = append(result, points...)
	}

	return result, errs.MaybeUnwrap()
}
