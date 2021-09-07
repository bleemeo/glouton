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

import "github.com/influxdata/telegraf"

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
}
