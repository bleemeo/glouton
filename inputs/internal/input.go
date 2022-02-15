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

package internal

import (
	"glouton/logger"
	"reflect"
	"unsafe"

	goredis "github.com/go-redis/redis"
	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/models"
	"github.com/influxdata/telegraf/plugins/inputs/redis"
)

// Input is a generic input that use the modifying Accumulator defined in this package.
type Input struct {
	telegraf.Input
	Accumulator Accumulator
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval".
func (i *Input) Gather(acc telegraf.Accumulator) error {
	i.Accumulator.Accumulator = acc
	i.Accumulator.PrepareGather()
	err := i.Input.Gather(&i.Accumulator)

	return err
}

// Start the ServiceInput.  The Accumulator may be retained and used until
// Stop returns.
func (i *Input) Start(acc telegraf.Accumulator) error {
	i.Accumulator.Accumulator = acc
	if si, ok := i.Input.(telegraf.ServiceInput); ok {
		return si.Start(&i.Accumulator)
	}

	return nil
}

// Init performs one time setup of the plugin and returns an error if the
// configuration is invalid.
func (i *Input) Init() error {
	i.fixTelegrafInput()

	if si, ok := i.Input.(telegraf.Initializer); ok {
		return si.Init()
	}

	return nil
}

// Stop stops the services and closes any necessary channels and connections.
func (i *Input) Stop() {
	if si, ok := i.Input.(telegraf.ServiceInput); ok {
		si.Stop()
	}

	// The telegraf Redis plugin doesn't implement ServiceInput, so there is no Close function,
	// the clients have to be manually closed by accessing private struct fields.
	if redisInput, ok := i.Input.(*redis.Redis); ok {
		field := reflect.ValueOf(redisInput).Elem().FieldByName("clients")
		clientsField := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface()

		if clientsInterface, ok := clientsField.([]redis.Client); ok {
			for _, clientInterface := range clientsInterface {
				if redisClient, ok := clientInterface.(*redis.RedisClient); ok {
					field := reflect.ValueOf(redisClient).Elem().FieldByName("client")
					clientField := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface()

					if client, ok := clientField.(*goredis.Client); ok {
						if err := client.Close(); err != nil {
							logger.V(1).Printf("Failed to close Redis client: %v\n", err)
						}
					}
				}
			}
		}
	}
}

// fixTelegrafInput do some fix to make Telegraf input working.
// It try to initialize all fields that must be initialized like Log.
func (i *Input) fixTelegrafInput() {
	models.SetLoggerOnPlugin(i.Input, logger.NewTelegrafLog())
}
