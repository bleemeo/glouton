package facts

import (
	"context"
	"errors"
	"glouton/logger"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
)

const (
	ignoredPortLabel           = "glouton.check.ignore.port."
	containerEnableLabel       = "glouton.enable"
	containerEnableLegacyLabel = "bleemeo.enable"
)

type Container interface {
	Annotations() map[string]string
	Command() []string
	ContainerJSON() string
	ContainerName() string
	CreatedAt() time.Time
	Environment() map[string]string
	FinishedAt() time.Time
	Health() (ContainerHealth, string)
	ID() string
	ImageID() string
	ImageName() string
	Labels() map[string]string
	ListenAddresses() (addresses []ListenAddress, explicit bool)
	PodName() string
	PodNamespace() string
	PrimaryAddress() string
	StartedAt() time.Time
	State() ContainerState
	StoppedAndReplaced() bool
}

type ContainerState int

const (
	ContainerUnknown ContainerState = iota
	ContainerCreated
	ContainerRunning
	ContainerRestarting
	ContainerStopped
)

func (st ContainerState) IsRunning() bool {
	return st == ContainerRunning
}

func (st ContainerState) String() string {
	switch st {
	case ContainerRunning:
		return "running"
	case ContainerCreated:
		return "created"
	case ContainerStopped:
		return "stopped"
	case ContainerRestarting:
		return "restarting"
	default:
		return "unknown"
	}
}

type ContainerEvent struct {
	Type        EventType
	ContainerID string
	Container   Container
}

type ContainerHealth int

const (
	ContainerHealthUnknown ContainerHealth = iota
	ContainerStarting
	ContainerHealthy
	ContainerUnhealthy
	ContainerNoHealthCheck
)

type EventType int

const (
	EventTypeUnknown EventType = iota
	EventTypeCreate
	EventTypeStart
	EventTypeKill
	EventTypeStop
	EventTypeDelete
	EventTypeHealth
)

var (
	ErrContainerDoesNotExists = errors.New("the container doesn't exist but process seems to belong to a container")
)

type containerRuntime interface {
	ProcessWithCache() ContainerRuntimeProcessQuerier
}

type ContainerRuntimeProcessQuerier interface {
	// Processes could be unimplemented. Return empty processes and nil as error
	Processes(ctx context.Context) ([]Process, error)
	ContainerFromCGroup(ctx context.Context, cgroupData string) (Container, error)
	ContainerFromPID(ctx context.Context, parentContainerID string, pid int) (Container, error)
}

// ContainerIgnored return true if a container is ignored by Glouton.
func ContainerIgnored(c Container) bool {
	e, _ := ContainerEnabled(c)
	return !e
}

// ContainerEnabled returns true if this container should be monitored by Glouton.
// Also return a 2nd boolean telling is this container is explicitly enabled or if it's the default.
func ContainerEnabled(c Container) (enabled bool, explicit bool) {
	return containerEnabledFromLabels(LabelsAndAnnotations(c))
}

func containerEnabledFromLabels(labels map[string]string) (enabled bool, explicit bool) {
	label := labels[containerEnableLabel]
	if label == "" {
		label = labels[containerEnableLegacyLabel]
	}

	if label == "" && labels["io.kubernetes.docker.type"] == "podsandbox" {
		return false, true
	}

	if label == "" && labels["io.cri-containerd.kind"] == "sandbox" {
		return false, true
	}

	return string2Boolean(label, true)
}

func string2Boolean(s string, defaultValue bool) (bool, bool) {
	switch strings.ToLower(s) {
	case "0", "off", "false", "no":
		return false, true
	case "1", "on", "true", "yes":
		return true, true
	default:
		return defaultValue, false
	}
}

// ContainerIgnoredFromLabels return true if a container is ignored by Glouton.
func ContainerIgnoredFromLabels(labels map[string]string) bool {
	e, _ := containerEnabledFromLabels(labels)
	return !e
}

// LabelsAndAnnotations return labels and annotations merged.
// Annotation take precedence over labels.
func LabelsAndAnnotations(c Container) map[string]string {
	labels := c.Labels()
	annotations := c.Annotations()

	if annotations == nil {
		return labels
	}

	// Copy labels to avoid mutating them
	results := make(map[string]string, len(annotations))

	for k, v := range labels {
		results[k] = v
	}

	for k, v := range annotations {
		results[k] = v
	}

	return results
}

// ContainerIgnoredPorts return ports ignored by a container.
func ContainerIgnoredPorts(c Container) map[int]bool {
	merged := LabelsAndAnnotations(c)
	ignoredPort := make(map[int]bool)

	for k, v := range merged {
		if !strings.HasPrefix(k, ignoredPortLabel) {
			continue
		}

		ignore, _ := string2Boolean(v, false)
		portStr := strings.TrimPrefix(k, ignoredPortLabel)

		port, err := strconv.ParseInt(portStr, 10, 0)
		if err != nil {
			logger.V(1).Printf("Label %#v of %s containt invalid port: %v", k, c.ContainerName(), err)

			continue
		}

		ignoredPort[int(port)] = ignore
	}

	return ignoredPort
}

type FakeContainer struct {
	FakeAnnotations             map[string]string
	FakeCommand                 []string
	FakeContainerJSON           string
	FakeContainerName           string
	FakeCreatedAt               time.Time
	FakeEnvironment             map[string]string
	FakeFinishedAt              time.Time
	FakeHealth                  ContainerHealth
	FakeHealthMessage           string
	FakeID                      string
	FakeImageID                 string
	FakeImageName               string
	FakeLabels                  map[string]string
	FakeListenAddresses         []ListenAddress
	FakePodName                 string
	FakePodNamespace            string
	FakePrimaryAddress          string
	FakeStartedAt               time.Time
	FakeState                   ContainerState
	FakeListenAddressesExplicit bool
	FakeStoppedAndReplaced      bool

	// Test* flags are only used by tests
	TestIgnored bool
	TestHasPod  bool
}

func (c FakeContainer) Annotations() map[string]string {
	return c.FakeAnnotations
}
func (c FakeContainer) Command() []string {
	return c.FakeCommand
}
func (c FakeContainer) ContainerJSON() string {
	return c.FakeContainerJSON
}
func (c FakeContainer) ContainerName() string {
	return c.FakeContainerName
}
func (c FakeContainer) CreatedAt() time.Time {
	return c.FakeCreatedAt
}
func (c FakeContainer) Environment() map[string]string {
	return c.FakeEnvironment
}
func (c FakeContainer) FinishedAt() time.Time {
	return c.FakeFinishedAt
}
func (c FakeContainer) Health() (ContainerHealth, string) {
	return c.FakeHealth, c.FakeHealthMessage
}
func (c FakeContainer) ID() string {
	return c.FakeID
}
func (c FakeContainer) ImageID() string {
	return c.FakeImageID
}
func (c FakeContainer) ImageName() string {
	return c.FakeImageName
}
func (c FakeContainer) Labels() map[string]string {
	return c.FakeLabels
}
func (c FakeContainer) ListenAddresses() (addresses []ListenAddress, explicit bool) {
	return c.FakeListenAddresses, c.FakeListenAddressesExplicit
}
func (c FakeContainer) PodName() string {
	return c.FakePodName
}
func (c FakeContainer) PodNamespace() string {
	return c.FakePodNamespace
}
func (c FakeContainer) PrimaryAddress() string {
	return c.FakePrimaryAddress
}
func (c FakeContainer) StartedAt() time.Time {
	return c.FakeStartedAt
}
func (c FakeContainer) State() ContainerState {
	return c.FakeState
}
func (c FakeContainer) StoppedAndReplaced() bool {
	return c.FakeStoppedAndReplaced
}

func (c FakeContainer) Diff(other Container) string { // nolint: gocyclo
	diffs := []string{}

	if diff := cmp.Diff(other.Annotations(), c.FakeAnnotations); c.FakeAnnotations != nil && diff != "" {
		diffs = append(diffs, "annotations: "+diff)
	}

	if diff := cmp.Diff(other.Command(), c.FakeCommand); c.FakeCommand != nil && diff != "" {
		diffs = append(diffs, "command: "+diff)
	}

	if diff := cmp.Diff(other.ContainerJSON(), c.FakeContainerJSON); c.FakeContainerJSON != "" && diff != "" {
		diffs = append(diffs, "containerJSON: "+diff)
	}

	if diff := cmp.Diff(other.ContainerName(), c.FakeContainerName); c.FakeContainerName != "" && diff != "" {
		diffs = append(diffs, "container name: "+diff)
	}

	if diff := cmp.Diff(other.CreatedAt(), c.FakeCreatedAt); !c.FakeCreatedAt.IsZero() && diff != "" {
		diffs = append(diffs, "createdAt: "+diff)
	}

	if diff := cmp.Diff(other.Environment(), c.FakeEnvironment); c.FakeEnvironment != nil && diff != "" {
		diffs = append(diffs, "environment: "+diff)
	}

	if diff := cmp.Diff(other.FinishedAt(), c.FakeFinishedAt); !c.FakeFinishedAt.IsZero() && diff != "" {
		diffs = append(diffs, "finishedAt: "+diff)
	}

	if c.FakeHealth != 0 || c.FakeHealthMessage != "" {
		h, msg := other.Health()
		if diff := cmp.Diff(h, c.FakeHealth); diff != "" {
			diffs = append(diffs, "health: "+diff)
		}

		if diff := cmp.Diff(msg, c.FakeHealthMessage); diff != "" {
			diffs = append(diffs, "healthMessage: "+diff)
		}
	}

	if diff := cmp.Diff(other.ID(), c.FakeID); c.FakeID != "" && diff != "" {
		diffs = append(diffs, "ID: "+diff)
	}

	if diff := cmp.Diff(other.ImageID(), c.FakeImageID); c.FakeImageID != "" && diff != "" {
		diffs = append(diffs, "ImageID: "+diff)
	}

	if diff := cmp.Diff(other.ImageName(), c.FakeImageName); c.FakeImageName != "" && diff != "" {
		diffs = append(diffs, "ImageName: "+diff)
	}

	if diff := cmp.Diff(other.Labels(), c.FakeLabels); c.FakeLabels != nil && diff != "" {
		diffs = append(diffs, diff)
	}

	if c.FakeListenAddresses != nil {
		addresses, explicit := other.ListenAddresses()
		if diff := cmp.Diff(addresses, c.FakeListenAddresses); diff != "" {
			diffs = append(diffs, "ListenAddresses: "+diff)
		}

		if diff := cmp.Diff(explicit, c.FakeListenAddressesExplicit); diff != "" {
			diffs = append(diffs, "ListenAddresses, explicit: "+diff)
		}
	}

	if diff := cmp.Diff(other.PodName(), c.FakePodName); c.FakePodName != "" && diff != "" {
		diffs = append(diffs, "PodName: "+diff)
	}

	if diff := cmp.Diff(other.PodNamespace(), c.FakePodNamespace); c.FakePodNamespace != "" && diff != "" {
		diffs = append(diffs, "PodNamespace: "+diff)
	}

	if diff := cmp.Diff(other.PrimaryAddress(), c.FakePrimaryAddress); c.FakePrimaryAddress != "" && diff != "" {
		diffs = append(diffs, "PrimaryAddress: "+diff)
	}

	if diff := cmp.Diff(other.StartedAt(), c.FakeStartedAt); !c.FakeStartedAt.IsZero() && diff != "" {
		diffs = append(diffs, "StartedAt: "+diff)
	}

	if diff := cmp.Diff(other.State(), c.FakeState); c.FakeState != 0 && diff != "" {
		diffs = append(diffs, "State: "+diff)
	}

	if diff := cmp.Diff(other.StoppedAndReplaced(), c.FakeStoppedAndReplaced); diff != "" {
		diffs = append(diffs, "StoppedAndReplaced: "+diff)
	}

	return strings.Join(diffs, "\n")
}
