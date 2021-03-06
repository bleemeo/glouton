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

package net

import (
	"glouton/inputs"
	"glouton/inputs/internal"
	"strings"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/net"
)

type netTransformer struct {
	blacklist []string
}

// New initialise net.Input
//
// blacklist contains a list of interface name prefix to ignore.
func New(blacklist []string) (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["net"]
	if ok {
		netInput := input().(*net.NetIOStats)
		netInput.IgnoreProtocolStats = true
		nt := netTransformer{
			blacklist,
		}
		i = &internal.Input{
			Input: netInput,
			Accumulator: internal.Accumulator{
				RenameGlobal:     nt.renameGlobal,
				DerivatedMetrics: []string{"bytes_sent", "bytes_recv", "drop_in", "drop_out", "packets_recv", "packets_sent", "err_out", "err_in"},
				TransformMetrics: nt.transformMetrics,
			},
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return
}

func (nt netTransformer) renameGlobal(originalContext internal.GatherContext) (newContext internal.GatherContext, drop bool) {
	newContext.Measurement = originalContext.Measurement
	item, ok := originalContext.Tags["interface"]
	newContext.Tags = make(map[string]string)

	if !ok {
		drop = true
		return
	}

	for _, b := range nt.blacklist {
		if strings.HasPrefix(item, b) {
			drop = true
			return
		}
	}

	newContext.Annotations.BleemeoItem = item
	newContext.Tags["device"] = item

	return
}

func (nt netTransformer) transformMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	for metricName, value := range fields {
		if metricName == "bytes_sent" {
			delete(fields, "bytes_sent")
			fields["bits_sent"] = value * 8
		} else if metricName == "bytes_recv" {
			delete(fields, "bytes_recv")
			fields["bits_recv"] = value * 8
		}
	}

	return fields
}
