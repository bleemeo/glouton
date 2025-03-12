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

//go:build windows

package windows

import (
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/prometheus/registry"

	"github.com/prometheus/client_golang/prometheus"
)

// AddWindowsExporter add a node_exporter to collector.
func AddWindowsExporter(gloutonRegistry *registry.Registry, collectors []string, options inputs.CollectorConfig) error {
	collector, err := NewCollector(collectors, options)
	if err != nil {
		return err
	}

	reg := prometheus.NewRegistry()

	err = reg.Register(collector)
	if err != nil {
		return err
	}

	_, err = gloutonRegistry.RegisterGatherer(
		registry.RegistrationOption{
			Description: "windows_exporter",
			JitterSeed:  0,
		},
		reg,
	)

	return err
}
