package facts

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	docker "github.com/docker/docker/client"
	"github.com/shirou/gopsutil/process"
)

// DockerProvider provider information about Docker & Docker containers
type DockerProvider struct {
	l sync.Mutex

	client           *docker.Client
	reconnectAttempt int

	notifyC     chan DockerEvent
	lastEventAt time.Time

	containers          map[string]Container
	ignoredID           map[string]interface{}
	lastContainerUpdate time.Time
}

// DockerEvent is a simplified version of Docker Event.Message
// Those event only happed on Container.
type DockerEvent struct {
	Action  string
	ActorID string
}

// Container wraps the Docker inspect values and provide few accessor to useful fields
type Container struct {
	inspect types.ContainerJSON
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
func (d *DockerProvider) Containers(ctx context.Context, maxAge time.Duration) (containers []Container, err error) {
	d.l.Lock()
	defer d.l.Unlock()

	if time.Since(d.lastContainerUpdate) > maxAge {
		err = d.updateContainers(ctx)
		if err != nil {
			return
		}
	}

	containers = make([]Container, 0, len(d.containers))
	for _, c := range d.containers {
		containers = append(containers, c)
	}
	return
}

// Container returns the container matching given Container ID.
//
// The Container ID must be the full ID (not only first 8 char).
//
// It may return a existing value from cache as old as maxAge. If not found in the cache,
// a direct call to Docker is always performed.
//
// On error (including not found) the Docker error is returned.
//
// Note: this function may return an ignored container (due to bleemeo.enable=false label)
func (d *DockerProvider) Container(ctx context.Context, containerID string, maxAge time.Duration) (container Container, err error) {
	d.l.Lock()
	if time.Since(d.lastContainerUpdate) <= maxAge {
		if c, ok := d.containers[containerID]; ok {
			d.l.Unlock()
			return c, nil
		}
	}
	cl, err := d.getClient(ctx)
	if err != nil {
		return
	}
	d.l.Unlock()

	inspect, err := cl.ContainerInspect(ctx, containerID)
	if err != nil {
		return
	}
	container = Container{inspect: inspect}
	return
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
		return
	}
	d.client = cl
	d.reconnectAttempt = 0
	return
}

func (d *DockerProvider) updateContainers(ctx context.Context) error {
	cl, err := d.getClient(ctx)
	if err != nil {
		return err
	}

	d.l.Unlock()
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
			continue
		}
		containers[c.ID] = Container{
			inspect: inspect,
		}
	}
	d.l.Lock()
	d.lastContainerUpdate = time.Now()
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
