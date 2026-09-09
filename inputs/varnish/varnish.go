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
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/registry"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/varnish"
)

// New returns a Varnish input. It reads the metrics with "varnishstat", run through
// Glouton's command runner so that the binary comes from a filesystem that has one rather
// than from the agent's own, which does not -- see useGloutonRunner.
//
// Two arguments say which Varnish is read, and normally only one of them is set. See
// Discovery.varnishTarget for how they are chosen.
//
// containerPID runs the varnishstat of that container, in its own filesystem. That is the
// way to read a containerised Varnish: the binary comes from the same image as the daemon,
// and inside that filesystem varnishd's default working directory is simply the right one,
// so no "-n" is needed.
//
// instanceDir is passed as varnishstat's "-n", naming an instance by the working directory
// varnishd keeps its shared memory in. It is the fallback for a container carrying no
// varnishstat of its own, read with the machine's binary through /proc. Empty means no
// "-n" at all, which is right for a Varnish installed on the machine -- varnishstat then
// finds the instance of the namespace it runs in.
func New(runner Runner, containerPID int, instanceDir string) (telegraf.Input, registry.RegistrationOption, error) {
	input, ok := telegraf_inputs.Inputs["varnish"]
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput
	}

	varnishInput, ok := input().(*varnish.Varnish)
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrUnexpectedType
	}

	if err := useGloutonRunner(varnishInput, runner, containerPID); err != nil {
		// Not fatal: the plugin keeps its own runner, which is what every Glouton did
		// before this and still works wherever varnishstat sits next to the agent. Only
		// the container case is lost, and it was already broken. The unit test is what
		// makes a Telegraf upgrade renaming the field loud.
		logger.V(1).Printf("Varnish metrics will be gathered without Glouton's command runner: %v", err)
	}

	// Asks the runner for root; it decides whether a sudo is actually needed.
	varnishInput.UseSudo = true

	// The plugin turns this into "-n <instanceDir>" and leaves it out when empty, which is
	// exactly the distinction wanted, so it is assigned unconditionally.
	varnishInput.InstanceName = instanceDir

	// The plugin only collects cache_hit/cache_miss/uptime by default. The backend and
	// thread-pool counters below are cheap backend-health and saturation signals varnishstat
	// already tracks, so ask for them too instead of leaving them out for lack of asking.
	// Nothing else: this is the list Glouton publishes, and asking varnishstat for a counter
	// no metric comes out of only costs a wider parse on every gather.
	varnishInput.Stats = []string{
		"MAIN.cache_hit",
		"MAIN.cache_miss",
		"MAIN.uptime",
		"MAIN.backend_fail",
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
				"backend_fail",
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
	// cache already know, and reads next to cache_hit/cache_miss/cache_hit_perc.
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

// transformMetrics adds a cache_hit_perc field computed from the already-differentiated
// cache_hit/cache_miss rates, scaled to 0..100 like every other percentage metric Glouton
// publishes.
func transformMetrics(_ internal.GatherContext, fields map[string]float64, _ map[string]any) map[string]float64 {
	hitRate, hasHit := fields["cache_hit"]
	missRate, hasMiss := fields["cache_miss"]

	// Protect from division by 0.
	if hasHit && hasMiss && hitRate+missRate > 0 {
		fields["cache_hit_perc"] = hitRate / (hitRate + missRate) * 100
	}

	return fields
}
