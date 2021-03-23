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

package docker

import (
	"errors"
	"glouton/facts"
	crTypes "glouton/facts/container-runtime/types"
	"glouton/inputs/internal"
	"glouton/types"
	"strings"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/docker"
)

// New initialise docker.Input.
func New(dockerAddress string, dockerRuntime crTypes.RuntimeInterface) (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["docker"]
	if ok {
		dockerInput, ok := input().(*docker.Docker)
		if ok {
			if dockerAddress != "" {
				dockerInput.Endpoint = dockerAddress
			}

			r := renamer{dockerRuntime: dockerRuntime}

			dockerInput.PerDevice = false
			dockerInput.Total = true
			dockerInput.Log = internal.Logger{}
			i = &internal.Input{
				Input: dockerInput,
				Accumulator: internal.Accumulator{
					RenameGlobal:     r.renameGlobal,
					DerivatedMetrics: []string{"usage_total", "rx_bytes", "tx_bytes", "io_service_bytes_recursive_read", "io_service_bytes_recursive_write"},
					TransformMetrics: transformMetrics,
				},
			}
		} else {
			err = errors.New("input Docker is not the expected type")
		}
	} else {
		err = errors.New("input Docker not enabled in Telegraf")
	}

	return
}

type renamer struct {
	dockerRuntime crTypes.RuntimeInterface
}

func (r renamer) renameGlobal(originalContext internal.GatherContext) (newContext internal.GatherContext, drop bool) {
	newContext.Measurement = strings.TrimPrefix(originalContext.Measurement, "docker_")
	newContext.Tags = make(map[string]string)

	if name, ok := originalContext.Tags["container_name"]; ok {
		newContext.Annotations.BleemeoItem = name
		newContext.Tags[types.LabelMetaContainerName] = name
	}

	if id, ok := originalContext.OriginalFields["container_id"]; ok {
		if containerID, ok := id.(string); ok {
			newContext.Annotations.ContainerID = containerID
		}
	}

	c, ok := r.dockerRuntime.CachedContainer(newContext.Annotations.ContainerID)
	if !ok || facts.ContainerIgnored(c) {
		drop = true
		return
	}

	switch newContext.Measurement {
	case "container_cpu":
		if originalContext.Tags["cpu"] != "cpu-total" {
			drop = true
		}
	case "container_net":
		if originalContext.Tags["network"] != "total" {
			drop = true
		}
	case "container_blkio":
		if originalContext.Tags["device"] != "total" {
			drop = true
		}

		newContext.Measurement = "container_io"
	}

	return newContext, drop
}

func transformMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	newFields := make(map[string]float64)

	switch currentContext.Measurement {
	case "container_cpu":
		if value, ok := fields["usage_total"]; ok {
			// Docker sends the total usage in nanosecond.
			// Convert it to Second, then percent
			newFields["used"] = value / 10000000
		}
	case "container_mem":
		if value, ok := fields["usage_percent"]; ok {
			newFields["used_perc"] = value
		}

		if value, ok := fields["usage"]; ok {
			newFields["used"] = value
		}
	case "container_net":
		if value, ok := fields["rx_bytes"]; ok {
			newFields["bits_recv"] = value * 8
		}

		if value, ok := fields["tx_bytes"]; ok {
			newFields["bits_sent"] = value * 8
		}
	case "container_io":
		if value, ok := fields["io_service_bytes_recursive_read"]; ok {
			newFields["read_bytes"] = value
		}

		if value, ok := fields["io_service_bytes_recursive_write"]; ok {
			newFields["write_bytes"] = value
		}
	}

	return newFields
}
