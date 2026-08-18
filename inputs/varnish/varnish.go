// Copyright 2015-2026 Bleemeo
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

//go:build !windows

package varnish

import (
	"time"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/prometheus/registry"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/varnish"
)

// New returns a Varnish input. It reads the metrics with "sudo varnishstat", run by
// Telegraf itself and not through Glouton's command runner, so a Varnish running in a
// container isn't reachable when Glouton runs on the host (and the other way around).
func New() (telegraf.Input, registry.RegistrationOption, error) {
	input, ok := telegraf_inputs.Inputs["varnish"]
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput
	}

	varnishInput, ok := input().(*varnish.Varnish)
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrUnexpectedType
	}

	// The input uses "sudo varnishstat ..." to retrieve the metrics.
	varnishInput.UseSudo = true

	internalInput := &internal.Input{
		Input: varnishInput,
		Accumulator: internal.Accumulator{
			RenameGlobal:     renameGlobal,
			TransformMetrics: transformMetrics,
			DifferentiatedMetrics: []string{
				"cache_hit",
				"cache_miss",
			},
		},
		Name: "varnish",
	}

	options := registry.RegistrationOption{
		// The input uses an external command with sudo so we gather metrics less often.
		MinInterval: 60 * time.Second,
	}

	return internalInput, options, nil
}

// renameGlobal drops the "section" tag: the metrics we gather all come from the MAIN
// section of varnishstat, so it's the same value on every point.
func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	delete(gatherContext.Tags, "section")

	return gatherContext, false
}

// transformMetrics adds a cache_hit_ratio field computed from the
// already-differentiated cache_hit/cache_miss rates.
func transformMetrics(_ internal.GatherContext, fields map[string]float64, _ map[string]any) map[string]float64 {
	hitRate, hasHit := fields["cache_hit"]
	missRate, hasMiss := fields["cache_miss"]

	// Protect from division by 0.
	if hasHit && hasMiss && hitRate+missRate > 0 {
		fields["cache_hit_ratio"] = hitRate / (hitRate + missRate)
	}

	return fields
}
