// Copyright 2015-2023 Bleemeo
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

package memcached

import (
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"strings"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/memcached"
)

// New initialise memcached.Input.
func New(url string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["memcached"]
	if ok {
		memcachedInput, ok := input().(*memcached.Memcached)
		if ok {
			slice := append(make([]string, 0), url)
			memcachedInput.Servers = slice
			i = &internal.Input{
				Input: memcachedInput,
				Accumulator: internal.Accumulator{
					DerivatedMetrics:      []string{"bytes_read", "bytes_written", "evictions", "memcached_get_misses", "memcached_get_hits"},
					ShouldDerivateMetrics: shouldDerivateMetrics,
					TransformMetrics:      transformMetrics,
				},
				Name: "memcached",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return
}

func shouldDerivateMetrics(currentContext internal.GatherContext, metricName string) bool {
	_ = currentContext

	if strings.HasPrefix(metricName, "cmd_") {
		return true
	}

	if strings.HasSuffix(metricName, "_misses") {
		return true
	}

	if strings.HasSuffix(metricName, "_hits") {
		return true
	}

	return false
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	_ = currentContext
	_ = originalFields
	newFields := make(map[string]float64)

	for metricName, value := range fields {
		if strings.HasPrefix(metricName, "cmd_") {
			metricName = strings.ReplaceAll(metricName, "cmd_", "command_")
			newFields[metricName] = value
		}

		if strings.HasSuffix(metricName, "_misses") || strings.HasSuffix(metricName, "_hits") {
			metricName = "ops_" + metricName
			newFields[metricName] = value
		}

		switch metricName {
		case "curr_connections":
			newFields["connections_current"] = value
		case "curr_items":
			newFields["items_current"] = value
		case "bytes_read":
			newFields["octets_rx"] = value
		case "bytes_written":
			newFields["octets_tx"] = value
		case "evictions":
			newFields["ops_evictions"] = value
		case "threads":
			newFields["ps_count_threads"] = value
		case "uptime":
			newFields[metricName] = value
		}
	}

	return newFields
}
