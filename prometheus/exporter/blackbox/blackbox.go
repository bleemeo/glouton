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

package blackbox

import (
	"context"
	"fmt"
	"glouton/logger"
	"glouton/prometheus/registry"
	"reflect"
	"time"

	"github.com/go-kit/log"
	"github.com/prometheus/blackbox_exporter/prober"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/model/labels"
)

const (
	proberNameHTTP string = "http"
	proberNameTCP  string = "tcp"
	proberNameICMP string = "icmp"
	proberNameDNS  string = "dns"
)

//nolint:gochecknoglobals
var (
	probeSuccessDesc = prometheus.NewDesc(
		prometheus.BuildFQName("", "", "probe_success"),
		"Displays whether or not the probe was a success",
		[]string{"instance"},
		nil,
	)
	probeDurationDesc = prometheus.NewDesc(
		prometheus.BuildFQName("", "", "probe_duration_seconds"),
		"Returns how long the probe took to complete in seconds",
		[]string{"instance"},
		nil,
	)
	probers = map[string]prober.ProbeFn{
		proberNameHTTP: prober.ProbeHTTP,
		proberNameTCP:  prober.ProbeTCP,
		proberNameICMP: prober.ProbeICMP,
		proberNameDNS:  prober.ProbeDNS,
	}
)

// Describe implements the prometheus.Collector interface.
func (target configTarget) Describe(ch chan<- *prometheus.Desc) {
	ch <- probeSuccessDesc
	ch <- probeDurationDesc
}

// Collect implements the prometheus.Collector interface.
// It is where we do the actual "probing".
func (target configTarget) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), target.Module.Timeout)
	// Let's ensure we don't end up with stray queries running somewhere
	defer cancel()

	probeFn, present := probers[target.Module.Prober]
	if !present {
		logger.V(1).Printf("blackbox_exporter: no prober registered under the name '%s', cannot check '%s'.",
			target.Module.Prober, target.Name)
		// notify the "client" that scraping this target resulted in an error
		ch <- prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, 0., target.Name)

		return
	}

	// The current state (sad) of affairs in blackbox_exporter in June 2020 is the following:
	// - First, the probber functions defined in prometheus/blackbox_exporter/prober are registering
	//   Collectors internally, instead of having those metrics defined first (and critically, once) and then
	//   collected over the lifetime of the program. This is the result of the design of blackbox_exporter:
	//   the idea is that targets are supplied on the fly via the /probe HTTP endpoint, and a new register is
	//   then created for the specific probe operation, with his lifetime tied to the HTTP request's. One may
	//   wonder why I said "critically" earlier on when speaking about unicity of Collectors' registration,
	//   and the reason is simple: if you declare more than once the same collector on a registry, you get a
	//   nice "panic: duplicate metrics collector registration attempted" at runtime ;). That behavior is
	//   actually forbidden by the prometheus/client_golang library. One could think of a custom registry
	//   that fakes registration when a collector is already declared. Except that's not possible :/
	// - Which brings us to the second point: the interface for prober functions is "func(ctx context.Context,
	//       target string, config config.Module, registry *prometheus.Registry, logger log.Logger) bool".
	//   As we see, the probers expect a 'prometheus.Registry', and that is a struct and not an interface, so
	//   we cannot redefine our own to abstract away all those nitty-gritty details.
	// And those are the reasons that made us choose to build a prometheus registry per query, so in effect
	// we will build nb_targets/scrape_duration registry creations per second, for the whole lifetime of the
	// program. Let's hope this won't have too much of a negative performance impact !

	registry := prometheus.NewRegistry()

	extLogger := log.With(logger.GoKitLoggerWrapper(logger.V(2)), "url", target.URL)
	start := time.Now()

	// do all the actual work
	success := probeFn(ctx, target.URL, target.Module, registry, extLogger)

	end := time.Now()
	duration := end.Sub(start)
	_ = extLogger.Log("msg", fmt.Sprintf("check started at %s, ended at %s (duration %s); success=%v", start, end, duration, success))

	mfs, err := registry.Gather()
	if err != nil {
		logger.V(1).Printf("blackbox_exporter: error while gathering metrics: %v", err)
		// notify the "client" that scraping this target resulted in an error
		ch <- prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, 0., target.Name)

		return
	}

	// write all the gathered metrics to our upper registry
	writeMFsToChan(mfs, ch)

	successVal := 0.
	if success {
		successVal = 1
	}
	ch <- prometheus.MustNewConstMetric(probeDurationDesc, prometheus.GaugeValue, duration.Seconds(), target.Name)
	ch <- prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, successVal, target.Name)
}

// compareConfigTargets returns true if the monitors are identical, and false otherwise.
func compareConfigTargets(a configTarget, b configTarget) bool {
	return a.BleemeoAgentID == b.BleemeoAgentID && a.URL == b.URL && a.RefreshRate == b.RefreshRate && reflect.DeepEqual(a.Module, b.Module)
}

func collectorInMap(value collectorWithLabels, iterable map[int]gathererWithConfigTarget) bool {
	for _, mapValue := range iterable {
		if compareConfigTargets(value.collector, mapValue.target) {
			return true
		}
	}

	return false
}

func gathererInArray(value gathererWithConfigTarget, iterable []collectorWithLabels) bool {
	for _, arrayValue := range iterable {
		// see inMap() above
		if compareConfigTargets(value.target, arrayValue.collector) {
			return true
		}
	}

	return false
}

// updateRegistrations registers and deregisters collectors to sync the internal state with the configuration.
func (m *RegisterManager) updateRegistrations() error {
	// register new probes
	for _, collectorFromConfig := range m.targets {
		if !collectorInMap(collectorFromConfig, m.registrations) {
			reg := prometheus.NewRegistry()

			if err := reg.Register(collectorFromConfig.collector); err != nil {
				return err
			}

			var g prometheus.Gatherer = reg

			// wrap our gatherer in ProbeGatherer, to only collect metrics when necessary
			g = registry.NewProbeGatherer(g, collectorFromConfig.collector.RefreshRate > time.Minute)

			hash := labels.FromMap(collectorFromConfig.labels).Hash()

			refreshRate := collectorFromConfig.collector.RefreshRate
			creationDate := collectorFromConfig.collector.CreationDate

			if refreshRate > 0 {
				// We want public probe to run at known time
				hash = uint64(creationDate.UnixNano()) % uint64(refreshRate)
			}

			// this weird "dance" where we create a registry and add it to the registererGatherer
			// for each probe is the product of our unability to expose a "__meta_something"
			// label while doing Collect(). We end up adding the meta labels statically at
			// registration.
			id, err := m.registry.RegisterGatherer(
				registry.RegistrationOption{
					Description: "blackbox for " + collectorFromConfig.collector.URL,
					JitterSeed:  hash,
					Interval:    collectorFromConfig.collector.RefreshRate,
					ExtraLabels: collectorFromConfig.labels,
				},
				g,
				true,
			)
			if err != nil {
				return err
			}

			if refreshRate > 0 && time.Since(creationDate) < refreshRate {
				// For new monitor, trigger a schedule immediately
				m.registry.ScheduleScrape(id, time.Now())
			}

			m.registrations[id] = gathererWithConfigTarget{
				target:   collectorFromConfig.collector,
				gatherer: g,
			}

			logger.V(2).Printf("New probe registered for '%s'", collectorFromConfig.collector.Name)
		}
	}

	// unregister any obsolete probe
	for idx, gatherer := range m.registrations {
		if gatherer.target.BleemeoAgentID != "" && !gathererInArray(gatherer, m.targets) {
			logger.V(2).Printf("The probe for '%s' is now deactivated", gatherer.target.Name)

			m.registry.Unregister(idx)
			delete(m.registrations, idx)
		}
	}

	return nil
}
