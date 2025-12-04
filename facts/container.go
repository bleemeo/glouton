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

package facts

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bleemeo/glouton/logger"

	"github.com/google/go-cmp/cmp"
)

const (
	ignoredPortLabel           = "glouton.check.ignore.port."
	containerEnableLabel       = "glouton.enable"
	containerEnableLegacyLabel = "bleemeo.enable"
)

// Container is an interface that defines all the information retrievable of a container.
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
	ImageTags(ctx context.Context) ([]string, error)
	// Labels returns the container labels. They must not be mutated.
	Labels() map[string]string
	ListenAddresses() []ListenAddress
	LogPath() string
	PodName() string
	PodNamespace() string
	PrimaryAddress() string
	StartedAt() time.Time
	State() ContainerState
	StoppedAndReplaced() bool
	RuntimeName() string
	PID() int
}

// ContainerState is the container lifecycle state.
type ContainerState int

const (
	// ContainerUnknown is the default container state.
	ContainerUnknown ContainerState = iota
	// ContainerCreated is the state in which a container was just created.
	ContainerCreated
	// ContainerRunning is the state in which a container is currently running.
	ContainerRunning
	// ContainerRestarting is the state in which a container is restarting.
	ContainerRestarting
	// ContainerStopped is the state in which a container was stopped.
	ContainerStopped
)

// IsRunning checks if a container is currently in a running state.
func (st ContainerState) IsRunning() bool {
	return st == ContainerRunning
}

// String returns the container state as string.
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
	case ContainerUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// ContainerEvent encapsulates an event related to a container.
type ContainerEvent struct {
	Type        EventType
	ContainerID string
	Container   Container
}

// ContainerHealth is the health status.
type ContainerHealth int

const (
	// ContainerHealthUnknown is the default state value.
	ContainerHealthUnknown ContainerHealth = iota
	// ContainerStarting is the value for which the container status is starting.
	ContainerStarting
	// ContainerHealthy is the value for which the container status is healthy.
	ContainerHealthy
	// ContainerUnhealthy is the value for which the container status is unhealthy.
	ContainerUnhealthy
	// ContainerNoHealthCheck is the value for which the container status indicates not health check.
	ContainerNoHealthCheck
)

// EventType is the container event.
type EventType int

const (
	// EventTypeUnknown is the default event value.
	EventTypeUnknown EventType = iota
	// EventTypeCreate is the event type for which the event is "Create".
	EventTypeCreate
	// EventTypeStart is the event type for which the event is "Start".
	EventTypeStart
	// EventTypeKill is the event type for which the event is "Kill".
	EventTypeKill
	// EventTypeStop is the event type for which the event is "Stop".
	EventTypeStop
	// EventTypeDelete is the event type for which the event is "Delete".
	EventTypeDelete
	// EventTypeHealth is the event type for which the event is "Health".
	EventTypeHealth
)

// ErrContainerDoesNotExists is the default error value when a container does not exists.
var ErrContainerDoesNotExists = errors.New("the container doesn't exist but process seems to belong to a container")

// NoRuntimeError wraps an error from a container when the error likely indicates that runtime isn't running.
type NoRuntimeError struct {
	err error
}

func (e NoRuntimeError) Error() string {
	return e.err.Error()
}

func (e NoRuntimeError) Unwrap() error {
	return e.err
}

func NewNoRuntimeError(err error) error {
	return NoRuntimeError{err: err}
}

type containerRuntime interface {
	ProcessWithCache() ContainerRuntimeProcessQuerier
}

// ContainerRuntimeProcessQuerier encapsulates queries about containers information.
type ContainerRuntimeProcessQuerier interface {
	// Processes could be unimplemented. Return empty processes and nil as error
	Processes(ctx context.Context) ([]Process, error)
	ContainerFromCGroup(ctx context.Context, cgroupData string) (Container, error)
	ContainerFromPID(ctx context.Context, parentContainerID string, pid int) (Container, error)
}

type ContainerFilter struct {
	DisabledByDefault bool
	AllowList         []string
	DenyList          []string
}

// ContainerIgnored return true if a container is ignored by Glouton.
func (cf ContainerFilter) ContainerIgnored(c Container) bool {
	// created container are ignored because there should not stay in this state
	// a long time. So wait for this container to because started or be deleted.
	if c.State() == ContainerCreated {
		return true
	}

	e, _ := cf.ContainerEnabled(c)

	return !e
}

// ContainerEnabled returns true if this container should be monitored by Glouton.
// Also return a 2nd boolean telling is this container is explicitly enabled or if it's the default.
func (cf ContainerFilter) ContainerEnabled(c Container) (enabled bool, explicit bool) {
	if c.StoppedAndReplaced() {
		return false, true
	}

	name := c.ContainerName()

	for _, pattern := range cf.DenyList {
		matched, err := filepath.Match(pattern, name)
		if err == nil && matched {
			return false, true
		}
	}

	for _, pattern := range cf.AllowList {
		matched, err := filepath.Match(pattern, name)
		if err == nil && matched {
			return true, true
		}
	}

	return containerEnabledFromLabels(LabelsAndAnnotations(c), cf.DisabledByDefault)
}

func containerEnabledFromLabels(labels map[string]string, disabledByDefault bool) (enabled bool, explicit bool) {
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

	return string2Boolean(label, !disabledByDefault)
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

	maps.Copy(results, labels)

	maps.Copy(results, annotations)

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
			logger.V(1).Printf("Label %#v of %s contain invalid port: %v", k, c.ContainerName(), err)

			continue
		}

		ignoredPort[int(port)] = ignore
	}

	return ignoredPort
}

// FakeContainer is a structure used to emulate containers for tests purposes
// It is defined in this file instead of the test file because it is used
// by multiples tests files.
type FakeContainer struct {
	FakeRuntimeName        string
	FakeAnnotations        map[string]string
	FakeCommand            []string
	FakeContainerJSON      string
	FakeContainerName      string
	FakeCreatedAt          time.Time
	FakeEnvironment        map[string]string
	FakeFinishedAt         time.Time
	FakeHealth             ContainerHealth
	FakeHealthMessage      string
	FakeID                 string
	FakeImageID            string
	FakeImageName          string
	FakeImageTags          []string
	FakeLabels             map[string]string
	FakeListenAddresses    []ListenAddress
	FakeLogPath            string
	FakePodName            string
	FakePodNamespace       string
	FakePrimaryAddress     string
	FakeStartedAt          time.Time
	FakeState              ContainerState
	FakeStoppedAndReplaced bool
	FakePID                int

	// Test* flags are only used by tests
	TestIgnored bool
	TestHasPod  bool
}

func (c FakeContainer) RuntimeName() string {
	if c.FakeRuntimeName == "" {
		return "fake"
	}

	return c.FakeRuntimeName
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

func (c FakeContainer) ImageTags(_ context.Context) ([]string, error) {
	return c.FakeImageTags, nil
}

func (c FakeContainer) Labels() map[string]string {
	return c.FakeLabels
}

func (c FakeContainer) ListenAddresses() []ListenAddress {
	return c.FakeListenAddresses
}

func (c FakeContainer) LogPath() string {
	return c.FakeLogPath
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

func (c FakeContainer) PID() int {
	return c.FakePID
}

func (c FakeContainer) Diff(ctx context.Context, other Container) string {
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

	tags, _ := other.ImageTags(ctx)
	if diff := cmp.Diff(tags, c.FakeImageTags); len(c.FakeImageTags) != 0 && diff != "" {
		diffs = append(diffs, "ImageTags: "+diff)
	}

	if diff := cmp.Diff(other.Labels(), c.FakeLabels); c.FakeLabels != nil && diff != "" {
		diffs = append(diffs, diff)
	}

	if c.FakeListenAddresses != nil {
		addresses := other.ListenAddresses()
		if diff := cmp.Diff(addresses, c.FakeListenAddresses); diff != "" {
			diffs = append(diffs, "ListenAddresses: "+diff)
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
