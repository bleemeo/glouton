package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"reflect"

	dockerTypes "github.com/docker/docker/api/types"
	containerTypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
)

// MockDockerClient is a fake Docker client that could be used during test.
type MockDockerClient struct {
	EventChanMaker func() <-chan events.Message
	Containers     []dockerTypes.ContainerJSON
	Version        dockerTypes.Version
	Top            map[string]containerTypes.ContainerTopOKBody
	TopWaux        map[string]containerTypes.ContainerTopOKBody

	TopCallCount int
}

var errNotFound = errors.New("not found?")
var errNotImplemented = errors.New("not implemented")
var errContainerTopMissingArg = errors.New("ContainerTop called without empty arg or waux")

func notImplemented(action string) error {
	return fmt.Errorf("%s %w", action, errNotImplemented)
}

// ContainerExecAttach is not implemented.
func (cl *MockDockerClient) ContainerExecAttach(ctx context.Context, execID string, config dockerTypes.ExecStartCheck) (dockerTypes.HijackedResponse, error) {
	return dockerTypes.HijackedResponse{}, notImplemented("ContainerExecAttach")
}

// ContainerExecCreate is not implemented.
func (cl *MockDockerClient) ContainerExecCreate(ctx context.Context, container string, config dockerTypes.ExecConfig) (dockerTypes.IDResponse, error) {
	return dockerTypes.IDResponse{}, notImplemented("ContainerExecCreate")
}

// ContainerInspect return inspect for in-memory list of containers.
func (cl *MockDockerClient) ContainerInspect(ctx context.Context, container string) (dockerTypes.ContainerJSON, error) {
	for _, c := range cl.Containers {
		if c.ID == container || c.Name == "/"+container {
			return c, nil
		}
	}

	return dockerTypes.ContainerJSON{}, errNotFound
}

// ContainerList list containers from in-memory list.
func (cl *MockDockerClient) ContainerList(ctx context.Context, options dockerTypes.ContainerListOptions) ([]dockerTypes.Container, error) {
	if !reflect.DeepEqual(options, dockerTypes.ContainerListOptions{All: true}) {
		return nil, fmt.Errorf("ContainerList %w with options other than all=True", errNotImplemented)
	}

	if cl.Containers == nil {
		return nil, fmt.Errorf("ContainerList %w", errNotImplemented)
	}

	result := make([]dockerTypes.Container, len(cl.Containers))
	for i, c := range cl.Containers {
		result[i] = dockerTypes.Container{
			ID: c.ID,
		}
	}

	return result, nil
}

// ContainerTop return hard-coded value for top.
func (cl *MockDockerClient) ContainerTop(ctx context.Context, container string, arguments []string) (containerTypes.ContainerTopOKBody, error) {
	cl.TopCallCount++

	if len(arguments) == 0 {
		return cl.Top[container], nil
	}

	if len(arguments) == 1 && arguments[0] == "waux" {
		return cl.TopWaux[container], nil
	}

	return containerTypes.ContainerTopOKBody{}, errContainerTopMissingArg
}

// Events do events.
func (cl *MockDockerClient) Events(ctx context.Context, options dockerTypes.EventsOptions) (<-chan events.Message, <-chan error) {
	if cl.EventChanMaker != nil {
		return cl.EventChanMaker(), nil
	}

	ch := make(chan error, 1)
	ch <- fmt.Errorf("ContainerTop %w", errNotImplemented)

	return nil, ch
}

// NetworkInspect is not implemented.
func (cl *MockDockerClient) NetworkInspect(ctx context.Context, network string, options dockerTypes.NetworkInspectOptions) (dockerTypes.NetworkResource, error) {
	return dockerTypes.NetworkResource{}, fmt.Errorf("NetworkInspect %w", errNotImplemented)
}

// NetworkList is not implemented.
func (cl *MockDockerClient) NetworkList(ctx context.Context, options dockerTypes.NetworkListOptions) ([]dockerTypes.NetworkResource, error) {
	return nil, fmt.Errorf("NetworkList %w", errNotImplemented)
}

// Ping do nothing.
func (cl *MockDockerClient) Ping(ctx context.Context) (dockerTypes.Ping, error) {
	return dockerTypes.Ping{}, nil
}

// ServerVersion do server version.
func (cl *MockDockerClient) ServerVersion(ctx context.Context) (dockerTypes.Version, error) {
	if len(cl.Version.Components) == 0 {
		return dockerTypes.Version{
			Version: "42",
		}, nil
	}

	return cl.Version, nil
}

// NewDockerMock create new MockDockerClient from a directory which may contains docker-version & docker-containers.json.
func NewDockerMock(dirname string) (*MockDockerClient, error) {
	result := &MockDockerClient{}

	data, err := ioutil.ReadFile(filepath.Join(dirname, "docker-version.json"))
	if err == nil {
		err = json.Unmarshal(data, &result.Version)
		if err != nil {
			return result, err
		}
	}

	data, err = ioutil.ReadFile(filepath.Join(dirname, "docker-containers.json"))
	if err == nil {
		err = json.Unmarshal(data, &result.Containers)
		if err != nil {
			return result, err
		}
	}

	return result, err
}

// NewDockerMockFromFile create a MockDockerClient from JSON file which contains containers.
func NewDockerMockFromFile(filename string) (*MockDockerClient, error) {
	result := &MockDockerClient{}

	data, err := ioutil.ReadFile(filename)
	if err == nil {
		err = json.Unmarshal(data, &result.Containers)
		if err != nil {
			return result, err
		}
	}

	return result, err
}

// FakeDocker return a Docker runtime connector that use a mock client.
func FakeDocker(client *MockDockerClient) *Docker {
	return &Docker{
		openConnection: func(_ context.Context, _ string) (cl dockerClient, err error) {
			return client, nil
		},
	}
}
