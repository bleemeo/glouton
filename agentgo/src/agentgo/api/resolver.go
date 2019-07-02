package api

import (
	"context"
	"time"
	"sort"
	"strings"

	"agentgo/types"
	"agentgo/facts"

	"github.com/vektah/gqlparser/gqlerror"
) // THIS CODE IS A STARTING POINT ONLY. IT WILL NOT BE UPDATED WITH SCHEMA CHANGES.

type Resolver struct {
	api *API
	dockerFact *facts.DockerProvider
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
	} else {
		var errMetrics error
		metrics, errMetrics = r.api.db.Metrics(map[string]string{})
		if errMetrics != nil {
			return nil, gqlerror.Errorf("Can not retrieve metrics")
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
	} else {
		return nil, gqlerror.Errorf("Can not retrieve points for every metrics")
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
func (r *queryResolver) Containers(ctx context.Context, input *Pagination) ([]*Container, error) {
	if r.dockerFact == nil {
		return nil, gqlerror.Errorf("Can not retrieve points at this moment. Please try later")
	}
	duration, _ := time.ParseDuration("1h")
	containers, errContainers := r.dockerFact.Containers(ctx, duration)
	if errContainers != nil {
		return nil, gqlerror.Errorf("Can not retrieve Containers")
	}
	containersRes := []*Container{}
	containerMetrics := []string{
		"docker_container_io_write_bytes",
		"docker_container_io_read_bytes",
		"docker_container_net_bits_recv",
		"docker_container_net_bits_sent",
		"docker_container_mem_used_perc",
		"docker_container_cpu_used",
	}
	layout := "2006-01-02T15:04:05.000Z"

	sort.Slice(containers, func(i, j int) bool {
		return strings.Compare(containers[i].Name(), containers[j].Name()) < 0
	})
	containersSliced := containers
	if input != nil {
		if len(containers) > input.Offset {
			to := input.Offset+input.Limit
			if len(containers) <= input.Offset + input.Limit {
				to = len(containers)
			}
			containersSliced = containers[input.Offset:to]
		} else if len(containers) <= input.Offset {
			containersSliced = []facts.Container{}
		}
	}
	for _, container := range containersSliced {
		createdAt, _ := time.Parse(layout, container.CreatedAt().Format(layout))
		startedAt, _ := time.Parse(layout, container.StartedAt().Format(layout))
		finishedAt, _ := time.Parse(layout, container.FinishedAt().Format(layout))
		c := &Container{
			Command: container.Command(),
			CreatedAt: &createdAt,
			ID: container.ID(),
			Image: container.Image(),
			InspectJSON: container.InspectJSON(),
			Name: container.Name(),
			StartedAt: &startedAt,
			State: container.State(),
			FinishedAt: &finishedAt,
		}
		for _, m := range containerMetrics {
			metricFilters := map[string]string{}
			metricFilters["item"] = container.Name()
			metricFilters["__name__"] = m
			metrics, errContainersMetrics := r.api.db.Metrics(metricFilters)
			if errContainersMetrics != nil {
				return nil, gqlerror.Errorf("Can not retrieve Containers's Metrics")
			}
			if metrics != nil && len(metrics) > 0 {
				points, errMetricsPoints := metrics[0].Points(time.Now().UTC().Add(time.Duration(-60 * time.Second)), time.Now().UTC())
				if errMetricsPoints != nil {
					return nil, gqlerror.Errorf("Can not retrieve Metics's Points")
				}
				var point float64
				if points != nil && len(points) > 0 {
					point = points[len(points) - 1].Value
				}
				switch m {
					case "docker_container_io_write_bytes":
						c.IoWriteBytes = point
					case "docker_container_io_read_bytes":
						c.IoReadBytes = point
					case "docker_container_net_bits_recv":
						c.NetBitsRecv = point
					case "docker_container_net_bits_sent":
						c.NetBitsSent = point
					case "docker_container_mem_used_perc":
						c.MemUsedPerc = point
					case "docker_container_cpu_used":
						c.CPUUsedPerc = point
				} 
			}
		}
		containersRes = append(containersRes, c)
	}
	return containersRes, nil
}
