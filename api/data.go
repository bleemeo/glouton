// Copyright 2015-2025 Bleemeo
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

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	"github.com/go-chi/render"
)

type Data struct {
	api *API
}

var (
	errContainerRuntime = errors.New("container runtime is not available")
	errContainer        = errors.New("can not retrieve containers")
	errProcesses        = errors.New("can not retrieve processes")
	errFacts            = errors.New("can not retrieve facts")
	errServices         = errors.New("can not retrieve services")
	errAgentInformation = errors.New("can not retrieve agent information")
	errTags             = errors.New("can not retrieve tags")
	errAgentStatus      = errors.New("can not retrieve agent status")
	errMetrics          = errors.New("can not retrieve metrics")
	errPoints           = errors.New("can not retrieve points")
)

// Containers have 4 query parameters :
// - offset (string) : The offset of the first element to return.
// - limit (string) : The maximum number of elements to return.
// - allContainers (bool) : If true, return all containers, otherwise only running containers.
// - search (string) : The search string to filter containers.
func (d *Data) Containers(w http.ResponseWriter, r *http.Request) {
	offset, limit := 0, -1

	if offsetParam := r.URL.Query().Get("offset"); offsetParam != "" {
		offsetValue, err := strconv.Atoi(offsetParam)
		if err == nil {
			offset = offsetValue
		}
	}

	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		limitValue, err := strconv.Atoi(limitParam)
		if err == nil {
			limit = limitValue
		}
	}

	allContainers, err := strconv.ParseBool(r.URL.Query().Get("allContainers"))
	if err != nil {
		allContainers = false
	}

	var search string
	if searchParam := r.URL.Query().Get("search"); searchParam != "" {
		search = searchParam
	}

	if d.api.ContainerRuntime == nil {
		logger.V(2).Printf("Container runtime is not available")

		err := render.Render(w, r, ErrInternalServerError(errContainerRuntime))
		if err != nil {
			logger.V(2).Printf("Can not render error: %v", err)
		}

		return
	}

	containers, err := d.api.ContainerRuntime.Containers(r.Context(), time.Hour, false)
	if err != nil {
		logger.V(2).Printf("Can not retrieve containers: %v", err)

		err := render.Render(w, r, ErrInternalServerError(errContainer))
		if err != nil {
			logger.V(2).Printf("Can not render error: %v", err)
		}

		return
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

			c, err = d.containerInformation(container, c)
			if err != nil {
				err := render.Render(w, r, ErrInternalServerError(err))
				if err != nil {
					logger.V(2).Printf("Can not render error: %v", err)
				}

				return
			}

			containersRes = append(containersRes, c)
		}
	}

	if limit == -1 {
		limit = len(containersRes)
	}

	pagination := Pagination{
		Offset: offset,
		Limit:  limit,
	}

	containersRes = paginateInformation(&pagination, containersRes)

	err = render.Render(w, r, &Containers{Containers: containersRes, Count: nbContainers, CurrentCount: nbCurrentContainers})
	if err != nil {
		logger.V(2).Printf("Can not render error: %v", err)
	}
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

func (d *Data) containerInformation(container facts.Container, c *Container) (*Container, error) {
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

		metrics, err := d.api.DB.Metrics(metricFilters)
		if err != nil {
			logger.V(2).Printf("Can not retrieve metrics: %v", err)

			return c, errMetrics
		}

		if len(metrics) > 0 {
			points, err := metrics[0].Points(time.Now().UTC().Add(-15*time.Minute), time.Now().UTC())
			if err != nil {
				logger.V(2).Printf("Can not retrieve points: %v", err)

				return c, errPoints
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

// Processes returns a list of topInfo, they have 1 query parameter :
// - search (string) : The container ID to filter processes.
func (d *Data) Processes(w http.ResponseWriter, r *http.Request) {
	var containerID *string
	if containerIDParam := r.URL.Query().Get("search"); containerIDParam != "" {
		containerID = &containerIDParam
	}

	if d.api.PsFact == nil {
		err := render.Render(w, r, ErrInternalServerError(errProcesses))
		if err != nil {
			logger.V(2).Printf("Can not render error: %v", err)
		}

		return
	}

	topInfo := d.api.PsFact.TopInfo()
	processes := topInfo.Processes
	processesRes := []*Process{}

	for _, process := range processes {
		if containerID == nil || *containerID == process.ContainerID {
			p := &Process{
				Pid:         process.PID,
				Ppid:        process.PPID,
				CreateTime:  process.CreateTime,
				Cmdline:     process.CmdLine,
				Name:        process.Name,
				MemoryRss:   int64(process.MemoryRSS), //nolint:gosec
				CPUPercent:  process.CPUPercent,
				CPUTime:     process.CPUTime,
				Status:      string(process.Status),
				Username:    process.Username,
				Executable:  process.Executable,
				ContainerID: process.ContainerID,
			}
			processesRes = append(processesRes, p)
		}
	}

	cpuRes := &CPUUsage{
		Nice:      topInfo.CPU.Nice,
		System:    topInfo.CPU.System,
		User:      topInfo.CPU.User,
		Idle:      topInfo.CPU.Idle,
		IOWait:    topInfo.CPU.IOWait,
		Guest:     topInfo.CPU.Guest,
		GuestNice: topInfo.CPU.GuestNice,
		Irq:       topInfo.CPU.IRQ,
		SoftIrq:   topInfo.CPU.SoftIRQ,
		Steal:     topInfo.CPU.Steal,
	}
	memoryRes := &MemoryUsage{
		Total:   topInfo.Memory.Total,
		Used:    topInfo.Memory.Used,
		Free:    topInfo.Memory.Free,
		Buffers: topInfo.Memory.Buffers,
		Cached:  topInfo.Memory.Cached,
	}
	swapRes := &SwapUsage{
		Total: topInfo.Swap.Total,
		Free:  topInfo.Swap.Free,
		Used:  topInfo.Swap.Used,
	}

	err := render.Render(w, r, &Topinfo{
		Time: time.Unix(topInfo.Time, topInfo.Time), Uptime: topInfo.Uptime, Loads: topInfo.Loads, Users: topInfo.Users,
		CPU: cpuRes, Memory: memoryRes, Swap: swapRes, Processes: processesRes,
	})
	if err != nil {
		logger.V(2).Printf("Can not render error: %v", err)
	}
}

// Facts returns a list of facts discovered by agent.
func (d *Data) Facts(w http.ResponseWriter, r *http.Request) {
	if d.api.FactProvider == nil {
		err := render.Render(w, r, ErrInternalServerError(errFacts))
		if err != nil {
			logger.V(2).Printf("Can not render error: %v", err)
		}

		return
	}

	facts, err := d.api.FactProvider.Facts(r.Context(), time.Hour)
	if err != nil {
		logger.V(2).Printf("Can not retrieve facts: %v", err)

		err := render.Render(w, r, ErrInternalServerError(errFacts))
		if err != nil {
			logger.V(2).Printf("Can not render error: %v", err)
		}

		return
	}

	factsRes := []render.Renderer{}

	for k, v := range facts {
		f := &Fact{
			Name:  k,
			Value: v,
		}
		factsRes = append(factsRes, f)
	}

	err = render.RenderList(w, r, factsRes)
	if err != nil {
		logger.V(2).Printf("Can not render error: %v", err)
	}
}

// Services returns a list services discovered by agent
// They can be filtered by active flag.
func (d *Data) Services(w http.ResponseWriter, r *http.Request) {
	var isActive bool

	isActiveParam := r.URL.Query().Get("isActive")
	if isActiveParam == "" {
		isActive = true
	}

	isActiveParamValue, err := strconv.ParseBool(isActiveParam)
	if err == nil {
		isActive = isActiveParamValue
	}

	if d.api.Discovery == nil {
		err := render.Render(w, r, ErrInternalServerError(errServices))
		if err != nil {
			logger.V(2).Printf("Can not render error: %v", err)
		}

		return
	}

	services, _ := d.api.Discovery.GetLatestDiscovery()
	servicesRes := []render.Renderer{}

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

			metrics, err := d.api.DB.Metrics(map[string]string{types.LabelName: types.MetricServiceStatus, types.LabelService: service.Name, types.LabelServiceInstance: service.Instance})
			if err != nil {
				logger.V(2).Printf("Can not retrieve services: %v", err)

				err := render.Render(w, r, ErrInternalServerError(errServices))
				if err != nil {
					logger.V(2).Printf("Can not render error: %v", err)
				}

				return
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

	err = render.RenderList(w, r, servicesRes)
	if err != nil {
		logger.V(2).Printf("Can not render error: %v", err)
	}
}

// AgentInformation returns some information about agent registration to Bleemeo Cloud.
func (d *Data) AgentInformation(w http.ResponseWriter, r *http.Request) {
	if d.api.AgentInfo == nil {
		err := render.Render(w, r, ErrInternalServerError(errAgentInformation))
		if err != nil {
			logger.V(2).Printf("Can not render error: %v", err)
		}

		return
	}

	registrationAt := d.api.AgentInfo.BleemeoRegistrationAt()
	lastReport := d.api.AgentInfo.BleemeoLastReport()
	connected := d.api.AgentInfo.BleemeoConnected()
	agentInfo := &AgentInfo{
		RegistrationAt: &registrationAt,
		LastReport:     &lastReport,
		IsConnected:    connected,
	}

	err := render.Render(w, r, agentInfo)
	if err != nil {
		logger.V(2).Printf("Can not render error: %v", err)
	}
}

// Tags returns a list of tags from system.
func (d *Data) Tags(w http.ResponseWriter, r *http.Request) {
	if d.api.AgentInfo == nil {
		err := render.Render(w, r, ErrInternalServerError(errTags))
		if err != nil {
			logger.V(2).Printf("Can not render error: %v", err)
		}

		return
	}

	tags := d.api.AgentInfo.Tags()
	tagsResult := []render.Renderer{}

	for _, tag := range tags {
		t := &Tag{
			TagName: tag,
		}
		tagsResult = append(tagsResult, t)
	}

	err := render.RenderList(w, r, tagsResult)
	if err != nil {
		logger.V(2).Printf("Can not render error: %v", err)
	}
}

// AgentStatus returns an integer that represent global server status over several metrics.
func (d *Data) AgentStatus(w http.ResponseWriter, r *http.Request) {
	if d.api.DB == nil {
		err := render.Render(w, r, ErrInternalServerError(errAgentStatus))
		if err != nil {
			logger.V(2).Printf("Can not render error: %v", err)
		}

		return
	}

	metrics, err := d.api.DB.Metrics(map[string]string{})
	if err != nil {
		logger.V(2).Printf("Can not retrieve metrics: %v", err)

		err := render.Render(w, r, ErrInternalServerError(errAgentStatus))
		if err != nil {
			logger.V(2).Printf("Can not render error: %v", err)
		}

		return
	}

	agentStatus := []render.Renderer{}

	for _, metric := range metrics {
		status := metric.Annotations().Status
		if !status.CurrentStatus.IsSet() {
			continue
		}

		name := metric.Annotations().ServiceName
		a := &AgentStatus{
			ServiceName:       name,
			Status:            float64(status.CurrentStatus.NagiosCode()),
			StatusDescription: status.StatusDescription,
		}

		agentStatus = append(agentStatus, a)
	}

	err = render.RenderList(w, r, agentStatus)
	if err != nil {
		logger.V(2).Printf("Can not render error: %v", err)
	}
}

func (d *Data) Logs(w http.ResponseWriter, _ *http.Request) {
	buffer := logger.BufferCurrLevel()

	_, err := w.Write(buffer)
	if err != nil {
		logger.V(2).Printf("Can not write logs: %v", err)
	}
}

type ErrResponse struct {
	Err            error `json:"-"` // low-level runtime error
	HTTPStatusCode int   `json:"-"` // http response status code

	StatusText string `json:"status"`          // user-level status message
	AppCode    int64  `json:"code,omitempty"`  // application-specific error code
	ErrorText  string `json:"error,omitempty"` // application-level error message, for debugging
}

func (e *ErrResponse) Render(_ http.ResponseWriter, r *http.Request) error {
	render.Status(r, e.HTTPStatusCode)

	return nil
}

func ErrInternalServerError(err error) render.Renderer {
	return &ErrResponse{
		Err:            err,
		HTTPStatusCode: 500,
		StatusText:     "Internal Server Error",
		ErrorText:      err.Error(),
	}
}
