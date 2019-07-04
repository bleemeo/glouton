package api

//go:generate go run github.com/99designs/gqlgen

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	"agentgo/facts"
	"agentgo/types"

	"github.com/vektah/gqlparser/gqlerror"
)

type Resolver struct {
	api        *API
	dockerFact *facts.DockerProvider
}

const layout = "2006-01-02T15:04:05Z"

func (r *Resolver) Query() QueryResolver {
	return &queryResolver{r}
}

type queryResolver struct{ *Resolver }

func (r *queryResolver) Metrics(ctx context.Context, metricsFilter []*MetricInput) ([]*Metric, error) {
	if r.api.db == nil {
		return nil, gqlerror.Errorf("Can not retrieve metrics at this moment. Please try later")
	}
	metrics := []types.Metric{}
	if len(metricsFilter) > 0 {
		for _, mf := range metricsFilter {
			if len(mf.Labels) > 0 {
				metricFilters := map[string]string{}
				for _, label := range mf.Labels {
					metricFilters[label.Key] = label.Value
				}
				newMetrics, err := r.api.db.Metrics(metricFilters)
				if err != nil {
					log.Println(err)
					return nil, gqlerror.Errorf("Can not retrieve metrics")
				}
				metrics = append(metrics, newMetrics...)
			}
		}
	} else {
		var err error
		metrics, err = r.api.db.Metrics(map[string]string{})
		if err != nil {
			log.Println(err)
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
func (r *queryResolver) Points(ctx context.Context, metricsFilter []*MetricInput, start string, end string, minutes int) ([]*Metric, error) {
	if r.api.db == nil {
		return nil, gqlerror.Errorf("Can not retrieve points at this moment. Please try later")
	}
	metrics := []types.Metric{}
	if len(metricsFilter) > 0 {
		for _, mf := range metricsFilter {
			if len(mf.Labels) > 0 {
				metricFilters := map[string]string{}
				for _, label := range mf.Labels {
					metricFilters[label.Key] = label.Value
				}
				newMetrics, err := r.api.db.Metrics(metricFilters)
				if err != nil {
					log.Println(err)
					return nil, gqlerror.Errorf("Can not retrieve metrics")
				}
				metrics = append(metrics, newMetrics...)
			}
		}
	} else {
		return nil, gqlerror.Errorf("Can not retrieve points for every metrics")
	}
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
		points, err := metric.Points(timeStart, timeEnd)
		if err != nil {
			log.Println(err)
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
func (r *queryResolver) Containers(ctx context.Context, input *Pagination, allContainers bool, search string) ([]*Container, error) {
	if r.dockerFact == nil {
		return nil, gqlerror.Errorf("Can not retrieve points at this moment. Please try later")
	}
	duration, _ := time.ParseDuration("1h")
	containers, err := r.dockerFact.Containers(ctx, duration)
	if err != nil {
		log.Println(err)
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

	sort.Slice(containers, func(i, j int) bool {
		return strings.Compare(containers[i].Name(), containers[j].Name()) < 0
	})
	for _, container := range containers {
		if (allContainers || container.State() == "running") && (strings.Contains(container.Name(), search) || strings.Contains(container.Image(), search) || strings.Contains(container.ID(), search) || strings.Contains(container.Command(), search)) {
			createdAt := container.CreatedAt()
			startedAt := container.StartedAt()
			finishedAt := container.FinishedAt()
			c := &Container{
				Command:     container.Command(),
				CreatedAt:   &createdAt,
				ID:          container.ID(),
				Image:       container.Image(),
				InspectJSON: container.InspectJSON(),
				Name:        container.Name(),
				StartedAt:   &startedAt,
				State:       container.State(),
				FinishedAt:  &finishedAt,
			}
			for _, m := range containerMetrics {
				metricFilters := map[string]string{}
				metricFilters["item"] = container.Name()
				metricFilters["__name__"] = m
				metrics, err := r.api.db.Metrics(metricFilters)
				if err != nil {
					log.Println(err)
					return nil, gqlerror.Errorf("Can not retrieve Containers")
				}
				if metrics != nil && len(metrics) > 0 {
					points, err := metrics[0].Points(time.Now().UTC().Add(time.Duration(-1)*time.Minute), time.Now().UTC())
					if err != nil {
						log.Println(err)
						return nil, gqlerror.Errorf("Can not retrieve Containers")
					}
					var point float64
					if points != nil && len(points) > 0 {
						point = points[len(points)-1].Value
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
	}
	containersSliced := containersRes
	if input != nil {
		if len(containersRes) > input.Offset {
			to := input.Offset + input.Limit
			if len(containersRes) <= input.Offset+input.Limit {
				to = len(containersRes)
			}
			containersSliced = containersRes[input.Offset:to]
		} else if len(containersRes) <= input.Offset {
			containersSliced = []*Container{}
		}
	}
	return containersSliced, nil
}
