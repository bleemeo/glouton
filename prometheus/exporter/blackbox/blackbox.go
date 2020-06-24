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
	"glouton/types"
	"time"

	"github.com/prometheus/blackbox_exporter/config"
	"github.com/prometheus/blackbox_exporter/prober"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	proberNameHTTP string = "http"
	proberNameTCP  string = "tcp"
	proberNameICMP string = "icmp"
	proberNameDNS  string = "dns"
)

// nolint: gochecknoglobals
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
	Conf Config = Config{
		Targets: []ConfigTarget{},
		Modules: map[string]config.Module{},
	}
	Manager = registerManager{
		registrations: map[int]collectorWithLabels{},
	}
)

type target struct {
	name   string
	url    string
	module config.Module
}

// Describe implements the prometheus.Collector interface.
func (target *target) Describe(ch chan<- *prometheus.Desc) {
	ch <- probeSuccessDesc
	ch <- probeDurationDesc
}

// Collect implements the prometheus.Collector interface.
// It is where we do the actual "probing".
func (target *target) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), target.module.Timeout)
	// Let's ensure we don't end up with stray queries running somewhere
	defer cancel()

	probeFn, present := probers[target.module.Prober]
	if !present {
		logger.V(1).Printf("blackbox_exporter: no module registered under the name '%s', cannot check '%s'.",
			target.module.Prober, target.name)
		// notify the "client" that scraping this target resulted in an error
		ch <- prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, 0., target.name)

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

	extLogger := logger.GoKitLoggerWrapper(logger.V(3))
	start := time.Now()

	// do all the actual work
	success := probeFn(ctx, target.url, target.module, registry, extLogger)

	duration := time.Since(start).Seconds()

	mfs, err := registry.Gather()
	if err != nil {
		logger.V(1).Printf("blackbox_exporter: error while gathering metrics: %v", err)
		// notify the "client" that scraping this target resulted in an error
		ch <- prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, 0., target.name)

		return
	}

	// write all the gathered metrics to our upper registry
	writeMFsToChan(mfs, ch)

	successVal := 0.
	if success {
		successVal = 1
	}
	ch <- prometheus.MustNewConstMetric(probeDurationDesc, prometheus.GaugeValue, duration, target.name)
	ch <- prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, successVal, target.name)
}

// We define labels to apply on a specific collector at registration, as those labels cannot be exposed
// while gathering (e.g. labels prefixed by '__').
type collectorWithLabels struct {
	collector prometheus.Collector
	labels    map[string]string
	target    ConfigTarget
}

// registerManager is an abstraction that allows us to reload blackbox at runtime, enabling and disabling
// probes at will.
type registerManager struct {
	registrations map[int]collectorWithLabels
	registry      *registry.Registry
}

func newCollector(ctarget ConfigTarget, conf Config) (*collectorWithLabels, error) {
	module, present := conf.Modules[ctarget.ModuleName]
	// if the module is unknown, add it to the list
	if !present {
		return nil, fmt.Errorf("unknown blackbox module found in your configuration for %s (module '%v'). "+
			"This is a bug, please contact us", ctarget.Name, ctarget.ModuleName)
	}

	return &collectorWithLabels{
		collector: &target{url: ctarget.URL, module: module, name: ctarget.Name},
		labels: map[string]string{
			types.LabelMetaProbeTarget: ctarget.Name,
			types.LabelMetaMetricKind:  types.MonitorMetricKind.String(),
			// Exposing the module name allows the client to differentiate probes when
			// the same URL is scrapped by different modules.
			"module": ctarget.ModuleName,
		},
		target: ctarget,
	}, nil
}

// Register registers blackbox_exporter in our internal registry.
func Register(r *registry.Registry) {
	Manager.registry = r
}

func inMap(value collectorWithLabels, iterable map[int]collectorWithLabels) bool {
	for _, arrayValue := range iterable {
		if value.target == arrayValue.target {
			return true
		}
	}

	return false
}

func inArray(value collectorWithLabels, iterable []collectorWithLabels) bool {
	for _, arrayValue := range iterable {
		if value.target == arrayValue.target {
			return true
		}
	}

	return false
}

// UpdateRegistrations registers and deregisters collectors to sync the internal state with the configuration.
func UpdateRegistrations() error {
	if Manager.registry == nil {
		return fmt.Errorf("the registry is not defined for blackbox yet")
	}

	collectorsFromConfig := make([]collectorWithLabels, 0, len(Conf.Targets))

	for _, configCollector := range Conf.Targets {
		collectorFromConfig, err := newCollector(configCollector, Conf)
		if err != nil {
			return err
		}

		collectorsFromConfig = append(collectorsFromConfig, *collectorFromConfig)
	}

	// register new probes
	for _, collectorFromConfig := range collectorsFromConfig {
		if !inMap(collectorFromConfig, Manager.registrations) {
			// this weird "dance" where we create a registry and add it to the registererGatherer
			// for each probe is the product of our unability to expose a "__meta_something"
			// label while doing Collect(). We end up adding the meta labels statically at
			// registration.
			reg := prometheus.NewRegistry()

			if err := reg.Register(collectorFromConfig.collector); err != nil {
				return err
			}

			id, err := Manager.registry.RegisterGatherer(reg, nil, collectorFromConfig.labels)
			if err != nil {
				return err
			}

			Manager.registrations[id] = collectorFromConfig
			logger.V(2).Printf("New probe registered for '%s'", collectorFromConfig.target.Name)
		}
	}

	// unregister any obsolete probe
	for idx, managerCollector := range Manager.registrations {
		if !managerCollector.target.FromStaticConfig && !inArray(managerCollector, collectorsFromConfig) {
			logger.V(2).Printf("The probe for '%s' is now deactivated", managerCollector.target.Name)
			Manager.registry.UnregisterGatherer(idx)
			delete(Manager.registrations, idx)
		}
	}

	return nil
}
