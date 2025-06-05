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

package net

import (
	"github.com/bleemeo/glouton/facts/container-runtime/veth"
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/net"
)

type netTransformer struct {
	filter       types.Matcher
	vethProvider *veth.Provider
}

// New initialise net.Input
//
// denylist contains a list of interface name prefix to ignore.
func New(filter types.Matcher, vethProvider *veth.Provider) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["net"]
	if ok {
		netInput, _ := input().(*net.Net)
		netInput.IgnoreProtocolStats = true
		nt := netTransformer{
			filter:       filter,
			vethProvider: vethProvider,
		}
		i = &internal.Input{
			Input: netInput,
			Accumulator: internal.Accumulator{
				RenameGlobal:          nt.renameGlobal,
				DifferentiatedMetrics: []string{"bytes_sent", "bytes_recv", "drop_in", "drop_out", "packets_recv", "packets_sent", "err_out", "err_in"},
				TransformMetrics:      nt.transformMetrics,
				RenameCallbacks:       []internal.RenameCallback{nt.renameCallback},
			},
			Name: "net",
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return
}

func (nt netTransformer) renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	item, ok := gatherContext.Tags["interface"]
	gatherContext.Tags = make(map[string]string)

	if !ok {
		return gatherContext, true
	}

	match := nt.filter.Match(item)
	if !match {
		return gatherContext, true
	}

	gatherContext.Tags[types.LabelItem] = item

	return gatherContext, false
}

func (nt netTransformer) transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64 {
	_ = currentContext
	_ = originalFields

	for metricName, value := range fields {
		switch metricName {
		case "bytes_sent":
			delete(fields, "bytes_sent")
			fields["bits_sent"] = value * 8
		case "bytes_recv":
			delete(fields, "bytes_recv")
			fields["bits_recv"] = value * 8
		}
	}

	return fields
}

func (nt netTransformer) renameCallback(
	labels map[string]string,
	annotations types.MetricAnnotations,
) (map[string]string, types.MetricAnnotations) {
	containerID, err := nt.vethProvider.ContainerID(labels[types.LabelItem])
	if err != nil {
		logger.V(1).Printf("Failed to get container interfaces: %s", err)

		return labels, annotations
	}

	annotations.ContainerID = containerID

	return labels, annotations
}
