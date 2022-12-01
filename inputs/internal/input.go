// Copyright 2015-2022 Bleemeo
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
	"fmt"
	"glouton/collector"
	"glouton/logger"
	"glouton/prometheus/registry"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/models"
)

// Input is a generic input that use the modifying Accumulator defined in this package.
type Input struct {
	telegraf.Input
	Accumulator Accumulator
	Name        string
	// KeepLabels make the input use an input gatherer instead of the collector,
	// this means all labels will be kept and not only the item.
	KeepLabels bool
	// Recording rules evaluated in the input gatherer. KeepLabels must
	// be true if recording rules are used. They are evaluated after
	// the metrics are renamed in the accumulator.
	Rules []registry.SimpleRule
	// The delay to wait for between gathers.
	MinInterval time.Duration

	startError        error
	lastGatherIsError bool
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval".
func (i *Input) Gather(acc telegraf.Accumulator) error {
	i.Accumulator.Accumulator = acc
	i.Accumulator.PrepareGather()

	err := i.Input.Gather(&i.Accumulator)
	if err != nil {
		if i.lastGatherIsError {
			return fmt.Errorf("%w: %s", collector.ErrConsecutiveGather, err)
		}

		i.lastGatherIsError = true

		return err
	}

	i.lastGatherIsError = false

	return nil
}

// Start the ServiceInput.  The Accumulator may be retained and used until
// Stop returns.
func (i *Input) Start(acc telegraf.Accumulator) error {
	i.Accumulator.Accumulator = acc
	if si, ok := i.Input.(telegraf.ServiceInput); ok {
		i.startError = si.Start(&i.Accumulator)

		return i.startError
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
		// Stop the service only if it started properly to avoid panic.
		if i.startError == nil {
			si.Stop()
		}
	}
}

// fixTelegrafInput do some fix to make Telegraf input working.
// It try to initialize all fields that must be initialized like Log.
func (i *Input) fixTelegrafInput() {
	models.SetLoggerOnPlugin(i.Input, logger.NewTelegrafLog(i.Name))
}
