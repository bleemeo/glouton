package api

//go:generate go run github.com/99designs/gqlgen

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	"agentgo/types"

	"github.com/vektah/gqlparser/gqlerror"
)

type Resolver struct {
	api *API
}

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
		finalEnd = time.Now().UTC().Format(time.RFC3339)
		finalStart = time.Now().UTC().Add(time.Duration(-minutes) * time.Minute).Format(time.RFC3339)
	} else {
		finalStart = start
		finalEnd = end
	}
	timeStart, _ := time.Parse(time.RFC3339, finalStart)
	timeEnd, _ := time.Parse(time.RFC3339, finalEnd)
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
	if r.api.dockerFact == nil {
		return nil, gqlerror.Errorf("Can not retrieve containers at this moment. Please try later")
	}
	containers, err := r.api.dockerFact.Containers(ctx, time.Hour, false)
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
		if (allContainers || container.IsRunning()) && (strings.Contains(container.Name(), search) || strings.Contains(container.Image(), search) || strings.Contains(container.ID(), search) || strings.Contains(container.Command(), search)) {
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
				metricFilters := map[string]string{
					"item":     container.Name(),
					"__name__": m,
				}
				metrics, err := r.api.db.Metrics(metricFilters)
				if err != nil {
					log.Println(err)
					return nil, gqlerror.Errorf("Can not retrieve Containers")
				}
				if len(metrics) > 0 {
					points, err := metrics[0].Points(time.Now().UTC().Add(-15*time.Minute), time.Now().UTC())
					if err != nil {
						log.Println(err)
						return nil, gqlerror.Errorf("Can not retrieve Containers")
					}
					var point float64
					if len(points) > 0 {
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
	if input != nil {
		if len(containersRes) > input.Offset {
			to := input.Offset + input.Limit
			if len(containersRes) <= input.Offset+input.Limit {
				to = len(containersRes)
			}
			containersRes = containersRes[input.Offset:to]
		} else if len(containersRes) <= input.Offset {
			containersRes = []*Container{}
		}
	}
	return containersRes, nil
}

func (r *queryResolver) Processes(ctx context.Context, containerID *string) ([]*Process, error) {
	if r.api.psFact == nil {
		return nil, gqlerror.Errorf("Can not retrieve processes at this moment. Please try later")
	}
	duration, _ := time.ParseDuration("1h")
	processes, err := r.api.psFact.Processes(ctx, duration)
	if err != nil {
		log.Println(err)
		return nil, gqlerror.Errorf("Can not retrieve processes")
	}
	processesRes := []*Process{}
	for _, process := range processes {
		p := &Process{
			Pid:         process.PID,
			Ppid:        process.PPID,
			CreateTime:  process.CreateTime,
			Cmdline:     strings.Join(process.CmdLine, " "),
			Name:        process.Name,
			MemoryRss:   int(process.MemoryRSS),
			CPUPercent:  process.CPUPercent,
			CPUTime:     process.CPUTime,
			Status:      process.Status,
			Username:    process.Username,
			Executable:  process.Executable,
			ContainerID: process.ContainerID,
		}
		processesRes = append(processesRes, p)
	}
	return processesRes, nil
}

func (r *queryResolver) Facts(ctx context.Context) ([]*Fact, error) {
	if r.api.factProvider == nil {
		return nil, gqlerror.Errorf("Can not retrieve facts at this moment. Please try later")
	}
	duration, _ := time.ParseDuration("1h")
	facts, err := r.api.factProvider.Facts(ctx, duration)
	if err != nil {
		log.Println(err)
		return nil, gqlerror.Errorf("Can not retrieve facts")
	}
	factsRes := []*Fact{}
	for k, v := range facts {
		f := &Fact{
			Name:  k,
			Value: v,
		}
		factsRes = append(factsRes, f)
	}
	return factsRes, nil
}
