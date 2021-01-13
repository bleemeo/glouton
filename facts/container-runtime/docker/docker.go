package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"glouton/facts"
	"glouton/logger"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	dockerTypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	docker "github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/shirou/gopsutil/process"
)

// errors of Docker runtime.
var (
	ErrDockerUnexcepted = errors.New("unexcepted data")
)

// DefaultAddresses returns default address for the Docker socket. If hostroot is set (and not "/") ALSO add
// socket path prefixed by hostRoot.
func DefaultAddresses(hostRoot string) []string {
	list := []string{""}
	if hostRoot != "" && hostRoot != "/" {
		list = append(list, "unix://"+filepath.Join(hostRoot, "run/docker.sock"), "unix://"+filepath.Join(hostRoot, "var/run/docker.sock"))
	}

	return list
}

// Docker implement a method to query Docker runtime.
// It try to connect to the first valid DockerSockets. Empty string is a special
// value: it means use default.
type Docker struct {
	DockerSockets             []string
	DeletedContainersCallback func(containersID []string)

	l                sync.Mutex
	openConnection   func(ctx context.Context, host string) (cl dockerClient, err error)
	serverAddress    string
	client           dockerClient
	serverVersion    string
	apiVersion       string
	reconnectAttempt int
	notifyC          chan facts.ContainerEvent
	lastEventAt      time.Time

	containers                     map[string]dockerContainer
	lastKill                       map[string]time.Time
	ignoredID                      map[string]bool
	lastUpdate                     time.Time
	bridgeNetworks                 map[string]interface{}
	containerAddressOnDockerBridge map[string]string
}

// ProcessWithCache facts.containerRuntime.
func (d *Docker) ProcessWithCache() facts.ContainerRuntimeProcessQuerier {
	return &dockerProcessQuerier{
		d:                    d,
		containerProcessDone: make(map[string]bool),
	}
}

// RuntimeFact will return facts from the Docker runtime, like docker_version.
func (d *Docker) RuntimeFact(ctx context.Context, currentFact map[string]string) map[string]string {
	d.l.Lock()
	defer d.l.Unlock()

	_, err := d.ensureClient(ctx)
	if err != nil {
		return nil
	}

	if d.serverVersion == "" {
		return nil
	}

	return map[string]string{
		"docker_version":     d.serverVersion,
		"docker_api_version": d.apiVersion,
		"container_runtime":  "Docker",
	}
}

// ServerAddress will return the last server address for which connection sucessed.
// Note that empty string could be "docker default" or no valid connection. You should use this after call to IsRuntimeRunning().
func (d *Docker) ServerAddress() string {
	d.l.Lock()
	defer d.l.Unlock()

	return d.serverAddress
}

// CachedContainer return a container without querying Docker, it use in-memory cache which must have been filled by a call to Continers().
func (d *Docker) CachedContainer(containerID string) (c facts.Container, found bool) {
	d.l.Lock()
	defer d.l.Unlock()

	c, found = d.containers[containerID]

	return c, found
}

// Containers return Docker containers.
func (d *Docker) Containers(ctx context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, err error) {
	d.l.Lock()
	defer d.l.Unlock()

	if time.Since(d.lastUpdate) >= maxAge {
		err = d.updateContainers(ctx)
		if err != nil {
			return
		}
	}

	containers = make([]facts.Container, 0, len(d.containers))
	for _, c := range d.containers {
		if includeIgnored || !facts.ContainerIgnored(c) {
			containers = append(containers, c)
		}
	}

	return
}

// Run will run connect and listen to Docker event until context is cancelled
//
// Any error (unable to connect due to permission issue or Docker down) are not returned
// by Run but could be retrieved with LastError.
func (d *Docker) Run(ctx context.Context) error {
	var (
		lastErrorNotify time.Time
		sleepDelay      float64
	)

	notifyError := func(err error) {
		if time.Since(lastErrorNotify) < time.Hour && d.reconnectAttempt > 1 {
			return
		}

		if strings.Contains(fmt.Sprintf("%v", err), "permission denied") {
			logger.Printf(
				"The agent is not permitted to access Docker, the Docker integration will be disabled.",
			)
			logger.Printf(
				"'adduser glouton docker' and a restart of the Agent should fix this issue",
			)
		} else if isDockerRunning() {
			logger.Printf("Unable to contact Docker: %v", err)
		}

		lastErrorNotify = time.Now()
	}

	// This will initialize d.notifyC
	d.Events()

	for {
		err := d.run(ctx)

		d.l.Lock()

		d.reconnectAttempt++

		if err != nil {
			notifyError(err)
		}

		sleepDelay = 5 * math.Pow(2, float64(d.reconnectAttempt))
		if sleepDelay > 60 {
			sleepDelay = 60
		}

		d.l.Unlock()

		select {
		case <-time.After(time.Duration(sleepDelay) * time.Second):
		case <-ctx.Done():
			close(d.notifyC)
			return nil
		}
	}
}

// ContainerLastKill return the last time a container was killed or zero-time if unknown.
func (d *Docker) ContainerLastKill(containerID string) time.Time {
	d.l.Lock()
	defer d.l.Unlock()

	return d.lastKill[containerID]
}

// Exec run a command in a container and return stdout+stderr.
func (d *Docker) Exec(ctx context.Context, containerID string, cmd []string) ([]byte, error) {
	d.l.Lock()
	cl, err := d.getClient(ctx)
	d.l.Unlock()

	if err != nil {
		return nil, err
	}

	id, err := cl.ContainerExecCreate(ctx, containerID, dockerTypes.ExecConfig{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, err
	}

	resp, err := cl.ContainerExecAttach(ctx, id.ID, dockerTypes.ExecStartCheck{})
	if err != nil {
		return nil, err
	}

	defer resp.Close()

	var output bytes.Buffer

	_, err = stdcopy.StdCopy(&output, &output, resp.Reader)
	if err != nil {
		return nil, err
	}

	return output.Bytes(), nil
}

// Events return the channel used to send events. There is only one shared channel (so
// multiple consumer should be implemented by caller).
func (d *Docker) Events() <-chan facts.ContainerEvent {
	d.l.Lock()
	defer d.l.Unlock()

	if d.notifyC == nil {
		d.notifyC = make(chan facts.ContainerEvent)
	}

	return d.notifyC
}

// IsRuntimeRunning returns whether or not Docker is available
//
// IsRuntimeRunning will try to open a new connection if it never tried. It will also check that connection is still working (do a ping).
// Note: if Docker is running but Glouton can't access it, IsRuntimeRunning will return false.
func (d *Docker) IsRuntimeRunning(ctx context.Context) bool {
	d.l.Lock()
	defer d.l.Unlock()

	_, err := d.ensureClient(ctx)

	return err == nil
}

// similar to getClient but also check that connection works.
func (d *Docker) ensureClient(ctx context.Context) (cl dockerClient, err error) {
	cl = d.client

	if cl == nil {
		cl, err = d.getClient(ctx)
		if err != nil {
			return nil, err
		}
	}

	if _, err := cl.Ping(ctx); err != nil {
		d.apiVersion = ""
		d.serverVersion = ""
		d.client = nil

		cl, err = d.getClient(ctx)
		if err != nil {
			return nil, err
		}
	}

	return cl, nil
}

func (d *Docker) run(ctx context.Context) error {
	d.l.Lock()

	cl, err := d.ensureClient(ctx)

	if d.lastKill == nil {
		d.lastKill = make(map[string]time.Time)
	}

	d.l.Unlock()

	if err != nil {
		return err
	}

	// Make sure information is recent enough
	_, _ = d.Containers(ctx, time.Minute, false)

	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()

	eventC, errC := cl.Events(ctx2, dockerTypes.EventsOptions{Since: d.lastEventAt.Format(time.RFC3339Nano)})

	var lastCleanup time.Time

	for {
		if time.Since(lastCleanup) > 10*time.Minute {
			d.l.Lock()

			for k, v := range d.lastKill {
				if time.Since(v) > time.Hour {
					delete(d.lastKill, k)
				}
			}

			d.l.Unlock()
		}

		select {
		case event, ok := <-eventC:
			if !ok {
				return ctx.Err()
			}

			d.lastEventAt = time.Unix(event.Time, event.TimeNano)

			if event.Type == "" || event.Type == "container" {
				action := event.Action
				actorID := event.Actor.ID

				if event.Action == "" {
					// Docker before 1.10 didn't had Action
					action = event.Status
				}

				if event.Actor.ID == "" {
					// Docker before 1.10 didn't had Actor
					actorID = event.ID
				}

				d.l.Lock()
				_, ok := d.ignoredID[actorID]
				d.l.Unlock()

				if ok {
					continue
				}

				if action == "kill" {
					d.l.Lock()
					d.lastKill[actorID] = time.Now()
					d.l.Unlock()
				}

				event := facts.ContainerEvent{
					ContainerID: actorID,
				}

				switch action {
				case "start":
					event.Type = facts.EventTypeStart
				case "die":
					event.Type = facts.EventTypeStop
				case "kill", "stop":
					event.Type = facts.EventTypeKill
				case "destroy":
					event.Type = facts.EventTypeDelete
				default:
					event.Type = facts.EventTypeUnknown
				}

				if strings.HasPrefix(action, "health_status:") {
					event.Type = facts.EventTypeHealth

					_, err := d.updateContainer(ctx, cl, actorID)
					if err != nil {
						logger.V(1).Printf("Update of container %v failed (will assume container is removed): %v", actorID, err)
						continue
					}
				}

				d.l.Lock()

				event.Container = d.containers[actorID]

				switch event.Type {
				case facts.EventTypeDelete:
					delete(d.containers, event.ContainerID)
				case facts.EventTypeStop:
					current, ok := d.containers[event.ContainerID]
					if ok {
						current.stopped = true
						d.containers[event.ContainerID] = current
					}
				}

				d.l.Unlock()

				select {
				case d.notifyC <- event:
				case <-ctx.Done():
				}
			}
		case err = <-errC:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (d *Docker) updateContainers(ctx context.Context) error {
	cl, err := d.getClient(ctx)
	if err != nil {
		return err
	}

	bridgeNetworks := make(map[string]interface{})
	containerAddressOnDockerBridge := make(map[string]string)

	if networks, err := cl.NetworkList(ctx, dockerTypes.NetworkListOptions{}); err == nil {
		for _, n := range networks {
			if n.Name == "" {
				continue
			}

			if n.Driver == "bridge" {
				bridgeNetworks[n.Name] = nil
			}
		}
	}

	if network, err := cl.NetworkInspect(ctx, "docker_gwbridge", dockerTypes.NetworkInspectOptions{}); err == nil {
		for containerID, endpoint := range network.Containers {
			// IPv4Address is an CIDR (like "172.17.0.4/24")
			address := strings.Split(endpoint.IPv4Address, "/")[0]
			containerAddressOnDockerBridge[containerID] = address
		}
	}

	dockerContainers, err := cl.ContainerList(ctx, dockerTypes.ContainerListOptions{All: true})
	if err != nil {
		return err
	}

	containers := make(map[string]dockerContainer)
	ignoredID := make(map[string]bool)

	for _, c := range dockerContainers {
		inspect, err := cl.ContainerInspect(ctx, c.ID)
		if err != nil && docker.IsErrNotFound(err) || inspect.ContainerJSONBase == nil {
			continue // the container was deleted between call. Ignore it
		}

		if err != nil {
			return err
		}

		sortInspect(inspect)

		container := dockerContainer{
			primaryAddress: d.primaryAddress(inspect, bridgeNetworks, containerAddressOnDockerBridge),
			inspect:        inspect,
		}

		containers[c.ID] = container

		if facts.ContainerIgnored(container) {
			ignoredID[c.ID] = true
		}
	}

	var deletedContainerID []string

	for k := range d.containers {
		if _, ok := containers[k]; !ok {
			deletedContainerID = append(deletedContainerID, k)
		}
	}

	if len(deletedContainerID) > 0 && d.DeletedContainersCallback != nil {
		d.DeletedContainersCallback(deletedContainerID)
	}

	d.lastUpdate = time.Now()
	d.containers = containers
	d.ignoredID = ignoredID
	d.bridgeNetworks = bridgeNetworks
	d.containerAddressOnDockerBridge = containerAddressOnDockerBridge

	return nil
}

func (d *Docker) updateContainer(ctx context.Context, cl dockerClient, containerID string) (dockerContainer, error) {
	var result dockerContainer

	inspect, err := cl.ContainerInspect(ctx, containerID)
	if err != nil {
		return result, err
	}

	if inspect.ContainerJSONBase == nil {
		return result, errors.New("ContainerJSONBase is nil. Assume container is deleted")
	}

	sortInspect(inspect)

	d.l.Lock()
	defer d.l.Unlock()

	container := dockerContainer{
		primaryAddress: d.primaryAddress(inspect, d.bridgeNetworks, d.containerAddressOnDockerBridge),
		inspect:        inspect,
	}

	d.containers[containerID] = container

	if facts.ContainerIgnored(container) {
		d.ignoredID[containerID] = true
	} else {
		delete(d.ignoredID, containerID)
	}

	return d.containers[containerID], nil
}

func (d *Docker) primaryAddress(inspect dockerTypes.ContainerJSON, bridgeNetworks map[string]interface{}, containerAddressOnDockerBridge map[string]string) string {
	if inspect.NetworkSettings != nil && inspect.NetworkSettings.IPAddress != "" {
		return inspect.NetworkSettings.IPAddress
	}

	addressOfFirstNetwork := ""

	if inspect.NetworkSettings != nil {
		for key, ep := range inspect.NetworkSettings.Networks {
			if key == "host" {
				return "127.0.0.1"
			}

			if _, ok := bridgeNetworks[key]; ep.IPAddress != "" && ok {
				return ep.IPAddress
			}

			if addressOfFirstNetwork == "" && ep.IPAddress != "" {
				addressOfFirstNetwork = ep.IPAddress
			}
		}
	}

	if address := containerAddressOnDockerBridge[inspect.ID]; address != "" {
		return address
	}

	if addressOfFirstNetwork != "" {
		return addressOfFirstNetwork
	}

	if ipMask := inspect.Config.Labels["io.rancher.container.ip"]; ipMask != "" {
		return strings.Split(ipMask, "/")[0]
	}

	return ""
}

func (d *Docker) getClient(ctx context.Context) (cl dockerClient, err error) {
	if d.openConnection == nil {
		d.openConnection = openConnection
	}

	if d.client == nil {
		var firstErr error

		if len(d.DockerSockets) == 0 {
			d.DockerSockets = DefaultAddresses("")
		}

		for _, addr := range d.DockerSockets {
			cl, err := d.openConnection(ctx, addr)
			if err != nil {
				logger.V(2).Printf("Docker openConnection on %s failed: %v", addr, err)

				if firstErr == nil {
					firstErr = err
				}

				continue
			}

			v, err := cl.ServerVersion(ctx)
			if err != nil {
				logger.V(2).Printf("Docker openConnection on %s failed: %v", addr, err)

				if firstErr == nil {
					firstErr = err
				}

				continue
			}

			d.apiVersion = v.APIVersion
			d.serverVersion = v.Version
			d.client = cl
			d.serverAddress = addr

			break
		}

		if d.client == nil {
			return nil, firstErr
		}
	}

	return d.client, nil
}

type dockerClient interface {
	ContainerExecAttach(ctx context.Context, execID string, config dockerTypes.ExecStartCheck) (dockerTypes.HijackedResponse, error)
	ContainerExecCreate(ctx context.Context, container string, config dockerTypes.ExecConfig) (dockerTypes.IDResponse, error)
	ContainerInspect(ctx context.Context, container string) (dockerTypes.ContainerJSON, error)
	ContainerList(ctx context.Context, options dockerTypes.ContainerListOptions) ([]dockerTypes.Container, error)
	ContainerTop(ctx context.Context, container string, arguments []string) (container.ContainerTopOKBody, error)
	Events(ctx context.Context, options dockerTypes.EventsOptions) (<-chan events.Message, <-chan error)
	NetworkInspect(ctx context.Context, network string, options dockerTypes.NetworkInspectOptions) (dockerTypes.NetworkResource, error)
	NetworkList(ctx context.Context, options dockerTypes.NetworkListOptions) ([]dockerTypes.NetworkResource, error)
	Ping(ctx context.Context) (dockerTypes.Ping, error)
	ServerVersion(ctx context.Context) (dockerTypes.Version, error)
}

func openConnection(ctx context.Context, host string) (cl dockerClient, err error) {
	if host != "" {
		if u, err := url.Parse(host); err == nil && u.Scheme == "unix://" {
			_, err := os.Stat(u.Path)
			if err != nil {
				return nil, fmt.Errorf("unable to access socket %s: %w", host, err)
			}
		}
	}

	if host == "" {
		cl, err = docker.NewClientWithOpts(docker.FromEnv, docker.WithAPIVersionNegotiation(), docker.WithTimeout(10*time.Second))
	} else {
		cl, err = docker.NewClientWithOpts(docker.FromEnv, docker.WithHost(host), docker.WithAPIVersionNegotiation(), docker.WithTimeout(10*time.Second))
	}

	if err != nil {
		return nil, err
	}

	if _, err = cl.Ping(ctx); err != nil {
		return nil, err
	}

	return cl, nil
}

func dockerStatus2State(status string) facts.ContainerState {
	switch status {
	case "exited":
		return facts.ContainerStopped
	case "running":
		return facts.ContainerRunning
	case "restarting":
		return facts.ContainerRestarting
	case "removing":
		return facts.ContainerStopped
	case "paused":
		return facts.ContainerRunning
	case "dead":
		return facts.ContainerStopped
	default:
		return facts.ContainerUnknown
	}
}

func isDockerRunning() bool {
	pids, err := process.Pids()
	if err != nil {
		return false
	}

	for _, pid := range pids {
		p, err := process.NewProcess(pid)
		if err != nil {
			continue
		}

		if n, _ := p.Name(); n == "dockerd" {
			return true
		}
	}

	return false
}

func sortInspect(inspect dockerTypes.ContainerJSON) {
	// Sort the docker inspect to have consistent hash value
	// Mounts order does not matter but is not consistent between call to docker inspect.
	if len(inspect.Mounts) > 0 {
		sort.Slice(inspect.Mounts, func(i int, j int) bool {
			if inspect.Mounts[i].Source < inspect.Mounts[j].Source {
				return true
			}

			if inspect.Mounts[i].Source == inspect.Mounts[j].Source && inspect.Mounts[i].Destination < inspect.Mounts[j].Destination {
				return true
			}

			return false
		})
	}
}

// dockerContainer wraps the Docker inspect values and provide facts.Container implementation.
type dockerContainer struct {
	primaryAddress string
	inspect        dockerTypes.ContainerJSON
	stopped        bool
}

func (c dockerContainer) RuntimeName() string {
	return "docker"
}

func (c dockerContainer) Annotations() map[string]string {
	return nil
}

func (c dockerContainer) Command() []string {
	if c.inspect.Config == nil {
		return nil
	}

	return c.inspect.Config.Cmd
}
func (c dockerContainer) ContainerJSON() string {
	result, err := json.Marshal(c.inspect)
	if err != nil {
		return ""
	}

	return string(result)
}
func (c dockerContainer) ContainerName() string {
	if c.inspect.Name[0] == '/' {
		return c.inspect.Name[1:]
	}

	return c.inspect.Name
}
func (c dockerContainer) CreatedAt() time.Time {
	var result time.Time

	result, err := time.Parse(time.RFC3339Nano, c.inspect.Created)
	if err != nil {
		return result
	}

	return result
}
func (c dockerContainer) Environment() map[string]string {
	if c.inspect.Config == nil {
		return make(map[string]string)
	}

	results := make(map[string]string, len(c.inspect.Config.Env))

	for _, env := range c.inspect.Config.Env {
		part := strings.SplitN(env, "=", 2)
		if len(part) != 2 {
			continue
		}

		results[part[0]] = part[1]
	}

	return results
}
func (c dockerContainer) FinishedAt() time.Time {
	var result time.Time

	if c.inspect.State == nil {
		return result
	}

	result, err := time.Parse(time.RFC3339Nano, c.inspect.State.FinishedAt)
	if err != nil {
		return result
	}

	return result
}
func (c dockerContainer) Health() (facts.ContainerHealth, string) {
	if !c.State().IsRunning() {
		return facts.ContainerHealthUnknown, "container is stopped"
	}

	if c.inspect.State == nil {
		return facts.ContainerHealthUnknown, "container don't have State"
	}

	if c.inspect.State.Health == nil {
		return facts.ContainerNoHealthCheck, ""
	}

	msg := ""
	index := len(c.inspect.State.Health.Log) - 1

	if index >= 0 && c.inspect.State.Health.Log[index] != nil {
		msg = c.inspect.State.Health.Log[index].Output
	}

	switch c.inspect.State.Health.Status {
	case "healthy":
		return facts.ContainerHealthy, msg
	case "starting":
		return facts.ContainerStarting, msg
	case "unhealthy":
		return facts.ContainerUnhealthy, msg
	default:
		return facts.ContainerHealthUnknown, msg
	}
}

func (c dockerContainer) ID() string {
	return c.inspect.ID
}

func (c dockerContainer) ImageID() string {
	return c.inspect.Image
}

func (c dockerContainer) ImageName() string {
	if c.inspect.Config == nil {
		return c.inspect.Image
	}

	return c.inspect.Config.Image
}
func (c dockerContainer) Labels() map[string]string {
	if c.inspect.Config == nil {
		return nil
	}

	return c.inspect.Config.Labels
}
func (c dockerContainer) ListenAddresses() (addresses []facts.ListenAddress, explicit bool) {
	exposedPorts := make([]facts.ListenAddress, 0)

	if len(exposedPorts) == 0 && c.inspect.NetworkSettings != nil && len(c.inspect.NetworkSettings.Ports) > 0 {
		for k, v := range c.inspect.NetworkSettings.Ports {
			if len(v) == 0 {
				continue
			}

			exposedPorts = append(exposedPorts, facts.ListenAddress{
				NetworkFamily: k.Proto(),
				Address:       c.PrimaryAddress(),
				Port:          k.Int(),
			})

			explicit = true
		}
	}

	if len(exposedPorts) == 0 && c.inspect.Config != nil {
		for v := range c.inspect.Config.ExposedPorts {
			exposedPorts = append(exposedPorts, facts.ListenAddress{NetworkFamily: v.Proto(), Address: c.PrimaryAddress(), Port: v.Int()})
		}

		// The information come from "EXPOSE" from Dockerfile. It's easy to have configuration of the service
		// which make is listen on another port, but user can't override EXPOSE without building it own Docker image.
		// So this information is likely to be wrong.
		explicit = false
	}

	sort.Slice(exposedPorts, func(i, j int) bool {
		return exposedPorts[i].Port < exposedPorts[j].Port
	})

	return exposedPorts, explicit
}
func (c dockerContainer) PodName() string {
	// Get the POD namespace from Docker labels if k8s API not available
	labels := c.Labels()

	return labels["io.kubernetes.pod.name"]
}
func (c dockerContainer) PodNamespace() string {
	// Get the POD namespace from Docker labels if k8s API not available
	labels := c.Labels()

	return labels["io.kubernetes.pod.namespace"]
}
func (c dockerContainer) PrimaryAddress() string {
	return c.primaryAddress
}
func (c dockerContainer) StartedAt() time.Time {
	var result time.Time

	if c.inspect.State == nil {
		return result
	}

	result, err := time.Parse(time.RFC3339Nano, c.inspect.State.StartedAt)
	if err != nil {
		return result
	}

	return result
}
func (c dockerContainer) State() facts.ContainerState {
	if c.inspect.State == nil {
		return facts.ContainerUnknown
	}

	if c.stopped {
		return facts.ContainerStopped
	}

	return dockerStatus2State(c.inspect.State.Status)
}
func (c dockerContainer) StoppedAndReplaced() bool {
	return false
}

var (
	//nolint:gochecknoglobals
	dockerCGroupRE = regexp.MustCompile(
		`(?m:^\d+:[^:]+:(/kubepods/.*pod[0-9a-fA-F-]+/|.*/docker[-/])([0-9a-fA-F]+)(\.scope)?$)`,
	)
)

type dockerProcessQuerier struct {
	d                    *Docker
	containers           map[string]dockerContainer
	processesMap         map[int]facts.Process
	containerProcessDone map[string]bool
}

// Processes returns processes from all running Docker containers.
func (d *dockerProcessQuerier) Processes(ctx context.Context) ([]facts.Process, error) {
	err := d.processes(ctx, -1, "")
	if err != nil {
		return nil, err
	}

	processes := make([]facts.Process, 0, len(d.processesMap))
	for _, p := range d.processesMap {
		processes = append(processes, p)
	}

	return processes, nil
}

func (d *dockerProcessQuerier) processes(ctx context.Context, searchPID int, firstContainer string) error {
	if d.containers == nil {
		_, err := d.d.Containers(ctx, 0, false)
		if err != nil {
			d.containers = make(map[string]dockerContainer)
			return err
		}

		d.d.l.Lock()

		d.containers = make(map[string]dockerContainer, len(d.d.containers))
		for k, v := range d.d.containers {
			d.containers[k] = v
		}

		d.d.l.Unlock()
	}

	if d.processesMap == nil {
		d.processesMap = make(map[int]facts.Process, len(d.containers)+10)
	}

	if c, ok := d.containers[firstContainer]; ok {
		err := d.processesContainerMap(ctx, c)
		if err != nil {
			return err
		}
	}

	for _, c := range d.containers {
		if _, ok := d.processesMap[searchPID]; ok && searchPID != -1 {
			return nil
		}

		if d.containerProcessDone[c.ID()] {
			continue
		}

		if !c.State().IsRunning() {
			continue
		}

		err := d.processesContainerMap(ctx, c)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *dockerProcessQuerier) processesContainerMap(ctx context.Context, c facts.Container) error {
	if d.containerProcessDone[c.ID()] {
		return nil
	}

	d.containerProcessDone[c.ID()] = true
	top, topWaux, err := d.top(ctx, c)

	switch {
	case err != nil && errdefs.IsNotFound(err):
		return nil
	case err != nil && strings.Contains(fmt.Sprintf("%v", err), "is not running"):
		return nil
	case err != nil:
		return err
	}

	processes1 := decodeDocker(top, c)
	processes2 := decodeDocker(topWaux, c)

	for _, p := range processes1 {
		d.processesMap[p.PID] = p
	}

	for _, p := range processes2 {
		if pOld, ok := d.processesMap[p.PID]; ok {
			pOld.Update(p)
			d.processesMap[p.PID] = pOld
		} else {
			d.processesMap[p.PID] = p
		}
	}

	return nil
}

func (d *dockerProcessQuerier) top(ctx context.Context, c facts.Container) (container.ContainerTopOKBody, container.ContainerTopOKBody, error) {
	d.d.l.Lock()
	cl, err := d.d.getClient(ctx)
	d.d.l.Unlock()

	if err != nil {
		return container.ContainerTopOKBody{}, container.ContainerTopOKBody{}, err
	}

	top, err := cl.ContainerTop(ctx, c.ID(), nil)
	if err != nil {
		return top, container.ContainerTopOKBody{}, err
	}

	topWaux, err := cl.ContainerTop(ctx, c.ID(), []string{"waux"})

	return top, topWaux, err
}

func decodeDocker(top container.ContainerTopOKBody, c facts.Container) []facts.Process {
	userIndex := -1
	pidIndex := -1
	pcpuIndex := -1
	rssIndex := -1
	timeIndex := -1
	cmdlineIndex := -1
	statIndex := -1
	ppidIndex := -1

	for i, v := range top.Titles {
		switch v {
		case "PID":
			pidIndex = i
		case "CMD", "COMMAND":
			cmdlineIndex = i
		case "UID", "USER":
			userIndex = i
		case "%CPU":
			pcpuIndex = i
		case "RSS":
			rssIndex = i
		case "TIME":
			timeIndex = i
		case "STAT":
			statIndex = i
		case "PPID":
			ppidIndex = i
		}
	}

	if pidIndex == -1 || cmdlineIndex == -1 {
		return nil
	}

	processes := make([]facts.Process, 0)

	for _, row := range top.Processes {
		pid, err := strconv.Atoi(row[pidIndex])
		if err != nil {
			continue
		}

		cmdLineList := strings.Split(row[cmdlineIndex], " ")
		process := facts.Process{
			PID:           pid,
			CmdLine:       row[cmdlineIndex],
			CmdLineList:   cmdLineList,
			Name:          filepath.Base(cmdLineList[0]),
			ContainerID:   c.ID(),
			ContainerName: c.ContainerName(),
		}

		if userIndex != -1 {
			process.Username = row[userIndex]
		}

		if pcpuIndex != -1 {
			v, err := strconv.ParseFloat(row[pcpuIndex], 64)
			if err == nil {
				process.CPUPercent = v
			}
		}

		if rssIndex != -1 {
			v, err := strconv.ParseInt(row[rssIndex], 10, 0)
			if err == nil {
				process.MemoryRSS = uint64(v)
			}
		}

		if timeIndex != -1 {
			v, err := psTime2Second(row[timeIndex])
			if err == nil {
				process.CPUTime = float64(v)
			}
		}

		if statIndex != -1 {
			process.Status = facts.PsStat2Status(row[statIndex])
		} else {
			process.Status = "?"
		}

		if ppidIndex != -1 {
			v, err := strconv.ParseInt(row[ppidIndex], 10, 0)
			if err == nil {
				process.PPID = int(v)
			}
		}

		processes = append(processes, process)
	}

	return processes
}

func psTime2Second(psTime string) (int, error) {
	if strings.Count(psTime, ":") == 1 {
		// format is MM:SS
		l := strings.Split(psTime, ":")

		minute, err := strconv.ParseInt(l[0], 10, 0)
		if err != nil {
			return 0, err
		}

		second, err := strconv.ParseInt(l[1], 10, 0)
		if err != nil {
			return 0, err
		}

		return int(minute)*60 + int(second), nil
	}

	if strings.Count(psTime, ":") == 2 && strings.Contains(psTime, "-") {
		// format is DD-HH:MM:SS
		l := strings.Split(psTime, "-")

		day, err := strconv.ParseInt(l[0], 10, 0)
		if err != nil {
			return 0, err
		}

		l = strings.Split(l[1], ":")

		hour, err := strconv.ParseInt(l[0], 10, 0)
		if err != nil {
			return 0, err
		}

		minute, err := strconv.ParseInt(l[1], 10, 0)
		if err != nil {
			return 0, err
		}

		second, err := strconv.ParseInt(l[2], 10, 0)
		if err != nil {
			return 0, err
		}

		result := int(day)*86400 + int(hour)*3600 + int(minute)*60 + int(second)

		return result, nil
	}

	if strings.Count(psTime, ":") == 2 {
		// format is HH:MM:SS
		l := strings.Split(psTime, ":")

		hour, err := strconv.ParseInt(l[0], 10, 0)
		if err != nil {
			return 0, err
		}

		minute, err := strconv.ParseInt(l[1], 10, 0)
		if err != nil {
			return 0, err
		}

		second, err := strconv.ParseInt(l[2], 10, 0)
		if err != nil {
			return 0, err
		}

		result := int(hour)*3600 + int(minute)*60 + int(second)

		return result, nil
	}

	if strings.Contains(psTime, "h") {
		// format is HHhMM
		l := strings.Split(psTime, "h")

		hour, err := strconv.ParseInt(l[0], 10, 0)
		if err != nil {
			return 0, err
		}

		minute, err := strconv.ParseInt(l[1], 10, 0)
		if err != nil {
			return 0, err
		}

		return int(hour)*3600 + int(minute)*60, nil
	}

	if strings.Contains(psTime, "d") {
		// format is DDdHH
		l := strings.Split(psTime, "d")

		day, err := strconv.ParseInt(l[0], 10, 0)
		if err != nil {
			return 0, err
		}

		hour, err := strconv.ParseInt(l[1], 10, 0)
		if err != nil {
			return 0, err
		}

		return int(day)*86400 + int(hour)*3600, nil
	}

	return 0, fmt.Errorf("unknown pstime format %#v", psTime)
}

func (d *dockerProcessQuerier) ContainerFromCGroup(ctx context.Context, cgroupData string) (facts.Container, error) {
	containerID := ""

	for _, submatches := range dockerCGroupRE.FindAllStringSubmatch(cgroupData, -1) {
		if containerID == "" {
			containerID = submatches[2]
		} else if containerID != submatches[2] {
			// different value for the same PID. Abort detection of container ID from cgroup
			return nil, fmt.Errorf("%w: more than one ID from cgroup: %v != %v", ErrDockerUnexcepted, containerID, submatches[2])
		}
	}

	if containerID == "" {
		return nil, nil
	}

	if d.containers == nil {
		_, err := d.d.Containers(ctx, 0, false)
		if err != nil {
			d.containers = make(map[string]dockerContainer)
			return nil, err
		}

		d.d.l.Lock()

		d.containers = make(map[string]dockerContainer, len(d.d.containers))
		for k, v := range d.d.containers {
			d.containers[k] = v
		}

		d.d.l.Unlock()
	}

	c, ok := d.containers[containerID]
	if !ok {
		return nil, facts.ErrContainerDoesNotExists
	}

	return c, nil
}

func (d *dockerProcessQuerier) ContainerFromPID(ctx context.Context, parentContainerID string, pid int) (facts.Container, error) {
	err := d.processes(ctx, pid, parentContainerID)
	if err != nil {
		return nil, err
	}

	containerID := d.processesMap[pid].ContainerID
	container, ok := d.containers[containerID]

	if ok {
		return container, nil
	}

	return nil, nil
}
