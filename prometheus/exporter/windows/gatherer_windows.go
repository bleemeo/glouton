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
	"context"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/prometheus/registry"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// AddWindowsExporter add a node_exporter to collector.
func AddWindowsExporter(ctx context.Context, gloutonRegistry *registry.Registry, collectors []string, options inputs.CollectorConfig) error {
	collector, err := NewCollector(ctx, collectors, options)
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
			Description:    "windows_exporter",
			JitterSeed:     0,
			GatherModifier: computeDiskDeviceStatus,
		},
		reg,
	)

	return err
}

// computeDiskDeviceStatus will produce smart_device_health_status from metrics found in mfs.
// It assume allows metrics start by "windows_", meaning "smart_device_health_status" will always be the
// first (no more sorting is done that prepend the new metric).
func computeDiskDeviceStatus(mfs []*dto.MetricFamily, _ error) []*dto.MetricFamily {
	smartStatus := getSmartStatus(mfs)
	if len(smartStatus) == 0 {
		return mfs
	}

	beforeMFS := model.MetricPointsToFamilies(smartStatus)

	return append(beforeMFS, mfs...)
}
