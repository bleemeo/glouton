package api

import (
	"errors"
	"math"
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

func (d *Data) Containers(w http.ResponseWriter, r *http.Request) {

	// 4 query parameters :
	// - offset (string) : The offset of the first element to return
	// - limit (string) : The maximum number of elements to return
	// - allContainers (bool) : If true, return all containers, otherwise only running containers
	// - search (string) : The search string to filter containers

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
		cerr := errors.New("Container runtime is not available")
		render.Render(w, r, ErrInternalServerError(cerr))
		return
	}

	containers, err := d.api.ContainerRuntime.Containers(r.Context(), time.Hour, false)
	if err != nil {
		logger.V(2).Printf("Can not retrieve containers: %v", err)
		cerr := errors.New("Can not retrieve containers")
		render.Render(w, r, ErrInternalServerError(cerr))
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
				render.Render(w, r, ErrInternalServerError(err))
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

	render.Render(w, r, &Containers{Containers: containersRes, Count: nbContainers, CurrentCount: nbCurrentContainers})
}

func (d *Data) paginateInformation(input *Pagination, containersRes []*Container) []*Container {
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

		if d.api.MetricFormat == types.MetricFormatPrometheus {
			metricFilters = map[string]string{
				types.LabelContainerName: container.ContainerName(),
				types.LabelName:          m,
			}
		}

		metrics, err := d.api.DB.Metrics(metricFilters)
		if err != nil {
			logger.V(2).Printf("Can not retrieve metrics: %v", err)

			return c, errors.New("Can not retrieve metrics")
		}

		if len(metrics) > 0 {
			points, err := metrics[0].Points(time.Now().UTC().Add(-15*time.Minute), time.Now().UTC())
			if err != nil {
				logger.V(2).Printf("Can not retrieve points: %v", err)

				return c, errors.New("Can not retrieve points")
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

// Processes returns a list of topInfo
// They can be filtered by container's ID.
func (d *Data) Processes(w http.ResponseWriter, r *http.Request) {

	// 1 query parameter :
	// - search (string) : The search string to filter processes

	var containerID *string
	if containerIDParam := r.URL.Query().Get("search"); containerIDParam == "" {
		containerID = &containerIDParam
	}

	if d.api.PsFact == nil {
		cerr := errors.New("Can not retrieve processes at this moment. Please try later")
		render.Render(w, r, ErrInternalServerError(cerr))
		return
	}

	topInfo, err := d.api.PsFact.TopInfo(r.Context(), time.Second*15)
	if err != nil {
		logger.V(2).Printf("Can not retrieve processes: %v", err)
		cerr := errors.New("Can not retrieve processes")
		render.Render(w, r, ErrInternalServerError(cerr))
		return
	}

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
				MemoryRss:   int(process.MemoryRSS),
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

	render.Render(w, r, &Topinfo{
		Time: time.Unix(topInfo.Time, topInfo.Time), Uptime: topInfo.Uptime, Loads: topInfo.Loads, Users: topInfo.Users,
		CPU: cpuRes, Memory: memoryRes, Swap: swapRes, Processes: processesRes,
	})
}

// Facts returns a list of facts discovered by agent.
func (d *Data) Facts(w http.ResponseWriter, r *http.Request) {

	if d.api.FactProvider == nil {
		cerr := errors.New("Can not retrieve facts at this moment. Please try later")
		render.Render(w, r, ErrInternalServerError(cerr))
		return
	}

	facts, err := d.api.FactProvider.Facts(r.Context(), time.Hour)
	if err != nil {
		logger.V(2).Printf("Can not retrieve facts: %v", err)
		cerr := errors.New("Can not retrieve facts")
		render.Render(w, r, ErrInternalServerError(cerr))
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

	render.RenderList(w, r, factsRes)
}

// Services returns a list services discovered by agent
// They can be filtered by active flag.
func (d *Data) Services(w http.ResponseWriter, r *http.Request) {

	var isActive bool
	if isActiveParam := r.URL.Query().Get("isActive"); isActiveParam == "" {
		isActiveParamValue, err := strconv.ParseBool(isActiveParam)
		if err == nil {
			isActive = isActiveParamValue
		}
	}

	if d.api.Discovery == nil {
		cerr := errors.New("Can not retrieve services at this moment. Please try later")
		render.Render(w, r, ErrInternalServerError(cerr))
		return
	}

	services, err := d.api.Discovery.Discovery(r.Context(), time.Hour)
	if err != nil {
		logger.V(2).Printf("Can not retrieve facts: %v", err)

		cerr := errors.New("Can not retrieve facts")
		render.Render(w, r, ErrInternalServerError(cerr))
		return
	}

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

				cerr := errors.New("Can not retrieve services")
				render.Render(w, r, ErrInternalServerError(cerr))
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

	render.RenderList(w, r, servicesRes)
}

// AgentInformation returns some informations about agent registration to Bleemeo Cloud.
func (d *Data) AgentInformation(w http.ResponseWriter, r *http.Request) {
	if d.api.AgentInfo == nil {
		cerr := errors.New("Can not retrieve agent information at this moment. Please try later")
		render.Render(w, r, ErrInternalServerError(cerr))
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

	render.Render(w, r, agentInfo)
}

// Tags returns a list of tags from system.
func (d *Data) Tags(w http.ResponseWriter, r *http.Request) {
	if d.api.AgentInfo == nil {
		cerr := errors.New("Can not retrieve tags at this moment. Please try later")
		render.Render(w, r, ErrInternalServerError(cerr))
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

	render.RenderList(w, r, tagsResult)
}

// AgentStatus returns an integer that represent global server status over several metrics.
func (d *Data) AgentStatus(w http.ResponseWriter, r *http.Request) {
	if d.api.DB == nil {
		cerr := errors.New("Can not retrieve agent status at this moment. Please try later")
		render.Render(w, r, ErrInternalServerError(cerr))
		return
	}

	metrics, err := d.api.DB.Metrics(map[string]string{})
	if err != nil {
		logger.V(2).Printf("Can not retrieve metrics: %v", err)

		cerr := errors.New("Can not retrieve metrics from agent status")
		render.Render(w, r, ErrInternalServerError(cerr))
		return
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

	render.Render(w, r, &AgentStatus{Status: finalStatus, StatusDescription: statusDescription})
}

type ErrResponse struct {
	Err            error `json:"-"` // low-level runtime error
	HTTPStatusCode int   `json:"-"` // http response status code

	StatusText string `json:"status"`          // user-level status message
	AppCode    int64  `json:"code,omitempty"`  // application-specific error code
	ErrorText  string `json:"error,omitempty"` // application-level error message, for debugging
}

func (e *ErrResponse) Render(w http.ResponseWriter, r *http.Request) error {
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
