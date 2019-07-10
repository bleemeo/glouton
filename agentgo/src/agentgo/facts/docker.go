package facts

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
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
		if includeIgnored || !c.Ignore() {
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

// ContainerNetworkInfo returns the IP addresses and listening port of provided containerID
//
// The information will come from cache exclusively. Use Containers() to refresh the cache if needed.
func (d *DockerProvider) ContainerNetworkInfo(containerID string) (ipAddress string, listenAddresses []net.Addr) {
	d.l.Lock()
	defer d.l.Unlock()
	c, ok := d.containers[containerID]
	if !ok {
		return
	}
	if c.inspect.Config == nil {
		return
	}
	if c.PrimaryAddress() == "" {
		return
	}
	exposedPorts := make([]net.Addr, 0)
	for v := range c.inspect.Config.ExposedPorts {
		tmp := strings.Split(string(v), "/")
		if len(tmp) != 2 {
			continue
		}
		portStr := tmp[0]
		protocol := tmp[1]
		port, err := strconv.ParseInt(portStr, 10, 0)
		if err != nil {
			log.Printf("DBG: unable to parse port %#v: %v", portStr, err)
			continue
		}
		exposedPorts = append(exposedPorts, listenAddress{network: protocol, address: c.PrimaryAddress(), port: int(port)})
	}
	return c.PrimaryAddress(), exposedPorts
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

// Run will run connect and listen to Docker event until context is cancelled
//
// Any error (unable to connect due to permission issue or Docker down) are not returned
// by Run but could be retrieved with LastError
func (d *DockerProvider) Run(ctx context.Context) {
	var lastErrorNotify time.Time
	for {
		err := d.run(ctx)
		d.l.Lock()
		d.reconnectAttempt++
		if err != nil {
			lastErrorNotify = notifyError(err, lastErrorNotify, d.reconnectAttempt)
		}
		sleepDelay := 5 * math.Pow(2, float64(d.reconnectAttempt))
		if sleepDelay > 5 {
			sleepDelay = 5
		}
		d.l.Unlock()
		select {
		case <-time.After(time.Duration(sleepDelay) * time.Second):
		case <-ctx.Done():
			close(d.notifyC)
			return
		}
	}
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

// ID returns the Container ID
func (c Container) ID() string {
	return c.inspect.ID
}

// Ignore returns true if this container should be ignored by Bleemeo agent
func (c Container) Ignore() bool {
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
		log.Println(
			"The agent is not permitted to access Docker, the Docker integration will be disabled.",
		)
		log.Println(
			"'adduser bleemeo docker' and a restart of the Agent should fix this issue",
		)
	} else if isDockerRunning() {
		log.Printf("Unable to contact Docker: %v", err)
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
	cl, err := d.getClient(ctx)
	if err != nil {
		d.l.Unlock()
		return
	}
	d.l.Unlock()

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

	d.l.Unlock()

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
		d.l.Lock()
		return err
	}
	containers := make(map[string]Container)
	ignoredID := make(map[string]interface{})
	for _, c := range dockerContainers {
		inspect, err := cl.ContainerInspect(ctx, c.ID)
		if err != nil && docker.IsErrNotFound(err) {
			continue // the container was deleted between call. Ignore it
		}
		if ignoreContainer(inspect) {
			ignoredID[c.ID] = nil
		}
		containers[c.ID] = Container{
			primaryAddress: primaryAddress(inspect, bridgeNetworks, containerAddressOnDockerBridge),
			inspect:        inspect,
		}
	}
	d.l.Lock()
	d.lastUpdate = time.Now()
	d.containers = containers
	d.ignoredID = ignoredID
	return nil
}

func (d *DockerProvider) run(ctx context.Context) (err error) {
	d.l.Lock()
	cl, err := d.getClient(ctx)
	d.l.Unlock()
	if err != nil {
		return
	}

	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()
	eventC, errC := cl.Events(ctx2, types.EventsOptions{Since: d.lastEventAt.Format(time.RFC3339Nano)})

	for {
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
				d.l.Lock()
				_, ok := d.ignoredID[se.ActorID]
				d.l.Unlock()
				if ok {
					continue
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
