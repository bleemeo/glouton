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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/bleemeo/glouton/facts"

	dockerTypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/common"
	containerTypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/network"
)

// MockDockerClient is a fake Docker client that could be used during test.
type MockDockerClient struct {
	EventChanMaker func() <-chan events.Message
	Containers     []containerTypes.InspectResponse
	Version        dockerTypes.Version
	Top            map[string]containerTypes.TopResponse
	TopWaux        map[string]containerTypes.TopResponse
	ReturnError    error

	TopCallCount int
}

var (
	errNotFound               = errors.New("not found")
	errNotImplemented         = errors.New("not implemented")
	errContainerTopMissingArg = errors.New("ContainerTop called without empty arg or waux")
)

// ContainerExecAttach is not implemented.
func (cl *MockDockerClient) ContainerExecAttach(context.Context, string, containerTypes.ExecAttachOptions) (dockerTypes.HijackedResponse, error) {
	return dockerTypes.HijackedResponse{}, errNotImplemented
}

// ContainerExecCreate is not implemented.
func (cl *MockDockerClient) ContainerExecCreate(context.Context, string, containerTypes.ExecOptions) (common.IDResponse, error) {
	return common.IDResponse{}, errNotImplemented
}

// ContainerInspect return inspect for in-memory list of containers.
func (cl *MockDockerClient) ContainerInspect(_ context.Context, container string) (containerTypes.InspectResponse, error) {
	if cl.ReturnError != nil {
		return containerTypes.InspectResponse{}, cl.ReturnError
	}

	for _, c := range cl.Containers {
		if c.ID == container || c.Name == "/"+container {
			return c, nil
		}
	}

	return containerTypes.InspectResponse{}, errNotFound
}

// ContainerList list containers from in-memory list.
func (cl *MockDockerClient) ContainerList(_ context.Context, options containerTypes.ListOptions) ([]containerTypes.Summary, error) {
	if cl.ReturnError != nil {
		return nil, cl.ReturnError
	}

	if !reflect.DeepEqual(options, containerTypes.ListOptions{All: true}) {
		return nil, fmt.Errorf("ContainerList %w with options other than all=True", errNotImplemented)
	}

	if cl.Containers == nil {
		return nil, fmt.Errorf("ContainerList %w", errNotImplemented)
	}

	result := make([]containerTypes.Summary, len(cl.Containers))
	for i, c := range cl.Containers {
		result[i] = containerTypes.Summary{
			ID: c.ID,
		}
	}

	return result, nil
}

// ContainerTop return hard-coded value for top.
func (cl *MockDockerClient) ContainerTop(_ context.Context, container string, arguments []string) (containerTypes.TopResponse, error) {
	cl.TopCallCount++

	if cl.ReturnError != nil {
		return containerTypes.TopResponse{}, cl.ReturnError
	}

	if len(arguments) == 0 {
		return cl.Top[container], nil
	}

	if len(arguments) == 1 && arguments[0] == "waux" {
		return cl.TopWaux[container], nil
	}

	return containerTypes.TopResponse{}, errContainerTopMissingArg
}

// Events do events.
func (cl *MockDockerClient) Events(context.Context, events.ListOptions) (<-chan events.Message, <-chan error) {
	if cl.ReturnError != nil {
		ch := make(chan error, 1)
		ch <- cl.ReturnError

		return nil, ch
	}

	if cl.EventChanMaker != nil {
		return cl.EventChanMaker(), nil
	}

	ch := make(chan error, 1)
	ch <- fmt.Errorf("Events %w", errNotImplemented)

	return nil, ch
}

func (cl *MockDockerClient) ImageInspectWithRaw(context.Context, string) (dockerTypes.ImageInspect, []byte, error) {
	return dockerTypes.ImageInspect{}, nil, errNotImplemented
}

// NetworkInspect is not implemented.
func (cl *MockDockerClient) NetworkInspect(context.Context, string, network.InspectOptions) (network.Inspect, error) {
	return network.Inspect{}, fmt.Errorf("NetworkInspect %w", errNotImplemented)
}

// NetworkList is not implemented.
func (cl *MockDockerClient) NetworkList(context.Context, network.ListOptions) ([]network.Inspect, error) {
	return nil, fmt.Errorf("NetworkList %w", errNotImplemented)
}

// Ping do nothing.
func (cl *MockDockerClient) Ping(context.Context) (dockerTypes.Ping, error) {
	if cl.ReturnError != nil {
		return dockerTypes.Ping{}, cl.ReturnError
	}

	return dockerTypes.Ping{}, nil
}

// ServerVersion do server version.
func (cl *MockDockerClient) ServerVersion(context.Context) (dockerTypes.Version, error) {
	if cl.ReturnError != nil {
		return dockerTypes.Version{}, cl.ReturnError
	}

	if len(cl.Version.Components) == 0 {
		return dockerTypes.Version{
			Version: "42",
		}, nil
	}

	return cl.Version, nil
}

// Close the docker client.
func (cl *MockDockerClient) Close() error {
	if cl.ReturnError != nil {
		return cl.ReturnError
	}

	return nil
}

// NewDockerMock create new MockDockerClient from a directory which may contains docker-version & docker-containers.json.
func NewDockerMock(dirname string) (*MockDockerClient, error) {
	result := &MockDockerClient{}

	data, err := os.ReadFile(filepath.Join(dirname, "docker-version.json"))
	if err == nil {
		err = json.Unmarshal(data, &result.Version)
		if err != nil {
			return result, err
		}
	}

	data, err = os.ReadFile(filepath.Join(dirname, "docker-containers.json"))
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

	data, err := os.ReadFile(filename)
	if err == nil {
		err = json.Unmarshal(data, &result.Containers)
		if err != nil {
			return result, err
		}
	}

	return result, err
}

// FakeDocker return a Docker runtime connector that use a mock client.
func FakeDocker(client *MockDockerClient, isContainerIgnored func(facts.Container) bool) *Docker {
	return newWithOpenner(
		nil,
		nil,
		isContainerIgnored,
		func(_ context.Context, _ string) (cl dockerClient, err error) {
			return client, nil
		},
	)
}
