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

package facts

import (
	"agentgo/logger"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	docker "github.com/docker/docker/client"
	"github.com/shirou/gopsutil/process"
)

// DockerProvider provider information about Docker & Docker containers
type DockerProvider struct {
	l sync.Mutex

	client           *docker.Client
	reconnectAttempt int
	dockerVersion    string
	dockerAPIVersion string

	notifyC     chan DockerEvent
	lastEventAt time.Time

	containers map[string]Container
	lastKill   map[string]time.Time
	ignoredID  map[string]interface{}
	lastUpdate time.Time
}

// DockerEvent is a simplified version of Docker Event.Message
// Those event only happed on Container.
type DockerEvent struct {
	Action  string
	ActorID string
}

// Container wraps the Docker inspect values and provide few accessor to useful fields
type Container struct {
	primaryAddress string
	inspect        types.ContainerJSON
}

// NewDocker creates a new Docker provider which must be started with Run() method
func NewDocker() *DockerProvider {
	return &DockerProvider{
		notifyC:     make(chan DockerEvent),
		lastEventAt: time.Now(),
		lastKill:    make(map[string]time.Time),
	}
}

// Containers returns the list of container present on this system.
//
// It may use a cached value as old as maxAge
//
// If includeIgnored is false, Containers that has bleemeo.enable=false labels
// are not listed.
func (d *DockerProvider) Containers(ctx context.Context, maxAge time.Duration, includeIgnored bool) (containers []Container, err error) {
	d.l.Lock()
	defer d.l.Unlock()

	if time.Since(d.lastUpdate) > maxAge {
		err = d.updateContainers(ctx)
		if err != nil {
			return
		}
	}

	containers = make([]Container, 0, len(d.containers))
	for _, c := range d.containers {
		if includeIgnored || !c.Ignored() {
			containers = append(containers, c)
		}
	}
	return
}

// Container returns the container matching given Container ID.
//
// The Container ID must be the full ID (not only first 8 char).
//
// The information will come from cache exclusively. Use Containers() to refresh the cache if needed.
func (d *DockerProvider) Container(containerID string) (container Container, found bool) {
	d.l.Lock()
	defer d.l.Unlock()
	c, ok := d.containers[containerID]
	return c, ok
}

// ContainerEnv returns the container environment of given container ID.
//
// The Container ID must be the full ID (not only first 8 char).
//
// The information will come from cache exclusively. Use Containers() to refresh the cache if needed.
func (d *DockerProvider) ContainerEnv(containerID string) (env []string) {
	d.l.Lock()
	defer d.l.Unlock()
	c, ok := d.containers[containerID]
	if !ok {
		return nil
	}
	return c.Env()
}

// DockerFact returns few facts from Docker. It should be usable as FactCallback
func (d *DockerProvider) DockerFact(ctx context.Context, currentFact map[string]string) map[string]string {
	d.l.Lock()
	defer d.l.Unlock()

	// Just call getClient to ensure connection is established. When connecting
	// fields version and apiVersion are updated
	_, err := d.getClient(ctx)
	if err != nil {
		return nil
	}

	if d.dockerVersion == "" {
		return nil
	}

	facts := make(map[string]string)
	facts["docker_version"] = d.dockerVersion
	facts["docker_api_version"] = d.dockerAPIVersion
	return facts
}

// Events returns the channel on which Docker events are sent
func (d *DockerProvider) Events() <-chan DockerEvent {
	return d.notifyC
}

// HasConnection returns whether or not a connection is currently established with Docker.
//
// It use the cached connection, no new connection are established. Use Containers() to establish new connection if needed.
// The existing connection from the cache is tested. So HasConnection may be used to validate that Docker is still available.
func (d *DockerProvider) HasConnection(ctx context.Context) bool {
	d.l.Lock()
	defer d.l.Unlock()

	if d.client == nil {
		return false
	}
	if _, err := d.client.Ping(ctx); err != nil {
		d.client = nil
		d.dockerVersion = ""
		d.dockerAPIVersion = ""
		return false
	}
	return true
}

// Run will run connect and listen to Docker event until context is cancelled
//
// Any error (unable to connect due to permission issue or Docker down) are not returned
// by Run but could be retrieved with LastError
func (d *DockerProvider) Run(ctx context.Context) error {
	var lastErrorNotify time.Time
	var sleepDelay float64
	for {
		err := d.run(ctx)
		func() {
			d.l.Lock()
			defer d.l.Unlock()
			d.reconnectAttempt++
			if err != nil {
				lastErrorNotify = notifyError(err, lastErrorNotify, d.reconnectAttempt)
			}
			sleepDelay = 5 * math.Pow(2, float64(d.reconnectAttempt))
			if sleepDelay > 60 {
				sleepDelay = 60
			}
		}()
		select {
		case <-time.After(time.Duration(sleepDelay) * time.Second):
		case <-ctx.Done():
			close(d.notifyC)
			return nil
		}
	}
}

// ContainerLastKill return the last time a kill event was seen for given container ID
func (d *DockerProvider) ContainerLastKill(containerID string) time.Time {
	d.l.Lock()
	defer d.l.Unlock()
	return d.lastKill[containerID]
}

// Command returns the command run in the container
func (c Container) Command() string {
	if c.inspect.Config == nil {
		return ""
	}
	return strings.Join(c.inspect.Config.Cmd, " ")
}

// CreatedAt returns the date of container creation
func (c Container) CreatedAt() time.Time {
	var result time.Time
	result, err := time.Parse(time.RFC3339Nano, c.inspect.Created)
	if err != nil {
		return result
	}
	return result
}

// Env returns the Container environment
func (c Container) Env() []string {
	if c.inspect.Config == nil {
		return make([]string, 0)
	}
	return c.inspect.Config.Env
}

// ID returns the Container ID
func (c Container) ID() string {
	return c.inspect.ID
}

// Ignored returns true if this container should be ignored by Bleemeo agent
func (c Container) Ignored() bool {
	return ignoreContainer(c.inspect)
}

// IsRunning returns true if this container is running
func (c Container) IsRunning() bool {
	return c.inspect.State != nil && c.inspect.State.Running
}

// Image returns the Docker container image
func (c Container) Image() string {
	if c.inspect.Config == nil {
		return c.inspect.Image
	}
	return c.inspect.Config.Image
}

// Inspect returns the Docker ContainerJSON object
func (c Container) Inspect() types.ContainerJSON {
	return c.inspect
}

// InspectJSON returns the JSON of Docker inspect
func (c Container) InspectJSON() string {
	result, err := json.Marshal(c.inspect)
	if err != nil {
		return ""
	}
	return string(result)
}

// Labels returns labels associated with the container
func (c Container) Labels() map[string]string {
	if c.inspect.Config == nil {
		return nil
	}
	return c.inspect.Config.Labels
}

// ListenAddresses returns the addresseses this container listen on
func (c Container) ListenAddresses() []ListenAddress {
	if c.inspect.Config == nil {
		return nil
	}
	if c.PrimaryAddress() == "" {
		return nil
	}
	exposedPorts := make([]ListenAddress, 0)
	for v := range c.inspect.Config.ExposedPorts {
		tmp := strings.Split(string(v), "/")
		if len(tmp) != 2 {
			continue
		}
		portStr := tmp[0]
		protocol := tmp[1]
		port, err := strconv.ParseInt(portStr, 10, 0)
		if err != nil {
			logger.V(1).Printf("unable to parse port %#v: %v", portStr, err)
			continue
		}
		exposedPorts = append(exposedPorts, ListenAddress{NetworkFamily: protocol, Address: c.PrimaryAddress(), Port: int(port)})
	}
	return exposedPorts
}

// Name returns the Container name
func (c Container) Name() string {
	if c.inspect.Name[0] == '/' {
		return c.inspect.Name[1:]
	}
	return c.inspect.Name
}

// PrimaryAddress returns the address where the container may be reachable from host
//
// This address may not exists. The returned address could be empty or an IP only
// accessible from an overlay network.
func (c Container) PrimaryAddress() string {
	return c.primaryAddress
}

// StartedAt returns the date of last container start
func (c Container) StartedAt() time.Time {
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

// State returns the container Status like "running", "exited", ...
func (c Container) State() string {
	if c.inspect.State == nil {
		return ""
	}
	return c.inspect.State.Status
}

// FinishedAt returns the date of last container stop
func (c Container) FinishedAt() time.Time {
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

func ignoreContainer(inspect types.ContainerJSON) bool {
	if inspect.Config == nil {
		return false
	}
	label := strings.ToLower(inspect.Config.Labels["bleemeo.enable"])
	switch label {
	case "0", "off", "false", "no":
		return true
	default:
		return false
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

func notifyError(err error, lastErrorNotify time.Time, reconnectAttempt int) time.Time {
	if time.Since(lastErrorNotify) < time.Hour && reconnectAttempt > 1 {
		return lastErrorNotify
	}
	if strings.Contains(fmt.Sprintf("%v", err), "permission denied") {
		logger.Printf(
			"The agent is not permitted to access Docker, the Docker integration will be disabled.",
		)
		logger.Printf(
			"'adduser bleemeo docker' and a restart of the Agent should fix this issue",
		)
	} else if isDockerRunning() {
		logger.Printf("Unable to contact Docker: %v", err)
	}
	return time.Now()
}

func primaryAddress(inspect types.ContainerJSON, bridgeNetworks map[string]interface{}, containerAddressOnDockerBridge map[string]string) string {
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

	// TODO try k8s
	return ""
}

func (d *DockerProvider) getClient(ctx context.Context) (cl *docker.Client, err error) {
	if d.client == nil {
		cl, err = docker.NewClientWithOpts(docker.FromEnv)
		if err != nil {
			return
		}
	} else {
		cl = d.client
	}
	if _, err = cl.Ping(ctx); err != nil {
		d.client = nil
		d.dockerVersion = ""
		d.dockerAPIVersion = ""
		return
	}
	if d.client == nil {
		// New connection, update dockerVersion/dockerAPIVersion
		v, err := cl.ServerVersion(ctx)
		if err == nil {
			d.dockerAPIVersion = v.APIVersion
			d.dockerVersion = v.Version
		}
	}
	d.client = cl
	d.reconnectAttempt = 0
	return
}

func (d *DockerProvider) top(ctx context.Context, containerID string) (top container.ContainerTopOKBody, topWaux container.ContainerTopOKBody, err error) {
	d.l.Lock()
	defer d.l.Unlock()
	cl, err := d.getClient(ctx)
	if err != nil {
		return
	}

	top, err = cl.ContainerTop(ctx, containerID, nil)
	if err != nil {
		return
	}
	topWaux, err = cl.ContainerTop(ctx, containerID, []string{"waux"})
	return
}

func (d *DockerProvider) updateContainers(ctx context.Context) error {
	cl, err := d.getClient(ctx)
	if err != nil {
		return err
	}

	bridgeNetworks := make(map[string]interface{})
	containerAddressOnDockerBridge := make(map[string]string)

	if networks, err := cl.NetworkList(ctx, types.NetworkListOptions{}); err == nil {
		for _, n := range networks {
			if n.Name == "" {
				continue
			}
			if n.Driver == "bridge" {
				bridgeNetworks[n.Name] = nil
			}
		}
	}
	if network, err := cl.NetworkInspect(ctx, "docker_gwbridge", types.NetworkInspectOptions{}); err == nil {
		for containerID, endpoint := range network.Containers {
			// IPv4Address is an CIDR (like "172.17.0.4/24")
			address := strings.Split(endpoint.IPv4Address, "/")[0]
			containerAddressOnDockerBridge[containerID] = address
		}
	}

	dockerContainers, err := cl.ContainerList(ctx, types.ContainerListOptions{All: true})
	if err != nil {
		return err
	}
	containers := make(map[string]Container)
	ignoredID := make(map[string]interface{})
	for _, c := range dockerContainers {
		inspect, err := cl.ContainerInspect(ctx, c.ID)
		if err != nil && docker.IsErrNotFound(err) || inspect.ContainerJSONBase == nil {
			continue // the container was deleted between call. Ignore it
		}
		if ignoreContainer(inspect) {
			ignoredID[c.ID] = nil
		}
		sortInspect(inspect)
		containers[c.ID] = Container{
			primaryAddress: primaryAddress(inspect, bridgeNetworks, containerAddressOnDockerBridge),
			inspect:        inspect,
		}
	}
	d.lastUpdate = time.Now()
	d.containers = containers
	d.ignoredID = ignoredID
	return nil
}

func (d *DockerProvider) run(ctx context.Context) (err error) {
	var cl *docker.Client
	func() {
		d.l.Lock()
		defer d.l.Unlock()
		cl, err = d.getClient(ctx)
	}()
	if err != nil {
		return
	}

	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()
	eventC, errC := cl.Events(ctx2, types.EventsOptions{Since: d.lastEventAt.Format(time.RFC3339Nano)})

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
		case event := <-eventC:
			d.lastEventAt = time.Unix(event.Time, event.TimeNano)
			if event.Type == "" || event.Type == "container" {
				se := DockerEvent{Action: event.Action, ActorID: event.Actor.ID}
				if event.Action == "" {
					// Docker before 1.10 didn't had Action
					se.Action = event.Status
				}
				if event.Actor.ID == "" {
					// Docker before 1.10 didn't had Actor
					se.ActorID = event.ID
				}
				ok := d.isIgnored(se.ActorID)
				if ok {
					continue
				}
				if se.Action == "kill" {
					d.l.Lock()
					d.lastKill[se.ActorID] = time.Now()
					d.l.Unlock()
				}
				select {
				case d.notifyC <- se:
				case <-ctx.Done():
				}
			}
		case err = <-errC:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (d *DockerProvider) isIgnored(containerID string) bool {
	d.l.Lock()
	defer d.l.Unlock()
	_, ok := d.ignoredID[containerID]
	return ok
}

func sortInspect(inspect types.ContainerJSON) {
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
