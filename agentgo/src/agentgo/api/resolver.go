package api

import (
	"context"
	"time"

	"github.com/vektah/gqlparser/gqlerror"
) // THIS CODE IS A STARTING POINT ONLY. IT WILL NOT BE UPDATED WITH SCHEMA CHANGES.

type Resolver struct {
	api *API
}

func (r *Resolver) Query() QueryResolver {
	return &queryResolver{r}
}

type queryResolver struct{ *Resolver }

func (r *queryResolver) Metrics(ctx context.Context, input LabelsInput) ([]*Metric, error) {
	if r.api.db == nil {
		return nil, gqlerror.Errorf("Can not retrieve metrics at this moment. Please try later")
	}
	metricFilters := map[string]string{}
	if len(input.Labels) > 0 {
		for _, filter := range input.Labels {
			metricFilters[filter.Key] = filter.Value
		}
	}
	metrics, errMetrics := r.api.db.Metrics(metricFilters)
	if errMetrics != nil {
		return nil, gqlerror.Errorf("Can not retrieve metrics")
	}
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
func (r *queryResolver) Points(ctx context.Context, input LabelsInput, start string, end string) ([]*Metric, error) {
	if r.api.db == nil {
		return nil, gqlerror.Errorf("Can not retrieve points at this moment. Please try later")
	}
	metricFilters := map[string]string{}
	if len(input.Labels) > 0 {
		for _, filter := range input.Labels {
			metricFilters[filter.Key] = filter.Value
		}
	} else {
		return nil, gqlerror.Errorf("Please add at least one metric filter")
	}
	metrics, errMetrics := r.api.db.Metrics(metricFilters)
	layout := "2006-01-02T15:04:05.000Z"
	timeStart, errTimeStart := time.Parse(layout, start)
	timeEnd, errTimeEnd := time.Parse(layout, end)
	if errMetrics != nil || errTimeStart != nil || errTimeEnd != nil {
		return nil, gqlerror.Errorf("Can not retrieve points")
	}
	metricsRes := []*Metric{}
	for _, metric := range metrics {
		metricRes := &Metric{}
		labels := metric.Labels()
		for key, value := range labels {
			label := &Label{Key: key, Value: value}
			metricRes.Labels = append(metricRes.Labels, label)
		}
		points, errPoints := metric.Points(timeStart, timeEnd)
		if errPoints != nil {
			return nil, gqlerror.Errorf("Can not retrieve points")
		}
		for _, point := range points {
			pointRes := &Point{Time: point.Time.UTC(), Value: point.Value}
			metricRes.Points = append(metricRes.Points, pointRes)
		}
		metricsRes = append(metricsRes, metricRes)
	}
	return metricsRes, nil
}
