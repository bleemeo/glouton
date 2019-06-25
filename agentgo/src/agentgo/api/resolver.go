package api

import (
	"context"
	"log"
) // THIS CODE IS A STARTING POINT ONLY. IT WILL NOT BE UPDATED WITH SCHEMA CHANGES.

type Resolver struct{}

func (r *Resolver) Query() QueryResolver {
	return &queryResolver{r}
}

type queryResolver struct{ *Resolver }

func (r *queryResolver) Metrics(ctx context.Context, input Labels) ([]*Metric, error) {
	metricFilters := map[string]string{}
	if len(input.Labels) > 0 {
		for _, filter := range input.Labels {
			metricFilters[filter.Key] = filter.Value
		}
	}
	log.Println(metricFilters)
	metrics, _ := globalDb.Metrics(metricFilters)
	log.Println(metrics)
	metricsRes := []*Metric{}
	for _, metric := range metrics {
		metricRes := &Metric{}
		labels := metric.Labels()
		for key, value := range labels {
			label := &Label{Key: key, Value: value}
			metricRes.Labels = append(metricRes.Labels, label)
		}
		metricsRes = append(metricsRes, metricRes)
	}
	return metricsRes, nil
}
