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

package redis

import (
	"errors"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/logger"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/redis"
)

var errTypeAssertion = errors.New("failed type assertion")

// The telegraf Redis plugin doesn't implement ServiceInput, so there is no Close function,
// the clients have to be manually closed by accessing private struct fields.
type redisServiceInput struct {
	telegraf.Input
}

func (r redisServiceInput) Start(_ telegraf.Accumulator) error {
	return nil
}

func (r redisServiceInput) Stop() {
	if err := r.stop(); err != nil {
		logger.V(1).Printf("Failed to close Redis client: %v\n", err)
	}
}

func (r redisServiceInput) stop() error {
	redisInput, ok := r.Input.(*redis.Redis)
	if !ok {
		return errTypeAssertion
	}

	redisInput.Stop()

	return nil
}

// New initialise redis.Input.
func New(url string, password string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["redis"]
	if ok {
		redisInput, ok := input().(*redis.Redis)
		if ok {
			slice := append(make([]string, 0), url)
			redisInput.Servers = slice
			redisInput.Log = internal.NewLogger()
			redisInput.Password = password
			i = &internal.Input{
				Input: redisServiceInput{redisInput},
				Accumulator: internal.Accumulator{
					DifferentiatedMetrics: []string{"evicted_keys", "expired_keys", "keyspace_hits", "keyspace_misses", "total_commands_processed", "total_connections_received"},
					TransformMetrics:      transformMetrics,
				},
				Name: "redis",
			}
		} else {
			err = inputs.ErrUnexpectedType
		}
	} else {
		err = inputs.ErrDisabledInput
	}

	return
}

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]any) map[string]float64 {
	_ = currentContext
	_ = originalFields

	newFields := make(map[string]float64)

	for metricName, value := range fields {
		switch metricName {
		case "evicted_keys", "expired_keys", "keyspace_hits", "keyspace_misses":
			// Keep name unchanged.
		case "keyspace_hitrate", "pubsub_channels", "pubsub_patterns", "uptime":
			// Keep name unchanged.
		case "connected_slaves":
			metricName = "current_connections_slaves"
		case "clients":
			metricName = "current_connections_clients"
		case "used_memory":
			metricName = "memory"
		case "used_memory_lua":
			metricName = "memory_lua"
		case "used_memory_peak":
			metricName = "memory_peak"
		case "used_memory_rss":
			metricName = "memory_rss"
		case "total_connections_received":
			metricName = "total_connections"
		case "total_commands_processed":
			metricName = "total_operations"
		case "rdb_changes_since_last_save":
			metricName = "volatile_changes"
		default:
			continue
		}

		newFields[metricName] = value
	}

	return newFields
}
