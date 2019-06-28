package api

import (
	"context"
	"time"

	"agentgo/types"

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
	metrics := []types.Metric{}
	if len(input.Labels) > 0 {
		for _, filter := range input.Labels {
			metricFilters := map[string]string{}
			metricFilters[filter.Key] = filter.Value
			newMetrics, errMetrics := r.api.db.Metrics(metricFilters)
			if errMetrics != nil {
				return nil, gqlerror.Errorf("Can not retrieve metrics")
			}
			metrics = append(metrics, newMetrics...)
		}
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
func (r *queryResolver) Points(ctx context.Context, input LabelsInput, start string, end string, minutes int) ([]*Metric, error) {
	if r.api.db == nil {
		return nil, gqlerror.Errorf("Can not retrieve points at this moment. Please try later")
	}
	metrics := []types.Metric{}
	if len(input.Labels) > 0 {
		for _, filter := range input.Labels {
			metricFilters := map[string]string{}
			metricFilters[filter.Key] = filter.Value
			newMetrics, errMetrics := r.api.db.Metrics(metricFilters)
			if errMetrics != nil {
				return nil, gqlerror.Errorf("Can not retrieve metrics")
			}
			metrics = append(metrics, newMetrics...)
		}
	}
	layout := "2006-01-02T15:04:05.000Z"
	finalStart := ""
	finalEnd := ""
	if minutes != 0 {
		finalEnd = time.Now().UTC().Format(layout)
		finalStart = time.Now().UTC().Add(time.Duration(-minutes) * time.Minute).Format(layout)
	} else {
		finalStart = start
		finalEnd = end
	}
	timeStart, _ := time.Parse(layout, finalStart)
	timeEnd, _ := time.Parse(layout, finalEnd)
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
