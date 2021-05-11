// Copyright 2015-2019 Bleemeo
//
// bleemeo.com an infrastructure monitoring solution in the Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package api

//go:generate go run github.com/99designs/gqlgen

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"glouton/facts"
	"glouton/logger"
	"glouton/threshold"
	"glouton/types"

	"github.com/vektah/gqlparser/gqlerror"
)

// Resolver is the api resolver.
type Resolver struct {
	api *API
}

// Query queries the resolver.
func (r *Resolver) Query() QueryResolver {
	return &queryResolver{r}
}

type queryResolver struct{ *Resolver }

// Metrics returns a list of metrics
// They can be filtered with an array of metrics which contains an array of labels.
func (r *queryResolver) Metrics(ctx context.Context, metricsFilter []*MetricInput) ([]*Metric, error) {
	if r.api.DB == nil {
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

				newMetrics, err := r.api.DB.Metrics(metricFilters)
				if err != nil {
					logger.V(2).Printf("Can not retrieve metrics: %v", err)
					return nil, gqlerror.Errorf("Can not retrieve metrics")
				}

				metrics = append(metrics, newMetrics...)
			}
		}
	} else {
		var err error

		metrics, err = r.api.DB.Metrics(map[string]string{})
		if err != nil {
			logger.V(2).Printf("Can not retrieve metrics: %v", err)
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

// Points returns metrics's points between a time interval
// This interval could be between a start and end dates or X minutes from now
// Metrics can also be filtered.
func (r *queryResolver) Points(ctx context.Context, metricsFilter []*MetricInput, start string, end string, minutes int) ([]*Metric, error) {
	if r.api.DB == nil || r.api.Threshold == nil {
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

				newMetrics, err := r.api.DB.Metrics(metricFilters)
				if err != nil {
					logger.V(2).Printf("Can not retrieve metrics: %v", err)
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
		annotations := metric.Annotations()

		for key, value := range labels {
			label := &Label{Key: key, Value: value}
			metricRes.Labels = append(metricRes.Labels, label)
		}

		points, err := metric.Points(timeStart, timeEnd)
		if err != nil {
			logger.V(2).Printf("Can not retrieve points: %v", err)
			return nil, gqlerror.Errorf("Can not retrieve points")
		}

		for _, point := range points {
			pointRes := &Point{Time: point.Time.UTC(), Value: point.Value}
			metricRes.Points = append(metricRes.Points, pointRes)
		}

		thresholds := r.api.Threshold.GetThreshold(threshold.MetricNameItem{Item: annotations.BleemeoItem, Name: labels[types.LabelName]})
		threshold := &Threshold{
			LowCritical:  &thresholds.LowCritical,
			LowWarning:   &thresholds.LowWarning,
			HighWarning:  &thresholds.HighWarning,
			HighCritical: &thresholds.HighCritical,
		}

		threshold.converNan2Nil()

		metricRes.Thresholds = threshold
		metricsRes = append(metricsRes, metricRes)
	}

	return metricsRes, nil
}

func (threshold *Threshold) converNan2Nil() {
	if math.IsNaN(*threshold.LowCritical) {
		threshold.LowCritical = nil
	}

	if math.IsNaN(*threshold.LowWarning) {
		threshold.LowWarning = nil
	}

	if math.IsNaN(*threshold.HighCritical) {
		threshold.HighCritical = nil
	}

	if math.IsNaN(*threshold.HighWarning) {
		threshold.HighWarning = nil
	}
}

// Containers returns containers information
// These containers could be paginated and filtered by a search input or allContainers flag
// If there is a search filter, it will check search is contained in container's name / Image name / ID / command.
func (r *queryResolver) Containers(ctx context.Context, input *Pagination, allContainers bool, search string) (*Containers, error) {
	if r.api.ContainerRuntime == nil {
		return nil, gqlerror.Errorf("Can not retrieve containers at this moment. Please try later")
	}

	containers, err := r.api.ContainerRuntime.Containers(ctx, time.Hour, false)
	if err != nil {
		logger.V(2).Printf("Can not retrieve containers: %v", err)
		return nil, gqlerror.Errorf("Can not retrieve containers")
	}

	containersRes := []*Container{}

	sort.Slice(containers, func(i, j int) bool {
		return strings.Compare(containers[i].ContainerName(), containers[j].ContainerName()) < 0
	})

	nbContainers := 0
	nbCurrentContainers := 0

	for _, container := range containers {
		if allContainers || container.State().IsRunning() {
			nbContainers++
		}

		cmdString := strings.Join(container.Command(), " ")
		if (allContainers || container.State().IsRunning()) && (strings.Contains(container.ContainerName(), search) || strings.Contains(container.ImageName(), search) || strings.Contains(container.ID(), search) || strings.Contains(cmdString, search)) {
			nbCurrentContainers++

			createdAt := container.CreatedAt()
			startedAt := container.StartedAt()
			finishedAt := container.FinishedAt()
			c := &Container{
				Command:     cmdString,
				CreatedAt:   &createdAt,
				ID:          container.ID(),
				Image:       container.ImageName(),
				InspectJSON: container.ContainerJSON(),
				Name:        container.ContainerName(),
				StartedAt:   &startedAt,
				State:       container.State().String(),
				FinishedAt:  &finishedAt,
			}

			c, err = r.containerInformation(container, c)
			if err != nil {
				return nil, err
			}

			containersRes = append(containersRes, c)
		}
	}

	containersRes = paginateInformation(input, containersRes)

	return &Containers{Containers: containersRes, Count: nbContainers, CurrentCount: nbCurrentContainers}, nil
}

func paginateInformation(input *Pagination, containersRes []*Container) []*Container {
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

	return containersRes
}

func (r *queryResolver) containerInformation(container facts.Container, c *Container) (*Container, error) {
	containerMetrics := []string{
		"container_io_write_bytes",
		"container_io_read_bytes",
		"container_net_bits_recv",
		"container_net_bits_sent",
		"container_mem_used_perc",
		"container_cpu_used",
	}

	for _, m := range containerMetrics {
		metricFilters := map[string]string{
			types.LabelItem: container.ContainerName(),
			types.LabelName: m,
		}

		metrics, err := r.api.DB.Metrics(metricFilters)
		if err != nil {
			logger.V(2).Printf("Can not retrieve metrics: %v", err)
			return c, gqlerror.Errorf("Can not retrieve metrics")
		}

		if len(metrics) > 0 {
			points, err := metrics[0].Points(time.Now().UTC().Add(-15*time.Minute), time.Now().UTC())
			if err != nil {
				logger.V(2).Printf("Can not retrieve points: %v", err)
				return c, gqlerror.Errorf("Can not retrieve points")
			}

			var point float64

			if len(points) > 0 {
				point = points[len(points)-1].Value
			}

			switch m {
			case "container_io_write_bytes":
				c.IoWriteBytes = point
			case "container_io_read_bytes":
				c.IoReadBytes = point
			case "container_net_bits_recv":
				c.NetBitsRecv = point
			case "container_net_bits_sent":
				c.NetBitsSent = point
			case "container_mem_used_perc":
				c.MemUsedPerc = point
			case "container_cpu_used":
				c.CPUUsedPerc = point
			}
		}
	}

	return c, nil
}

// Processes returns a list of processes
// They can be filtered by container's ID.
func (r *queryResolver) Processes(ctx context.Context, containerID *string) (*Topinfo, error) {
	if r.api.PsFact == nil {
		return nil, gqlerror.Errorf("Can not retrieve processes at this moment. Please try later")
	}

	processes, updatedAt, err := r.api.PsFact.ProcessesWithTime(ctx, time.Second*15)
	if err != nil {
		logger.V(2).Printf("Can not retrieve processes: %v", err)
		return nil, gqlerror.Errorf("Can not retrieve processes")
	}

	processesRes := []*Process{}

	for _, process := range processes {
		if containerID == nil || *containerID == process.ContainerID {
			p := &Process{
				Pid:         process.PID,
				Ppid:        process.PPID,
				CreateTime:  process.CreateTime,
				Cmdline:     process.CmdLine,
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
	}

	return &Topinfo{UpdatedAt: updatedAt, Processes: processesRes}, nil
}

// Facts returns a list of facts discovered by agent.
func (r *queryResolver) Facts(ctx context.Context) ([]*Fact, error) {
	if r.api.FactProvider == nil {
		return nil, gqlerror.Errorf("Can not retrieve facts at this moment. Please try later")
	}

	facts, err := r.api.FactProvider.Facts(ctx, time.Hour)
	if err != nil {
		logger.V(2).Printf("Can not retrieve facts: %v", err)
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

// Services returns a list services discovered by agent
// They can be filtered by active flag.
func (r *queryResolver) Services(ctx context.Context, isActive bool) ([]*Service, error) {
	if r.api.Disccovery == nil {
		return nil, gqlerror.Errorf("Can not retrieve services at this moment. Please try later")
	}

	services, err := r.api.Disccovery.Discovery(ctx, time.Hour)
	if err != nil {
		logger.V(2).Printf("Can not retrieve facts: %v", err)
		return nil, gqlerror.Errorf("Can not retrieve facts")
	}

	servicesRes := []*Service{}

	for _, service := range services {
		if !isActive || service.Active {
			netAddrs := []string{}

			for _, addr := range service.ListenAddresses {
				netAddrs = append(netAddrs, addr.String())
			}

			s := &Service{
				Name:            service.Name,
				ContainerID:     service.ContainerID,
				IPAddress:       service.IPAddress,
				ListenAddresses: netAddrs,
				ExePath:         service.ExePath,
				Active:          service.Active,
			}

			metrics, err := r.api.DB.Metrics(map[string]string{types.LabelName: service.Name + "_status"})
			if err != nil {
				logger.V(2).Printf("Can not retrieve services: %v", err)
				return nil, gqlerror.Errorf("Can not retrieve services")
			}

			if len(metrics) > 0 {
				annotations := metrics[0].Annotations()
				if annotations.Status.CurrentStatus.IsSet() {
					s.Status = float64(annotations.Status.CurrentStatus.NagiosCode())
					s.StatusDescription = &annotations.Status.StatusDescription
				}
			}

			servicesRes = append(servicesRes, s)
		}
	}

	return servicesRes, nil
}

// AgentInformation returns some informations about agent registration to Bleemeo Cloud.
func (r *queryResolver) AgentInformation(ctx context.Context) (*AgentInfo, error) {
	if r.api.AgentInfo == nil {
		return nil, gqlerror.Errorf("Can not retrieve agent information at this moment. Please try later")
	}

	registrationAt := r.api.AgentInfo.BleemeoRegistrationAt()
	lastReport := r.api.AgentInfo.BleemeoLastReport()
	connected := r.api.AgentInfo.BleemeoConnected()
	agentInfo := &AgentInfo{
		RegistrationAt: &registrationAt,
		LastReport:     &lastReport,
		IsConnected:    connected,
	}

	return agentInfo, nil
}

// Tags returns a list of tags from system.
func (r *queryResolver) Tags(ctx context.Context) ([]*Tag, error) {
	if r.api.AgentInfo == nil {
		return nil, gqlerror.Errorf("Can not retrieve tags at this moment. Please try later")
	}

	tags := r.api.AgentInfo.Tags()
	tagsResult := []*Tag{}

	for _, tag := range tags {
		t := &Tag{
			TagName: tag,
		}
		tagsResult = append(tagsResult, t)
	}

	return tagsResult, nil
}

// AgentStatus returns an integer that represent global server status over several metrics.
func (r *queryResolver) AgentStatus(ctx context.Context) (*AgentStatus, error) {
	if r.api.DB == nil {
		return nil, gqlerror.Errorf("Can not retrieve agent status at this moment. Please try later")
	}

	metrics, err := r.api.DB.Metrics(map[string]string{})
	if err != nil {
		logger.V(2).Printf("Can not retrieve metrics: %v", err)
		return nil, gqlerror.Errorf("Can not retrieve metrics from agent status")
	}

	statuses := []float64{}
	statusDescription := []string{}

	for _, metric := range metrics {
		status := metric.Annotations().Status
		if !status.CurrentStatus.IsSet() {
			continue
		}

		statuses = append(statuses, float64(status.CurrentStatus.NagiosCode()))

		if status.CurrentStatus != types.StatusOk {
			statusDescription = append(statusDescription, status.StatusDescription)
		}
	}

	var finalStatus float64

	for _, status := range statuses {
		finalStatus = math.Max(status, finalStatus)
	}

	return &AgentStatus{Status: finalStatus, StatusDescription: statusDescription}, nil
}
