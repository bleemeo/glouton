// Copyright 2015-2018 Bleemeo
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

// Package for redis input

package redis

import (
	"errors"
	"fmt"
	"time"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/redis"
)

// Input countains input information about Redis
type Input struct {
	telegraf.Input
}

// New initialise redis.Input
func New(url string) (i *Input, err error) {
	var input, ok = telegraf_inputs.Inputs["redis"]
	if ok {
		redisInput, ok := input().(*redis.Redis)
		if ok {
			slice := append(make([]string, 0), url)
			redisInput.Servers = slice
			i = &Input{redisInput}
		} else {
			err = errors.New("Telegraf \"redis\" input type is not redis.Redis")
		}
	} else {
		err = errors.New("Telegraf don't have \"redis\" input")
	}
	return
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (i *Input) Gather(acc telegraf.Accumulator) error {
	redisAccumulator := accumulator{acc}
	err := i.Input.Gather(&redisAccumulator)
	return err
}

// accumulator save the redis metric from telegraf
type accumulator struct {
	accumulator telegraf.Accumulator
}

// AddFields adds a metric to the accumulator with the given measurement
// name, fields, and tags (and timestamp). If a timestamp is not provided,
// then the accumulator sets it to "now".
// Create a point with a value, decorating it with tags
// NOTE: tags is expected to be owned by the caller, don't mutate
// it after passing to Add.
// nolint: gocyclo
func (a *accumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	finalFields := make(map[string]interface{})
	for metricName, value := range fields {
		finalMetricName := measurement + "_" + metricName
		switch metricName {
		case "evicted_keys", "expired_keys", "keyspace_hits", "keyspace_misses", "keyspace_hitrate", "pubsub_channels", "pubsub_patterns", "uptime":
			// Keep name unchanged.
		case "connected_slaves":
			finalMetricName = "redis_current_connections_slaves"
		case "clients":
			finalMetricName = "redis_current_connections_clients"
		case "total_system_memory":
			finalMetricName = "redis_memory"
		case "used_memory_lua":
			finalMetricName = "redis_memory_lua"
		case "used_memory_peak":
			finalMetricName = "redis_memory_peak"
		case "used_memory_rss":
			finalMetricName = "redis_memory_rss"
		case "total_connections_received":
			finalMetricName = "redis_total_connections"
		case "total_commands_processed":
			finalMetricName = "redis_total_operations"
		case "rdb_changes_since_last_save":
			finalMetricName = "redis_volatile_changes"
		default:
			continue
		}
		finalFields[finalMetricName] = value
	}
	a.accumulator.AddFields(measurement, finalFields, nil, t...)
}

// AddError add an error to the accumulator
func (a *accumulator) AddError(err error) {
	a.accumulator.AddError(err)
}

// This functions are useless for redis metric.
// They are not implemented

// AddGauge is useless for redis
func (a *accumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddGauge not implemented for redis accumulator"))
}

// AddCounter is useless for redis
func (a *accumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddCounter not implemented for redis accumulator"))
}

// AddSummary is useless for redis
func (a *accumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddSummary not implemented for redis accumulator"))
}

// AddHistogram is useless for redis
func (a *accumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	a.accumulator.AddError(fmt.Errorf("AddHistogram not implemented for redis accumulator"))
}

// SetPrecision is useless for redis
func (a *accumulator) SetPrecision(precision, interval time.Duration) {
	a.accumulator.AddError(fmt.Errorf("SetPrecision not implemented for redis accumulator"))
}
