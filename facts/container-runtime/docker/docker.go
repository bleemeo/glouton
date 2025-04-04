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

package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/facts/container-runtime/internal/obfuscation"
	containerTypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	dockerTypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/common"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	docker "github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/shirou/gopsutil/v4/process"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric/noop"
)

// errors of Docker runtime.
var (
	ErrDockerUnexcepted = errors.New("unexcepted data")
	errUnknownFormat    = errors.New("unknown pstime format")
	errNilJSON          = errors.New("ContainerJSONBase is nil. Assume container is deleted")
	errNoImageTagsFound = errors.New("no tags found")
)

// dockerTimeout is the time limit for requests made by the docker client.
const dockerTimeout = 10 * time.Second

// Docker implement a method to query Docker runtime.
// It try to connect to the first valid DockerSockets. Empty string is a special
// value: it means use default.
type Docker struct {
	DockerSockets             []string
	DeletedContainersCallback func(containersID []string)
	IsContainerIgnored        func(facts.Container) bool

	l                sync.Mutex
	workedOnce       bool
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
	lastDestroyedName              map[string]time.Time
	ignoredID                      map[string]bool
	lastUpdate                     time.Time
	bridgeNetworks                 map[string]interface{}
	containerAddressOnDockerBridge map[string]string
}

// New returns a new Docker runtime.
func New(
	runtime config.ContainerRuntimeAddresses,
	hostRoot string,
	deletedContainersCallback func(containersID []string),
	isContainerIgnored func(facts.Container) bool,
) *Docker {
	return newWithOpenner(
		containerTypes.ExpandRuntimeAddresses(runtime, hostRoot),
		deletedContainersCallback,
		isContainerIgnored,
		openConnection,
	)
}

func newWithOpenner(
	sockets []string,
	deletedContainersCallback func(containersID []string),
	isContainerIgnored func(facts.Container) bool,
	openConnection func(ctx context.Context, host string) (cl dockerClient, err error),
) *Docker {
	// This is required to avoid a memory leak https://github.com/open-telemetry/opentelemetry-go-contrib/issues/5190
	otel.SetMeterProvider(noop.MeterProvider{})

	return &Docker{
		DockerSockets:             sockets,
		DeletedContainersCallback: deletedContainersCallback,
		IsContainerIgnored:        isContainerIgnored,
		lastKill:                  make(map[string]time.Time),
		lastDestroyedName:         make(map[string]time.Time),
		containers:                make(map[string]dockerContainer),
		ignoredID:                 make(map[string]bool),
		bridgeNetworks:            make(map[string]interface{}),
		openConnection:            openConnection,
	}
}

func (d *Docker) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("docker.json")
	if err != nil {
		return err
	}

	d.l.Lock()
	defer d.l.Unlock()

	type containerInfo struct {
		ID        string
		Name      string
		LastKill  time.Time
		IsIgnored bool
	}

	containers := make([]containerInfo, 0, len(d.containers))

	for _, c := range d.containers {
		containers = append(containers, containerInfo{
			ID:        c.ID(),
			Name:      c.ContainerName(),
			LastKill:  d.lastKill[c.ID()],
			IsIgnored: d.ignoredID[c.ID()],
		})
	}

	networks := make([]string, 0, len(d.bridgeNetworks))
	for net := range d.bridgeNetworks {
		networks = append(networks, net)
	}

	obj := struct {
		WorkedOnce        bool
		ServerAddress     string
		ServerVersion     string
		APIVersion        string
		ReconnectAttempt  int
		Containers        []containerInfo
		LastDestroyedName map[string]time.Time
		AllLastKill       map[string]time.Time
		LastUpdate        time.Time
		BridgeNetworks    []string
	}{
		WorkedOnce:        d.workedOnce,
		ServerAddress:     d.serverAddress,
		ServerVersion:     d.serverVersion,
		APIVersion:        d.apiVersion,
		ReconnectAttempt:  d.reconnectAttempt,
		Containers:        containers,
		LastUpdate:        d.lastUpdate,
		BridgeNetworks:    networks,
		LastDestroyedName: d.lastDestroyedName,
		AllLastKill:       d.lastKill,
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

func (d *Docker) ContainerExists(id string) bool {
	d.l.Lock()
	defer d.l.Unlock()

	_, found := d.containers[id]

	return found
}

// LastUpdate return the last time containers list was updated.
func (d *Docker) LastUpdate() time.Time {
	d.l.Lock()
	defer d.l.Unlock()

	return d.lastUpdate
}

// ProcessWithCache facts.containerRuntime.
func (d *Docker) ProcessWithCache() facts.ContainerRuntimeProcessQuerier {
	return &dockerProcessQuerier{
		d:                   d,
		containerProcessErr: make(map[string]error),
	}
}

// RuntimeFact will return facts from the Docker runtime, like docker_version.
func (d *Docker) RuntimeFact(ctx context.Context, currentFact map[string]string) map[string]string {
	_ = currentFact

	d.l.Lock()
	defer d.l.Unlock()

	if _, err := d.ensureClient(ctx); err != nil {
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

// ServerAddress will return the last server address for which connection succeeded.
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

	container, err := d.getContainers(ctx, maxAge, includeIgnored)
	if err != nil && !d.workedOnce {
		return nil, nil
	}

	return container, err
}

// getContainers return Docker containers.
func (d *Docker) getContainers(ctx context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, err error) {
	if time.Since(d.lastUpdate) >= maxAge {
		err = d.updateContainers(ctx)
		if err != nil {
			return nil, err
		}
	}

	containers = make([]facts.Container, 0, len(d.containers))
	for _, c := range d.containers {
		if includeIgnored || !d.IsContainerIgnored(c) {
			containers = append(containers, c)
		}
	}

	return
}

func (d *Docker) Metrics(_ context.Context, now time.Time) ([]types.MetricPoint, error) {
	_ = now

	return []types.MetricPoint{}, nil
}

func (d *Docker) MetricsMinute(_ context.Context, now time.Time) ([]types.MetricPoint, error) {
	_ = now

	return []types.MetricPoint{}, nil
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

		if strings.Contains(err.Error(), "permission denied") {
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
			d.l.Lock()
			if d.client != nil {
				_ = d.client.Close()
			}
			d.l.Unlock()

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
	ctx, cancel := context.WithTimeout(ctx, dockerTimeout)
	defer cancel()

	d.l.Lock()
	cl, err := d.getClient(ctx)
	d.l.Unlock()

	if err != nil {
		return nil, err
	}

	id, err := cl.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, err
	}

	resp, err := cl.ContainerExecAttach(ctx, id.ID, container.ExecAttachOptions{})
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

func (d *Docker) IsContainerNameRecentlyDeleted(name string) bool {
	d.l.Lock()
	defer d.l.Unlock()

	_, ok := d.lastDestroyedName[name]

	return ok
}

func (d *Docker) ImageTags(ctx context.Context, imageID, _ string) ([]string, error) {
	d.l.Lock()
	defer d.l.Unlock()

	img, err := d.client.ImageInspect(ctx, imageID)
	if err != nil {
		return nil, err
	}

	if len(img.RepoTags) == 0 {
		return nil, fmt.Errorf("%w on image %s", errNoImageTagsFound, imageID)
	}

	tags := make([]string, len(img.RepoTags))

	for i, tag := range img.RepoTags {
		tagSplit := strings.SplitN(tag, ":", 2)
		if len(tagSplit) == 1 {
			tags[i] = "latest"
		} else {
			tags[i] = tagSplit[1]
		}
	}

	return tags, nil
}

// similar to getClient but also check that connection works.
func (d *Docker) ensureClient(ctx context.Context) (cl dockerClient, err error) {
	ctx, cancel := context.WithTimeout(ctx, dockerTimeout)
	defer cancel()

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

	d.l.Unlock()

	if err != nil {
		return err
	}

	// Make sure information is recent enough
	_, _ = d.Containers(ctx, time.Minute, false)

	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()

	eventC, errC := cl.Events(ctx2, events.ListOptions{Since: d.lastEventAt.Format(time.RFC3339Nano)})

	var lastCleanup time.Time

	for {
		if time.Since(lastCleanup) > 10*time.Minute {
			d.l.Lock()

			for k, v := range d.lastKill {
				if time.Since(v) > time.Hour {
					delete(d.lastKill, k)
				}
			}

			for k, v := range d.lastDestroyedName {
				if time.Since(v) > 10*time.Minute {
					delete(d.lastDestroyedName, k)
				}
			}

			lastCleanup = time.Now()

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
					action = events.Action(event.Status)
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

				switch action { //nolint: exhaustive
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

				if strings.HasPrefix(string(action), "health_status:") {
					event.Type = facts.EventTypeHealth

					_, err := d.updateContainer(ctx, cl, actorID)
					if err != nil {
						logger.V(1).Printf("Update of container %v failed (will assume container is removed): %v", actorID, err)

						continue
					}
				}

				d.l.Lock()

				container, found := d.containers[actorID]
				if found {
					event.Container = container
				}

				switch event.Type { //nolint:exhaustive,nolintlint
				case facts.EventTypeDelete:
					if event.Container != nil {
						d.lastDestroyedName[event.Container.ContainerName()] = time.Now()
					}

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
	ctx, cancel := context.WithTimeout(ctx, dockerTimeout)
	defer cancel()

	cl, err := d.getClient(ctx)
	if err != nil {
		return err
	}

	bridgeNetworks := make(map[string]interface{})
	containerAddressOnDockerBridge := make(map[string]string)

	if networks, err := cl.NetworkList(ctx, network.ListOptions{}); err == nil {
		for _, n := range networks {
			if n.Name == "" {
				continue
			}

			if n.Driver == "bridge" {
				bridgeNetworks[n.Name] = nil
			}
		}
	}

	if network, err := cl.NetworkInspect(ctx, "docker_gwbridge", network.InspectOptions{}); err == nil {
		for containerID, endpoint := range network.Containers {
			// IPv4Address is an CIDR (like "172.17.0.4/24")
			address := strings.Split(endpoint.IPv4Address, "/")[0]
			containerAddressOnDockerBridge[containerID] = address
		}
	}

	dockerContainers, err := cl.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return err
	}

	containers := make(map[string]dockerContainer)
	ignoredID := make(map[string]bool)

	for _, c := range dockerContainers {
		inspect, err := cl.ContainerInspect(ctx, c.ID)
		if err != nil && docker.IsErrNotFound(err) {
			continue // the container was deleted between call. Ignore it
		}

		if err != nil {
			return err
		}

		if inspect.ContainerJSONBase == nil {
			logger.V(2).Printf("containerJSON base is empty for %s", c.ID)

			continue
		}

		sortInspect(inspect)

		container := dockerContainer{
			primaryAddress: d.primaryAddress(inspect, bridgeNetworks, containerAddressOnDockerBridge),
			inspect:        inspect,
		}

		containers[c.ID] = container

		if d.IsContainerIgnored(container) {
			ignoredID[c.ID] = true
		}
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	var deletedContainerID []string

	for k := range d.containers {
		if _, ok := containers[k]; !ok {
			deletedContainerID = append(deletedContainerID, k)
		}
	}

	if len(deletedContainerID) > 0 && d.DeletedContainersCallback != nil {
		logger.V(2).Printf(
			"Docker runtime request to delete %d containers (previous container count was %d, new containers count is %d)",
			len(deletedContainerID),
			len(d.containers),
			len(containers),
		)
		d.DeletedContainersCallback(deletedContainerID)
	}

	d.lastUpdate = time.Now()
	d.containers = containers
	d.ignoredID = ignoredID
	d.bridgeNetworks = bridgeNetworks
	d.containerAddressOnDockerBridge = containerAddressOnDockerBridge

	return ctx.Err()
}

func (d *Docker) updateContainer(ctx context.Context, cl dockerClient, containerID string) (dockerContainer, error) {
	ctx, cancel := context.WithTimeout(ctx, dockerTimeout)
	defer cancel()

	var result dockerContainer

	inspect, err := cl.ContainerInspect(ctx, containerID)
	if err != nil {
		return result, err
	}

	if inspect.ContainerJSONBase == nil {
		return result, errNilJSON
	}

	sortInspect(inspect)

	d.l.Lock()
	defer d.l.Unlock()

	container := dockerContainer{
		primaryAddress: d.primaryAddress(inspect, d.bridgeNetworks, d.containerAddressOnDockerBridge),
		inspect:        inspect,
	}

	d.containers[containerID] = container

	if d.IsContainerIgnored(container) {
		d.ignoredID[containerID] = true
	} else {
		delete(d.ignoredID, containerID)
	}

	return d.containers[containerID], nil
}

func (d *Docker) primaryAddress(inspect container.InspectResponse, bridgeNetworks map[string]interface{}, containerAddressOnDockerBridge map[string]string) string {
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
	if d.client == nil {
		var firstErr error

		if len(d.DockerSockets) == 0 {
			d.DockerSockets = []string{""}
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

	d.workedOnce = true

	return d.client, nil
}

type dockerClient interface {
	ContainerExecAttach(ctx context.Context, execID string, config container.ExecAttachOptions) (dockerTypes.HijackedResponse, error)
	ContainerExecCreate(ctx context.Context, container string, config container.ExecOptions) (common.IDResponse, error)
	ContainerInspect(ctx context.Context, container string) (container.InspectResponse, error)
	ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	ContainerTop(ctx context.Context, container string, arguments []string) (container.TopResponse, error)
	Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error)
	ImageInspect(ctx context.Context, imageID string, inspectOpts ...docker.ImageInspectOption) (image.InspectResponse, error)
	NetworkInspect(ctx context.Context, network string, options network.InspectOptions) (network.Inspect, error)
	NetworkList(ctx context.Context, options network.ListOptions) ([]network.Summary, error)
	Ping(ctx context.Context) (dockerTypes.Ping, error)
	ServerVersion(ctx context.Context) (dockerTypes.Version, error)
	Close() error
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
		cl, err = docker.NewClientWithOpts(docker.FromEnv, docker.WithAPIVersionNegotiation())
	} else {
		cl, err = docker.NewClientWithOpts(docker.FromEnv, docker.WithHost(host), docker.WithAPIVersionNegotiation())
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
	case "created":
		return facts.ContainerCreated
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

func sortInspect(inspect container.InspectResponse) {
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
	inspect        container.InspectResponse
	stopped        bool
}

func (c dockerContainer) RuntimeName() string {
	return containerTypes.DockerRuntime
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
	inspect := c.inspect
	if inspect.Config != nil {
		// Making a copy of the config not to modify c.inspect.Config
		cfg := *inspect.Config
		cfg.Env = make([]string, len(inspect.Config.Env))
		copy(cfg.Env, inspect.Config.Env)

		obfuscation.ObfuscateEnv(cfg.Env)

		inspect.Config = &cfg
	}

	result, err := json.Marshal(inspect)
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
	if c.inspect.State == nil {
		return facts.ContainerHealthUnknown, "container don't have State"
	}

	if c.inspect.State.Health == nil {
		return facts.ContainerNoHealthCheck, ""
	}

	if !c.State().IsRunning() {
		return facts.ContainerHealthUnknown, "container is stopped"
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

func (c dockerContainer) ListenAddresses() []facts.ListenAddress {
	if c.inspect.NetworkSettings == nil {
		return nil
	}

	// We only get the ports which are really exposed, not the ports in
	// the EXPOSE line of the Dockerfile because they are often wrong.
	addresses := make([]facts.ListenAddress, 0, len(c.inspect.NetworkSettings.Ports))

	for port, portBindings := range c.inspect.NetworkSettings.Ports {
		if len(portBindings) == 0 {
			continue
		}

		addresses = append(addresses, facts.ListenAddress{
			NetworkFamily: port.Proto(),
			Address:       c.PrimaryAddress(),
			Port:          port.Int(),
		})
	}

	sort.Slice(addresses, func(i, j int) bool {
		return addresses[i].Port < addresses[j].Port
	})

	return addresses
}

func (c dockerContainer) LogPath() string {
	return c.inspect.LogPath
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

func (c dockerContainer) PID() int {
	if c.inspect.State == nil {
		return 0
	}

	return c.inspect.State.Pid
}

var dockerCGroupRE = regexp.MustCompile(
	`(?m:^(0::/\.\./|.*?(/kubepods/.*pod[0-9a-fA-F-]+/|/docker[-/]))(?P<container_id>[0-9a-fA-F]{64}))`,
)

// maybeWrapError wraps the error inside a NoRuntimeError when Docker isn't running.
func maybeWrapError(err error, workedOnce bool) error {
	if workedOnce {
		// If connection worker recently, Docker is likely to be running.
		return err
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	if docker.IsErrConnectionFailed(err) {
		return facts.NewNoRuntimeError(err)
	}

	return err
}

type dockerProcessQuerier struct {
	d                   *Docker
	errListContainers   error
	containers          map[string]dockerContainer
	processesMap        map[int]facts.Process
	containerProcessErr map[string]error
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

func (d *dockerProcessQuerier) fillContainers(ctx context.Context) error {
	if d.containers == nil {
		d.d.l.Lock()

		_, err := d.d.getContainers(ctx, 0, false)
		if err != nil {
			d.containers = make(map[string]dockerContainer)
			err = maybeWrapError(err, d.d.workedOnce)

			d.errListContainers = err

			d.d.l.Unlock()

			return err
		}

		d.containers = make(map[string]dockerContainer, len(d.d.containers))
		for k, v := range d.d.containers {
			d.containers[k] = v
		}

		d.d.l.Unlock()
	}

	if len(d.containers) == 0 && d.errListContainers != nil {
		return d.errListContainers
	}

	return nil
}

func (d *dockerProcessQuerier) processes(ctx context.Context, searchPID int, firstContainer string) error {
	err := d.fillContainers(ctx)
	if err != nil {
		return err
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
	if _, ok := d.containerProcessErr[c.ID()]; ok {
		return d.containerProcessErr[c.ID()]
	}

	top, topWaux, err := d.top(ctx, c)

	switch {
	case err != nil && errdefs.IsNotFound(err):
		d.containerProcessErr[c.ID()] = nil

		return nil
	case err != nil && strings.Contains(err.Error(), "is not running"):
		d.containerProcessErr[c.ID()] = nil

		return nil
	case err != nil:
		d.containerProcessErr[c.ID()] = err

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

	d.containerProcessErr[c.ID()] = nil

	return nil
}

func (d *dockerProcessQuerier) top(ctx context.Context, c facts.Container) (container.TopResponse, container.TopResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, dockerTimeout)
	defer cancel()

	d.d.l.Lock()

	cl, err := d.d.getClient(ctx)
	if err != nil {
		err = maybeWrapError(err, d.d.workedOnce)
		cl = nil
	}

	d.d.l.Unlock()

	if err != nil || cl == nil {
		return container.TopResponse{}, container.TopResponse{}, err
	}

	top, err := cl.ContainerTop(ctx, c.ID(), nil)
	if err != nil {
		return top, container.TopResponse{}, err
	}

	topWaux, err := cl.ContainerTop(ctx, c.ID(), []string{"waux"})

	return top, topWaux, err
}

func decodeDocker(top container.TopResponse, c facts.Container) []facts.Process {
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
				process.MemoryRSS = uint64(v) //nolint: gosec
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

	return 0, fmt.Errorf("%w %#v", errUnknownFormat, psTime)
}

func (d *dockerProcessQuerier) ContainerFromCGroup(ctx context.Context, cgroupData string) (facts.Container, error) {
	containerID := ""

	for _, submatches := range dockerCGroupRE.FindAllStringSubmatch(cgroupData, -1) {
		if containerID == "" {
			containerID = submatches[dockerCGroupRE.SubexpIndex("container_id")]
		} else if containerID != submatches[dockerCGroupRE.SubexpIndex("container_id")] {
			// different value for the same PID. Abort detection of container ID from cgroup
			return nil, fmt.Errorf("%w: more than one ID from cgroup: %v != %v", ErrDockerUnexcepted, containerID, submatches[2])
		}
	}

	if containerID == "" {
		return nil, nil //nolint: nilnil
	}

	if err := d.fillContainers(ctx); err != nil {
		// When the cgroup matched the regexp, we don't want to return NoRuntimeError but
		// we want to return ErrContainerDoesNotExists.
		if !errors.As(err, &facts.NoRuntimeError{}) {
			return nil, err
		}
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

	return nil, nil //nolint: nilnil
}
