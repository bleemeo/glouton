package registry

import (
	"fmt"
	"glouton/types"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// AnnotatedGatherer is a gatherer which also return a metric annotation to be used by all samples
type AnnotatedGatherer interface {
	prometheus.Gatherer
	Annotations() types.MetricAnnotations
}

// WrapWithAnnotation add annotations to a gatherer.
// This is used by the Registry when collecting metric from gatherers to the store
func WrapWithAnnotation(g prometheus.Gatherer, annotations types.MetricAnnotations) AnnotatedGatherer {
	return wrappedGatherer{
		Gatherer:    g,
		annotations: annotations,
	}
}

type wrappedGatherer struct {
	prometheus.Gatherer
	annotations types.MetricAnnotations
}

func (g wrappedGatherer) Annotations() types.MetricAnnotations {
	return g.annotations
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

	for _, g := range gs {
		mfs, err := g.Gather()
		if err != nil {
			errs = append(errs, err)
		}
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

// GatherPoints return samples as MetricPoint instead of Prometheus MetricFamily
func (gs Gatherers) GatherPoints() ([]types.MetricPoint, error) {
	result := []types.MetricPoint{}

	var errs prometheus.MultiError

	for _, g := range gs {
		mfs, err := g.Gather()
		if err != nil {
			errs = append(errs, err)
		}
		points := familiesToMetricPoints(mfs)

		if annotatedGatherer, ok := g.(AnnotatedGatherer); ok {
			annotations := annotatedGatherer.Annotations()
			for i := range points {
				points[i].Annotations = annotations
			}
		}
		result = append(result, points...)
	}

	return result, errs.MaybeUnwrap()
}
