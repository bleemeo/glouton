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

package redis

import (
	"errors"
	"glouton/inputs/internal"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/redis"
)

// New initialise redis.Input
func New(url string) (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["redis"]
	if ok {
		redisInput, ok := input().(*redis.Redis)
		if ok {
			slice := append(make([]string, 0), url)
			redisInput.Servers = slice
			i = &internal.Input{
				Input: redisInput,
				Accumulator: internal.Accumulator{
					DerivatedMetrics: []string{"evicted_keys", "expired_keys", "keyspace_hits", "keyspace_misses", "total_commands_processed", "total_connections_received"},
					TransformMetrics: transformMetrics,
				},
			}
		} else {
			err = errors.New("input Redis is not the expected type")
		}
	} else {
		err = errors.New("input Redis is not enabled in Telegraf")
	}
	return
}

func transformMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	newFields := make(map[string]float64)
	for metricName, value := range fields {
		finalMetricName := metricName
		switch metricName {
		case "evicted_keys", "expired_keys", "keyspace_hits", "keyspace_misses":
			// Keep name unchanged.
		case "keyspace_hitrate", "pubsub_channels", "pubsub_patterns", "uptime":
			// Keep name unchanged.
		case "connected_slaves":
			finalMetricName = "current_connections_slaves"
		case "clients":
			finalMetricName = "current_connections_clients"
		case "used_memory":
			finalMetricName = "memory"
		case "used_memory_lua":
			finalMetricName = "memory_lua"
		case "used_memory_peak":
			finalMetricName = "memory_peak"
		case "used_memory_rss":
			finalMetricName = "memory_rss"
		case "total_connections_received":
			finalMetricName = "total_connections"
		case "total_commands_processed":
			finalMetricName = "total_operations"
		case "rdb_changes_since_last_save":
			finalMetricName = "volatile_changes"
		default:
			continue
		}
		newFields[finalMetricName] = value
	}
	return newFields
}
