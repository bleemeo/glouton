package containerd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"glouton/facts"
	"glouton/logger"
	"glouton/types"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	v1 "github.com/containerd/cgroups/stats/v1"
	"github.com/containerd/containerd"
	pbEvents "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/typeurl"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/process"
)

var errNotFound = errors.New("not found")

// DefaultAddresses returns default address for the Docker socket. If hostroot is set (and not "/") ALSO add
// socket path prefixed by hostRoot.
func DefaultAddresses(hostRoot string) []string {
	list := []string{"/run/containerd/containerd.sock"}
	if hostRoot != "" && hostRoot != "/" {
		list = append(list, filepath.Join(hostRoot, "run/containerd/containerd.sock"))
	}

	return list
}

// Containerd implement connector to containerd.
type Containerd struct {
	Addresses                 []string
	DeletedContainersCallback func(containersID []string)

	l                sync.Mutex
	openConnection   func(ctx context.Context, address string) (cl containerdClient, err error)
	client           containerdClient
	lastUpdate       time.Time
	containers       map[string]containerObject
	ignoredID        map[string]bool
	notifyC          chan facts.ContainerEvent
	pastMetricValues []metricValue
}

// ignore "moby" namespace. It contains container managed by Docker, for which
// Docker connector must be used.
// Thought containerd we will be missing important information like name and IP address.
const ignoredNamespace = "moby"

// LastUpdate return the last time containers list was updated.
func (c *Containerd) LastUpdate() time.Time {
	c.l.Lock()
	defer c.l.Unlock()

	return c.lastUpdate
}

// RuntimeFact will return facts from the ContainerD runtime, like containerd_version.
func (c *Containerd) RuntimeFact(ctx context.Context, currentFact map[string]string) map[string]string {
	c.l.Lock()
	defer c.l.Unlock()

	cl, err := c.getClient(ctx)
	if err != nil {
		return nil
	}

	version, err := cl.Version(ctx)
	if err != nil {
		if c.client != nil {
			_ = c.client.Close()
		}

		c.client = nil

		return nil
	}

	return map[string]string{
		"containerd_version": version.Version,
		"container_runtime":  "containerd",
	}
}

// Metrics return metrics in a format similar to the one returned by Telegraf docker input.
// Note that Metrics will never open the connection to ContainerD and will return empty points if not connected.
//nolint:gocyclo,cyclop
func (c *Containerd) Metrics(ctx context.Context) ([]types.MetricPoint, error) {
	now := time.Now()

	c.l.Lock()

	cl := c.client

	c.l.Unlock()

	if cl == nil {
		return nil, nil
	}

	// ensure information isn't too much out-dated
	_, err := c.Containers(ctx, 10*time.Minute, false)
	if err != nil {
		return nil, err
	}

	idPerNamespace := make(map[string][]string)
	gloutonIDToName := make(map[string]string)

	c.l.Lock()

	for _, cont := range c.containers {
		if !facts.ContainerIgnored(cont) {
			idPerNamespace[cont.namespace] = append(idPerNamespace[cont.namespace], "id=="+cont.info.ID)

			gloutonIDToName[cont.ID()] = cont.ContainerName()
		}
	}

	c.l.Unlock()

	newValues := make([]metricValue, 0, len(gloutonIDToName))

	for ns, ids := range idPerNamespace {
		ctx := namespaces.WithNamespace(ctx, ns)

		r, err := cl.Metrics(ctx, ids)
		if err != nil {
			return nil, err
		}

		for _, metric := range r.Metrics {
			if metric == nil || metric.Data == nil {
				continue
			}

			data, err := typeurl.UnmarshalAny(metric.Data)
			if err != nil {
				logger.V(2).Printf("unable to unmarshal metrics value: %v", err)

				continue
			}

			value, ok := data.(*v1.Metrics)
			if !ok {
				logger.V(2).Printf("unexpected type for metric: %s", metric.Data.TypeUrl)

				continue
			}

			valueMap := make(map[string]uint64)

			for _, row := range value.Network {
				if row != nil {
					valueMap["container_net_bits_sent"] += row.TxBytes * 8
					valueMap["container_net_bits_recv"] += row.RxBytes * 8
				}
			}

			if value.CPU != nil && value.CPU.Usage != nil {
				valueMap["container_cpu_used"] = value.CPU.Usage.Total
			}

			if value.Blkio != nil {
				for _, row := range value.Blkio.IoServiceBytesRecursive {
					if row != nil && row.Op == "Read" {
						valueMap["container_io_read_bytes"] += row.Value
					} else if row != nil && row.Op == "Write" {
						valueMap["container_io_write_bytes"] += row.Value
					}
				}
			}

			if value.Memory != nil && value.Memory.Usage != nil {
				valueMap["container_mem_used"] = value.Memory.Usage.Usage
				valueMap["container_mem_limit"] = value.Memory.Usage.Limit
			}

			newValues = append(newValues, metricValue{
				ContainerNamespace: ns,
				ContainerID:        metric.ID,
				Time:               now,
				values:             valueMap,
			})
		}
	}

	c.l.Lock()
	defer c.l.Unlock()

	points := rateFromMetricValue(gloutonIDToName, c.pastMetricValues, newValues)
	c.pastMetricValues = newValues

	return points, nil
}

// CachedContainer return a container without querying ContainerD, it use in-memory cache which must have been filled by a call to Continers().
func (c *Containerd) CachedContainer(containerID string) (cont facts.Container, found bool) {
	c.l.Lock()
	defer c.l.Unlock()

	cont, found = c.containers[containerID]

	return cont, found
}

// Containers return ContainerD containers.
func (c *Containerd) Containers(ctx context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, err error) {
	c.l.Lock()
	defer c.l.Unlock()

	if time.Since(c.lastUpdate) >= maxAge {
		err = c.updateContainers(ctx)
		if err != nil {
			return nil, err
		}
	}

	containers = make([]facts.Container, 0, len(c.containers))
	for _, c := range c.containers {
		if includeIgnored || !facts.ContainerIgnored(c) {
			containers = append(containers, c)
		}
	}

	return
}

// IsRuntimeRunning returns whether or not Containerd is available
//
// IsRuntimeRunning will try to open a new connection if it never tried. It will also check that connection is still working (do a ping).
// Note: if ContainerD is running but Glouton can't access it, IsRuntimeRunning will return false.
func (c *Containerd) IsRuntimeRunning(ctx context.Context) bool {
	c.l.Lock()
	defer c.l.Unlock()

	_, err := c.getClient(ctx)
	if err != nil {
		return false
	}

	_, err = c.client.Version(ctx)
	if err != nil {
		if c.client != nil {
			_ = c.client.Close()
		}

		c.client = nil

		return false
	}

	return true
}

// Exec run a command in a container and return stdout+stderr.
func (c *Containerd) Exec(ctx context.Context, containerID string, cmd []string) ([]byte, error) {
	c.l.Lock()
	cl, err := c.getClient(ctx)

	cont, found := c.containers[containerID]

	c.l.Unlock()

	if err != nil {
		return nil, err
	}

	if !found {
		return nil, fmt.Errorf("container %w: %s", errNotFound, containerID)
	}

	// general worklow for exec is inspired by ctr tasks exec command.

	ctx = namespaces.WithNamespace(ctx, cont.namespace)

	container, err := cl.LoadContainer(ctx, cont.info.ID)
	if err != nil {
		return nil, err
	}

	task, err := container.Task(ctx, nil)
	if err != nil {
		return nil, err
	}

	var pspec specs.Process

	if cont.info.Spec.Process != nil {
		pspec = *cont.info.Spec.Process
	}

	pspec.Terminal = false
	pspec.Args = cmd

	buffer := bytes.NewBuffer(nil)

	ioCreator := cio.NewCreator(
		cio.WithStreams(nil, buffer, buffer),
	)

	process, err := task.Exec(ctx, "glouton-exec-id", &pspec, ioCreator)
	if err != nil {
		return nil, err
	}

	statusC, err := process.Wait(ctx)
	if err != nil {
		return nil, err
	}

	if err := process.Start(ctx); err != nil {
		return nil, err
	}

	var status containerd.ExitStatus

	select {
	case status = <-statusC:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	_, _, err = status.Result()
	if err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

// Events return the channel used to send events. There is only one shared channel (so
// multiple consumer should be implemented by caller).
func (c *Containerd) Events() <-chan facts.ContainerEvent {
	c.l.Lock()
	defer c.l.Unlock()

	if c.notifyC == nil {
		c.notifyC = make(chan facts.ContainerEvent)
	}

	return c.notifyC
}

// ProcessWithCache facts.containerRuntime.
func (c *Containerd) ProcessWithCache() facts.ContainerRuntimeProcessQuerier {
	return &containerdProcessQuerier{
		c:             c,
		pid2container: make(map[int]containerObject),
	}
}

// Run will run connect and listen to ContainerD event until context is cancelled
//
// Any error (unable to connect due to permission issue or ContainerD down) are not returned
// by Run but could be retrieved with LastError.
func (c *Containerd) Run(ctx context.Context) error {
	var (
		lastErrorNotify  time.Time
		sleepDelay       float64
		reconnectAttempt int
	)

	notifyError := func(err error) {
		if time.Since(lastErrorNotify) < time.Hour && reconnectAttempt > 1 {
			return
		}

		if strings.Contains(fmt.Sprintf("%v", err), "permission denied") {
			logger.Printf(
				"The agent is not permitted to access ContainerD, the ContainerD integration will be disabled.",
			)
		} else if isContainerdRunning() {
			logger.Printf("Unable to contact ContainerD: %v", err)
		}

		lastErrorNotify = time.Now()
	}

	// This will initialize d.notifyC
	c.Events()

	for {
		err := c.run(ctx)

		c.l.Lock()

		reconnectAttempt++

		if err != nil {
			notifyError(err)
		}

		sleepDelay = 5 * math.Pow(2, float64(reconnectAttempt))
		if sleepDelay > 60 {
			sleepDelay = 60
		}

		c.l.Unlock()

		select {
		case <-time.After(time.Duration(sleepDelay) * time.Second):
		case <-ctx.Done():
			close(c.notifyC)

			c.l.Lock()

			if c.client != nil {
				_ = c.client.Close()
				c.client = nil
			}

			c.l.Unlock()

			return nil
		}
	}
}

// ContainerLastKill return the last time a container was killed or zero-time if unknown.
// containerd does not provide this information.
func (c *Containerd) ContainerLastKill(containerID string) time.Time {
	return time.Time{}
}

//nolint:gocyclo,cyclop
func (c *Containerd) run(ctx context.Context) error {
	c.l.Lock()

	cl, err := c.getClient(ctx)

	c.l.Unlock()

	if err != nil {
		return err
	}

	// Make sure information is recent enough
	_, _ = c.Containers(ctx, time.Minute, false)

	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()

	eventC, errC := cl.Events(ctx2)

	for {
		select {
		case event, ok := <-eventC:
			if !ok {
				return ctx.Err()
			}

			if !strings.HasPrefix(event.Topic, "/containers/") && !strings.HasPrefix(event.Topic, "/tasks/") {
				continue
			}

			result, err := typeurl.UnmarshalAny(event.Event)
			if err != nil {
				logger.V(2).Printf("unable to decode event payload: %v", err)

				continue
			}

			if event.Namespace == ignoredNamespace {
				continue
			}

			gloutonEvent := facts.ContainerEvent{}

			switch value := result.(type) {
			case *pbEvents.ContainerCreate:
				gloutonEvent.ContainerID = fmt.Sprintf("%s/%s", event.Namespace, value.ID)
				gloutonEvent.Type = facts.EventTypeCreate
			case *pbEvents.ContainerDelete:
				gloutonEvent.ContainerID = fmt.Sprintf("%s/%s", event.Namespace, value.ID)
				gloutonEvent.Type = facts.EventTypeDelete
			case *pbEvents.TaskStart:
				gloutonEvent.ContainerID = fmt.Sprintf("%s/%s", event.Namespace, value.ContainerID)
				gloutonEvent.Type = facts.EventTypeStart
			case *pbEvents.TaskExit:
				gloutonEvent.ContainerID = fmt.Sprintf("%s/%s", event.Namespace, value.ContainerID)
				gloutonEvent.Type = facts.EventTypeStop
			default:
				continue
			}

			c.l.Lock()

			container, found := c.containers[gloutonEvent.ContainerID]
			if found {
				gloutonEvent.Container = container
			}

			switch gloutonEvent.Type { //nolint:exhaustive
			case facts.EventTypeDelete:
				delete(c.containers, gloutonEvent.ContainerID)
			case facts.EventTypeStop:
				if found {
					container.state = string(containerd.Stopped)
					c.containers[gloutonEvent.ContainerID] = container
				}
			}

			c.l.Unlock()

			select {
			case c.notifyC <- gloutonEvent:
			case <-ctx.Done():
			}

		case err = <-errC:
			return fmt.Errorf("Events() failed: %w", err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Containerd) updateContainers(ctx context.Context) error {
	cl, err := c.getClient(ctx)
	if err != nil {
		return err
	}

	nsList, err := cl.Namespaces(ctx)
	if err != nil {
		return fmt.Errorf("listing namespaces failed: %w", err)
	}

	containers := make(map[string]containerObject)
	ignoredID := make(map[string]bool)

	for _, ns := range nsList {
		if ns == ignoredNamespace {
			continue
		}

		ctx := namespaces.WithNamespace(ctx, ns)

		err := addContainersInfo(ctx, containers, cl, ns, ignoredID)
		if err != nil {
			return err
		}
	}

	var deletedContainerID []string

	for k := range c.containers {
		if _, ok := containers[k]; !ok {
			deletedContainerID = append(deletedContainerID, k)
		}
	}

	if len(deletedContainerID) > 0 && c.DeletedContainersCallback != nil {
		c.DeletedContainersCallback(deletedContainerID)
	}

	c.lastUpdate = time.Now()
	c.containers = containers
	c.ignoredID = ignoredID

	return nil
}

func addContainersInfo(ctx context.Context, containers map[string]containerObject, cl containerdClient, ns string, ignoredID map[string]bool) error {
	list, err := cl.Containers(ctx)
	if err != nil {
		return fmt.Errorf("listing containers failed: %w", err)
	}

	for _, cont := range list {
		info, err := cont.Info(ctx, containerd.WithoutRefreshedMetadata)
		if err != nil {
			return fmt.Errorf("Info() on %s/%s failed: %w", ns, cont.ID(), err)
		}

		if info.Spec == nil {
			logger.V(2).Printf("container %s/%s has no spec", ns, cont.ID())

			continue
		}

		img, err := cont.Image(ctx)
		if err != nil {
			return fmt.Errorf("Image() on %s/%s failed: %w", ns, cont.ID(), err)
		}

		var spec oci.Spec

		err = json.Unmarshal(info.Spec.Value, &spec)
		if err != nil {
			logger.V(2).Printf("unable to decode container %s/%s spec (type=%s): %v", ns, cont.ID(), info.Spec.TypeUrl, err)

			continue
		}

		obj := containerObject{
			namespace: ns,
			info: ContainerOCISpec{
				Container: info,
				Spec:      &spec,
			},
			state:   string(containerd.Unknown),
			imageID: img.Target().Digest.String(),
		}

		task, err := cont.Task(ctx, nil)
		if err == nil {
			obj.pid = int(task.Pid())

			status, err := task.Status(ctx)
			if err == nil {
				obj.state = string(status.Status)
				obj.exitTime = status.ExitTime
			}
		}

		if spec.Process != nil {
			obj.args = spec.Process.Args
		}

		containers[obj.ID()] = obj

		if facts.ContainerIgnored(obj) {
			ignoredID[obj.ID()] = true
		}
	}

	return nil
}

func (c *Containerd) getClient(ctx context.Context) (containerdClient, error) {
	if c.openConnection == nil {
		c.openConnection = openConnection
	}

	if c.client == nil {
		var firstErr error

		if len(c.Addresses) == 0 {
			c.Addresses = DefaultAddresses("")
		}

		for _, addr := range c.Addresses {
			cl, err := c.openConnection(ctx, addr)
			if err != nil {
				logger.V(2).Printf("ContainerD openConnection on %s failed: %v", addr, err)

				if firstErr == nil {
					firstErr = err
				}

				continue
			}

			_, err = cl.Version(ctx)
			if err != nil {
				if firstErr == nil {
					logger.V(2).Printf("ContainerD openConnection on %s failed: %v", addr, err)

					firstErr = err
				}

				continue
			}

			c.client = cl

			break
		}

		if c.client == nil {
			return nil, firstErr
		}
	}

	return c.client, nil
}

type containerdClient interface {
	LoadContainer(ctx context.Context, id string) (containerd.Container, error)
	Containers(ctx context.Context) ([]containerd.Container, error)
	Version(ctx context.Context) (containerd.Version, error)
	Namespaces(ctx context.Context) ([]string, error)
	Events(ctx context.Context) (ch <-chan *events.Envelope, errs <-chan error)
	Metrics(ctx context.Context, filters []string) (*tasks.MetricsResponse, error)
	Close() error
}

func openConnection(ctx context.Context, address string) (containerdClient, error) {
	if address != "" && (address[0] == '/' || address[1] == '\\') {
		_, err := os.Stat(address)
		if err != nil {
			return nil, fmt.Errorf("unable to access socket %s: %w", address, err)
		}
	}

	client, err := containerd.New(address)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to containerd at address %s: %w", address, err)
	}

	return realClient{client: client}, nil
}

type realClient struct {
	client *containerd.Client
}

func (cl realClient) Containers(ctx context.Context) ([]containerd.Container, error) {
	return cl.client.Containers(ctx)
}

func (cl realClient) LoadContainer(ctx context.Context, id string) (containerd.Container, error) {
	return cl.client.LoadContainer(ctx, id)
}

func (cl realClient) Version(ctx context.Context) (containerd.Version, error) {
	return cl.client.Version(ctx)
}

func (cl realClient) Metrics(ctx context.Context, filters []string) (*tasks.MetricsResponse, error) {
	return cl.client.TaskService().Metrics(
		ctx,
		&tasks.MetricsRequest{Filters: filters},
	)
}

func (cl realClient) Namespaces(ctx context.Context) ([]string, error) {
	return cl.client.NamespaceService().List(ctx)
}

func (cl realClient) Events(ctx context.Context) (ch <-chan *events.Envelope, errs <-chan error) {
	return cl.client.EventService().Subscribe(ctx)
}

func (cl realClient) Close() error {
	return cl.client.Close()
}

const expectedSpecType = "types.containerd.io/opencontainers/runtime-spec/1/Spec"

type containerObject struct {
	namespace string
	info      ContainerOCISpec
	pid       int
	state     string
	args      []string
	exitTime  time.Time
	imageID   string
}

// ContainerOCISpec contains Info() & unmarshaled oci Spec.
type ContainerOCISpec struct {
	containers.Container
	Spec *oci.Spec `json:"Spec,omitempty"`
}

func (c containerObject) RuntimeName() string {
	return "containerd"
}

func (c containerObject) Annotations() map[string]string {
	return nil
}

func (c containerObject) Command() []string {
	return c.args
}

func (c containerObject) ContainerJSON() string {
	buffer, err := json.MarshalIndent(c.info, "", "  ")
	if err != nil {
		logger.V(2).Printf("unable to marshel container info: %v", err)
	}

	return string(buffer)
}

func (c containerObject) ContainerName() string {
	podName := c.PodName()
	podNamespace := c.PodNamespace()
	containerName := c.info.Labels["io.kubernetes.container.name"]

	if c.info.Labels["io.cri-containerd.kind"] == "sandbox" {
		containerName = "POD"
	}

	if podName != "" && podNamespace != "" && containerName != "" {
		return fmt.Sprintf("k8s_%s_%s_%s", containerName, podName, podNamespace)
	}

	return c.info.ID
}

func (c containerObject) CreatedAt() time.Time {
	return c.info.CreatedAt
}

func (c containerObject) Environment() map[string]string {
	if c.info.Spec == nil || c.info.Spec.Process == nil {
		return nil
	}

	result := make(map[string]string, len(c.info.Spec.Process.Env))

	for _, l := range c.info.Spec.Process.Env {
		part := strings.SplitN(l, "=", 2)
		if len(part) != 2 {
			continue
		}

		result[part[0]] = part[1]
	}

	return result
}

func (c containerObject) FinishedAt() time.Time {
	return c.exitTime
}

func (c containerObject) Health() (facts.ContainerHealth, string) {
	return facts.ContainerNoHealthCheck, ""
}

func (c containerObject) ID() string {
	return fmt.Sprintf("%s/%s", c.namespace, c.info.ID)
}

func (c containerObject) ImageID() string {
	return c.imageID
}

func (c containerObject) ImageName() string {
	return c.info.Image
}

func (c containerObject) Labels() map[string]string {
	return c.info.Labels
}

func (c containerObject) ListenAddresses() (addresses []facts.ListenAddress, explicit bool) {
	return nil, false
}

func (c containerObject) PodName() string {
	return c.info.Labels["io.kubernetes.pod.name"]
}

func (c containerObject) PodNamespace() string {
	return c.info.Labels["io.kubernetes.pod.namespace"]
}

func (c containerObject) PrimaryAddress() string {
	return ""
}

func (c containerObject) StartedAt() time.Time {
	return time.Time{}
}

func (c containerObject) State() facts.ContainerState {
	switch containerd.ProcessStatus(c.state) {
	case containerd.Created:
		return facts.ContainerCreated
	case containerd.Paused:
		return facts.ContainerRunning
	case containerd.Pausing:
		return facts.ContainerRunning
	case containerd.Running:
		return facts.ContainerRunning
	case containerd.Stopped:
		return facts.ContainerStopped
	case containerd.Unknown:
		return facts.ContainerUnknown
	default:
		return facts.ContainerUnknown
	}
}

func (c containerObject) StoppedAndReplaced() bool {
	return false
}

func isContainerdRunning() bool {
	pids, err := process.Pids()
	if err != nil {
		return false
	}

	for _, pid := range pids {
		p, err := process.NewProcess(pid)
		if err != nil {
			continue
		}

		if n, _ := p.Name(); n == "containerd" {
			return true
		}
	}

	return false
}

var cgroupRE = regexp.MustCompile(
	`(?m:^\d+:(cpu,cpuacct|memory):(.*)$)`,
)

type namespaceContainer struct {
	namespace string
	container containerd.Container
}

type containerdProcessQuerier struct {
	c                     *Containerd
	containersUpdated     bool
	containersToQueryPIDS []namespaceContainer
	pid2container         map[int]containerObject
}

func (q *containerdProcessQuerier) Processes(ctx context.Context) ([]facts.Process, error) {
	// API don't have top() like Docker. We don't exec "ps" since the binary may not exist in containers.
	return nil, nil
}

func (q *containerdProcessQuerier) ContainerFromCGroup(ctx context.Context, cgroupData string) (facts.Container, error) {
	cgroupPath := ""

	for _, submatches := range cgroupRE.FindAllStringSubmatch(cgroupData, -1) {
		cgroupPath = submatches[2]

		break
	}

	if cgroupPath == "" {
		return nil, nil
	}

	q.c.l.Lock()
	defer q.c.l.Unlock()

	cont, ok := q.getContainerFromCGroupPath(cgroupPath)
	if ok {
		return cont, nil
	}

	if !q.containersUpdated {
		q.containersUpdated = true

		if err := q.c.updateContainers(ctx); err != nil {
			return nil, err
		}

		cont, ok := q.getContainerFromCGroupPath(cgroupPath)
		if ok {
			return cont, nil
		}
	}

	return nil, nil
}

func (q *containerdProcessQuerier) getContainerFromCGroupPath(cgroupPath string) (containerObject, bool) {
	// cgroupPath usually ends with /$namespace/$name. Since we use $namespace/$name as key,
	// try direct access first.
	part := strings.Split(cgroupPath, "/")

	if size := len(part); size > 2 {
		candidate := fmt.Sprintf("%s/%s", part[size-2], part[size-1])

		container, ok := q.c.containers[candidate]
		if ok && container.info.Spec.Linux != nil && strings.HasSuffix(cgroupPath, container.info.Spec.Linux.CgroupsPath) {
			return container, true
		}
	}

	for _, c := range q.c.containers {
		if c.info.Spec.Linux != nil && strings.HasSuffix(cgroupPath, c.info.Spec.Linux.CgroupsPath) {
			return c, true
		}
	}

	return containerObject{}, false
}

func (q *containerdProcessQuerier) ContainerFromPID(ctx context.Context, parentContainerID string, pid int) (facts.Container, error) {
	q.c.l.Lock()
	defer q.c.l.Unlock()

	if c, ok := q.pid2container[pid]; ok {
		return c, nil
	}

	for _, c := range q.c.containers {
		if c.pid == pid {
			return c, nil
		}
	}

	container, err := q.containerFromPID(ctx, pid)
	if err != nil {
		return nil, err
	}

	if container != nil {
		return container, nil
	}

	if q.containersToQueryPIDS == nil {
		if err := q.listContainers(ctx); err != nil {
			return nil, err
		}
	}

	for len(q.containersToQueryPIDS) > 0 {
		obj := q.containersToQueryPIDS[0]
		q.containersToQueryPIDS = q.containersToQueryPIDS[1:]
		id := fmt.Sprintf("%s/%s", obj.namespace, obj.container.ID())

		cont, ok := q.c.containers[id]
		if !ok {
			continue
		}

		ctx := namespaces.WithNamespace(ctx, cont.namespace)

		task, err := obj.container.Task(ctx, nil)
		if err != nil {
			return nil, err
		}

		pids, err := task.Pids(ctx)
		if err != nil {
			return nil, err
		}

		for _, p := range pids {
			q.pid2container[int(p.Pid)] = cont
		}

		if c, ok := q.pid2container[pid]; ok {
			return c, nil
		}
	}

	if c, ok := q.pid2container[pid]; ok {
		return c, nil
	}

	return nil, nil
}

// containerFromPID return the container which had pid as first process. It will update list of containers if needed.
func (q *containerdProcessQuerier) containerFromPID(ctx context.Context, pid int) (facts.Container, error) {
	if !q.containersUpdated {
		q.containersUpdated = true

		if err := q.c.updateContainers(ctx); err != nil {
			return nil, err
		}

		for _, c := range q.c.containers {
			if c.pid == pid {
				return c, nil
			}
		}
	}

	return nil, nil
}

func (q *containerdProcessQuerier) listContainers(ctx context.Context) error {
	q.containersToQueryPIDS = make([]namespaceContainer, 0)

	cl, err := q.c.getClient(ctx)
	if err != nil {
		return err
	}

	nsList, err := cl.Namespaces(ctx)
	if err != nil {
		return fmt.Errorf("listing namespace failed: %w", err)
	}

	for _, ns := range nsList {
		if ns == ignoredNamespace {
			continue
		}

		ctx := namespaces.WithNamespace(ctx, ns)

		list, err := cl.Containers(ctx)
		if err != nil {
			return fmt.Errorf("listing containers failed: %w", err)
		}

		for _, c := range list {
			q.containersToQueryPIDS = append(q.containersToQueryPIDS, namespaceContainer{
				namespace: ns,
				container: c,
			})
		}
	}

	return nil
}

type metricValue struct {
	ContainerNamespace string
	ContainerID        string
	Time               time.Time
	values             map[string]uint64
}

//nolint:gocyclo,cyclop
func rateFromMetricValue(gloutonIDToName map[string]string, pastValues []metricValue, newValues []metricValue) []types.MetricPoint {
	memUsage, err := mem.VirtualMemory()
	if err != nil {
		logger.V(2).Printf("unable to get machine memory: %v", err)
	}

	gloutonIDToPast := make(map[string]metricValue, len(pastValues))

	for _, v := range pastValues {
		id := v.ContainerNamespace + "/" + v.ContainerID
		gloutonIDToPast[id] = v
	}

	points := make([]types.MetricPoint, 0, len(newValues)*7)

	for _, newV := range newValues {
		id := newV.ContainerNamespace + "/" + newV.ContainerID

		pastV, ok := gloutonIDToPast[id]
		if !ok {
			continue
		}

		name := gloutonIDToName[id]
		if name == "" {
			continue
		}

		deltaT := newV.Time.Sub(pastV.Time)
		if deltaT < 0 {
			continue
		}

		for k, v := range newV.values {
			var floatValue float64

			switch {
			case k == "container_mem_limit":
				// This metric isn't emitted
				continue
			case k == "container_mem_used":
				// It's the only non-derivated value
				floatValue = float64(v)
			case pastV.values[k] <= v:
				floatValue = float64(v-pastV.values[k]) / deltaT.Seconds()
			default:
				// assume reset of the counter
				floatValue = float64(v) / deltaT.Seconds()
			}

			if k == "container_cpu_used" {
				// value is in nano-seconds, convert to %
				floatValue = floatValue / 1e9 * 100
			}

			points = append(points, types.MetricPoint{
				Point: types.Point{Time: newV.Time, Value: floatValue},
				Labels: map[string]string{
					types.LabelName:              k,
					types.LabelMetaContainerName: name,
				},
				Annotations: types.MetricAnnotations{
					BleemeoItem: name,
					ContainerID: id,
				},
			})

			if k == "container_mem_used" {
				limit := newV.values["container_mem_limit"]
				if memUsage != nil && (limit > memUsage.Total || limit == 0) {
					limit = memUsage.Total
				} else if limit >= 9e18 {
					limit = 0
				}

				if limit > 0 {
					floatValue = floatValue / float64(limit) * 100
					if floatValue > 100 {
						floatValue = 100
					}

					points = append(points, types.MetricPoint{
						Point: types.Point{Time: newV.Time, Value: floatValue},
						Labels: map[string]string{
							types.LabelName:              "container_mem_used_perc",
							types.LabelMetaContainerName: name,
						},
						Annotations: types.MetricAnnotations{
							BleemeoItem: name,
							ContainerID: id,
						},
					})
				}
			}
		}
	}

	return points
}
