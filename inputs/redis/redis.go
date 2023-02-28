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

package redis

import (
	"errors"
	"fmt"
	"glouton/inputs"
	"glouton/inputs/internal"
	"glouton/logger"
	"reflect"
	"unsafe"

	goredis "github.com/go-redis/redis"
	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/redis"
)

var (
	errTypeAssertion      = errors.New("failed type assertion")
	errGetUnexportedField = errors.New("failed to get unexported field")
)

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

	clientsField, err := getUnexportedField(redisInput, "clients")
	if err != nil {
		return errTypeAssertion
	}

	clientsInterface, ok := clientsField.([]redis.Client)
	if !ok {
		return errTypeAssertion
	}

	for _, clientInterface := range clientsInterface {
		redisClient, ok := clientInterface.(*redis.RedisClient)
		if !ok {
			return errTypeAssertion
		}

		clientField, err := getUnexportedField(redisClient, "client")
		if err != nil {
			return err
		}

		client, ok := clientField.(*goredis.Client)
		if !ok {
			return errTypeAssertion
		}

		if err := client.Close(); err != nil {
			return err
		}
	}

	return nil
}

func getUnexportedField(object interface{}, fieldName string) (field interface{}, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", errGetUnexportedField, r)
		}
	}()

	val := reflect.ValueOf(object).Elem().FieldByName(fieldName)
	field = reflect.NewAt(val.Type(), unsafe.Pointer(val.UnsafeAddr())).Elem().Interface()

	return
}

// New initialise redis.Input.
func New(url string, password string) (i telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["redis"]
	if ok {
		redisInput, ok := input().(*redis.Redis)
		if ok {
			slice := append(make([]string, 0), url)
			redisInput.Servers = slice
			redisInput.Log = internal.Logger{}
			redisInput.Password = password
			i = &internal.Input{
				Input: redisServiceInput{redisInput},
				Accumulator: internal.Accumulator{
					DerivatedMetrics: []string{"evicted_keys", "expired_keys", "keyspace_hits", "keyspace_misses", "total_commands_processed", "total_connections_received"},
					TransformMetrics: transformMetrics,
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

func transformMetrics(currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
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
