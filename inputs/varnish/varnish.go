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

	// The plugin only collects cache_hit/cache_miss/uptime by default. The backend and
	// thread-pool counters below are cheap backend-health and saturation signals varnishstat
	// already tracks, so ask for them too instead of leaving them out for lack of asking.
	varnishInput.Stats = []string{
		"MAIN.cache_hit",
		"MAIN.cache_miss",
		"MAIN.uptime",
		"MAIN.backend_fail",
		"MAIN.backend_unhealthy",
		"MAIN.n_lru_nuked",
		"MAIN.threads",
		"MAIN.threads_limited",
		"MAIN.sess_dropped",
		"MAIN.sess_queued",
	}

	internalInput := &internal.Input{
		Input: varnishInput,
		Accumulator: internal.Accumulator{
			RenameGlobal:     renameGlobal,
			RenameMetrics:    renameMetrics,
			TransformMetrics: transformMetrics,
			DifferentiatedMetrics: []string{
				"cache_hit",
				"cache_miss",
				// backend_fail/backend_unhealthy/n_lru_nuked/threads_limited/sess_dropped/
				// sess_queued are lifetime counts since Varnish started, same shape as
				// cache_hit/cache_miss. threads is deliberately not listed: it's the
				// current thread count, not a running total.
				"backend_fail",
				"backend_unhealthy",
				"n_lru_nuked",
				"threads_limited",
				"sess_dropped",
				"sess_queued",
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

var fieldRenames = map[string]string{ //nolint:gochecknoglobals
	// varnishstat calls this a "nuke": an object forced out of cache to make room for a
	// new one, as opposed to naturally expiring. "eviction" is the term users of any other
	// cache already know, and reads next to cache_hit/cache_miss/cache_hit_ratio.
	"n_lru_nuked": "cache_evictions",
	// varnishstat abbreviates "sessions" to "sess"; spelled out here to match
	// dovecot_num_connected_sessions and read on its own without varnishstat's docs open.
	"sess_dropped": "sessions_dropped",
	"sess_queued":  "sessions_queued",
}

func renameMetrics(currentContext internal.GatherContext, metricName string) (newMeasurement string, newMetricName string) {
	if renamed, ok := fieldRenames[metricName]; ok {
		return currentContext.Measurement, renamed
	}

	return currentContext.Measurement, metricName
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
