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

package bleemeoapi

import (
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
)

type ServicePayload struct {
	bleemeoTypes.Monitor

	Account   string `json:"account"`
	IsMonitor bool   `json:"monitor"`
}

type ContainerPayload struct {
	bleemeoTypes.Container

	Host             string                `json:"host"`
	Command          string                `json:"command"`
	StartedAt        bleemeoTypes.NullTime `json:"container_started_at"`
	FinishedAt       bleemeoTypes.NullTime `json:"container_finished_at"`
	ImageID          string                `json:"container_image_id"`
	ImageName        string                `json:"container_image_name"`
	DockerAPIVersion string                `json:"docker_api_version"`
}

type MetricPayload struct {
	bleemeoTypes.Metric

	Name string `json:"label,omitempty"`
	Item string `json:"item,omitempty"`
}

type AgentPayload struct {
	bleemeoTypes.Agent

	Abstracted         bool   `json:"abstracted"`
	InitialPassword    string `json:"initial_password"`
	InitialServerGroup string `json:"initial_server_group_name,omitempty"`
}

type RemoteDiagnostic struct {
	Name string `json:"name"`
}
