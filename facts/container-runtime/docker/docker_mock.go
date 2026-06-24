// Copyright 2015-2026 Bleemeo
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

	containerTypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	docker "github.com/moby/moby/client"
)

// MockDockerClient is a fake Docker client that could be used during test.
type MockDockerClient struct {
	EventChanMaker func() <-chan events.Message
	Containers     []containerTypes.InspectResponse
	Version        docker.ServerVersionResult
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

// ExecAttach is not implemented.
func (cl *MockDockerClient) ExecAttach(context.Context, string, docker.ExecAttachOptions) (docker.ExecAttachResult, error) {
	return docker.ExecAttachResult{}, errNotImplemented
}

// ExecCreate is not implemented.
func (cl *MockDockerClient) ExecCreate(context.Context, string, docker.ExecCreateOptions) (docker.ExecCreateResult, error) {
	return docker.ExecCreateResult{}, errNotImplemented
}

// ContainerInspect return inspect for in-memory list of containers.
func (cl *MockDockerClient) ContainerInspect(_ context.Context, container string, _ docker.ContainerInspectOptions) (docker.ContainerInspectResult, error) {
	if cl.ReturnError != nil {
		return docker.ContainerInspectResult{}, cl.ReturnError
	}

	for _, c := range cl.Containers {
		if c.ID == container || c.Name == "/"+container {
			return docker.ContainerInspectResult{Container: c}, nil
		}
	}

	return docker.ContainerInspectResult{}, errNotFound
}

// ContainerList list containers from in-memory list.
func (cl *MockDockerClient) ContainerList(_ context.Context, options docker.ContainerListOptions) (docker.ContainerListResult, error) {
	if cl.ReturnError != nil {
		return docker.ContainerListResult{}, cl.ReturnError
	}

	if !reflect.DeepEqual(options, docker.ContainerListOptions{All: true}) {
		return docker.ContainerListResult{}, fmt.Errorf("ContainerList %w with options other than all=True", errNotImplemented)
	}

	if cl.Containers == nil {
		return docker.ContainerListResult{}, fmt.Errorf("ContainerList %w", errNotImplemented)
	}

	result := make([]containerTypes.Summary, len(cl.Containers))
	for i, c := range cl.Containers {
		result[i] = containerTypes.Summary{
			ID: c.ID,
		}
	}

	return docker.ContainerListResult{Items: result}, nil
}

// ContainerTop return hard-coded value for top.
func (cl *MockDockerClient) ContainerTop(_ context.Context, container string, options docker.ContainerTopOptions) (docker.ContainerTopResult, error) {
	cl.TopCallCount++

	if cl.ReturnError != nil {
		return docker.ContainerTopResult{}, cl.ReturnError
	}

	if len(options.Arguments) == 0 {
		top := cl.Top[container]

		return docker.ContainerTopResult{Processes: top.Processes, Titles: top.Titles}, nil
	}

	if len(options.Arguments) == 1 && options.Arguments[0] == "waux" {
		top := cl.TopWaux[container]

		return docker.ContainerTopResult{Processes: top.Processes, Titles: top.Titles}, nil
	}

	return docker.ContainerTopResult{}, errContainerTopMissingArg
}

// Events do events.
func (cl *MockDockerClient) Events(context.Context, docker.EventsListOptions) docker.EventsResult {
	if cl.ReturnError != nil {
		ch := make(chan error, 1)
		ch <- cl.ReturnError

		return docker.EventsResult{Err: ch}
	}

	if cl.EventChanMaker != nil {
		return docker.EventsResult{Messages: cl.EventChanMaker()}
	}

	ch := make(chan error, 1)
	ch <- fmt.Errorf("Events %w", errNotImplemented)

	return docker.EventsResult{Err: ch}
}

func (cl *MockDockerClient) ImageInspect(context.Context, string, ...docker.ImageInspectOption) (docker.ImageInspectResult, error) {
	return docker.ImageInspectResult{}, errNotImplemented
}

// NetworkInspect is not implemented.
func (cl *MockDockerClient) NetworkInspect(context.Context, string, docker.NetworkInspectOptions) (docker.NetworkInspectResult, error) {
	return docker.NetworkInspectResult{}, fmt.Errorf("NetworkInspect %w", errNotImplemented)
}

// NetworkList is not implemented.
func (cl *MockDockerClient) NetworkList(context.Context, docker.NetworkListOptions) (docker.NetworkListResult, error) {
	return docker.NetworkListResult{}, fmt.Errorf("NetworkList %w", errNotImplemented)
}

// Ping do nothing.
func (cl *MockDockerClient) Ping(context.Context, docker.PingOptions) (docker.PingResult, error) {
	if cl.ReturnError != nil {
		return docker.PingResult{}, cl.ReturnError
	}

	return docker.PingResult{}, nil
}

// ServerVersion do server version.
func (cl *MockDockerClient) ServerVersion(context.Context, docker.ServerVersionOptions) (docker.ServerVersionResult, error) {
	if cl.ReturnError != nil {
		return docker.ServerVersionResult{}, cl.ReturnError
	}

	if len(cl.Version.Components) == 0 {
		return docker.ServerVersionResult{
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
