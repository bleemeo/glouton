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
	"strings"

	"github.com/bleemeo/glouton/facts"
	crTypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/docker"
)

// New initialise docker.Input.
func New(dockerAddress string, dockerRuntime crTypes.RuntimeInterface, isContainerIgnored func(facts.Container) bool) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["docker"]
	if ok {
		dockerInput, ok := input().(*docker.Docker)
		if ok {
			if dockerAddress != "" {
				dockerInput.Endpoint = dockerAddress
			}

			r := renamer{dockerRuntime: dockerRuntime, isContainerIgnored: isContainerIgnored}

			dockerInput.PerDeviceInclude = []string{}
			dockerInput.TotalInclude = []string{"cpu", "blkio", "network"}
			dockerInput.Log = internal.NewLogger()
			i = &internal.Input{
				Input: dockerInput,
				Accumulator: internal.Accumulator{
					RenameGlobal:          r.renameGlobal,
					DifferentiatedMetrics: []string{"usage_total", "rx_bytes", "tx_bytes", "io_service_bytes_recursive_read", "io_service_bytes_recursive_write"},
					TransformMetrics:      transformMetrics,
				},
				Name: "docker",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return i, err
}

type renamer struct {
	dockerRuntime      crTypes.RuntimeInterface
	isContainerIgnored func(facts.Container) bool
}

func (r renamer) renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	gatherContext.Measurement = strings.TrimPrefix(gatherContext.Measurement, "docker_")
	gatherContext.OriginalTags = gatherContext.Tags
	gatherContext.Tags = make(map[string]string)

	if name, ok := gatherContext.OriginalTags["container_name"]; ok {
		gatherContext.Tags[types.LabelMetaContainerName] = name
		gatherContext.Tags[types.LabelItem] = name
	}

	if id, ok := gatherContext.OriginalFields["container_id"]; ok {
		if containerID, ok := id.(string); ok {
			gatherContext.Annotations.ContainerID = containerID
		}
	}

	c, ok := r.dockerRuntime.CachedContainer(gatherContext.Annotations.ContainerID)
	if !ok || r.isContainerIgnored(c) {
		return gatherContext, true
	}

	switch gatherContext.Measurement {
	case "container_cpu":
		if gatherContext.OriginalTags["cpu"] != "cpu-total" {
			return gatherContext, true
		}
	case "container_net":
		if gatherContext.OriginalTags["network"] != "total" {
			return gatherContext, true
		}
	case "container_blkio":
		if gatherContext.OriginalTags["device"] != "total" {
			return gatherContext, true
		}

		gatherContext.Measurement = "container_io"
	}

	return gatherContext, false
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64 {
	_ = originalFields
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
