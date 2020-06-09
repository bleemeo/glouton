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
	"glouton/types"
	"time"

	"github.com/prometheus/blackbox_exporter/config"
	"github.com/prometheus/blackbox_exporter/prober"
	"github.com/prometheus/client_golang/prometheus"
)

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
		"http": prober.ProbeHTTP,
		"tcp":  prober.ProbeTCP,
		"icmp": prober.ProbeICMP,
		"dns":  prober.ProbeDNS,
	}
)

type target struct {
	url        string
	moduleName string
	module     config.Module
	timeout    int
}

// Describe implements the prometheus.Collector interface.
func (target *target) Describe(ch chan<- *prometheus.Desc) {
	ch <- probeSuccessDesc
	ch <- probeDurationDesc
}

type metricCollector interface {
	prometheus.Metric
	prometheus.Collector
}

// Collect implements the prometheus.Collector interface.
// It is where we do the actual "probing".
func (target *target) Collect(ch chan<- prometheus.Metric) {
	// set a timeout
	ctx, cancel := context.WithTimeout(context.Background(), target.module.Timeout)
	// Let's ensure we don't end up with stray queries running somewhere
	defer cancel()

	probeFn, present := probers[target.module.Prober]
	if !present {
		logger.V(1).Printf("blackbox_exporter: no prober registered under the name '%s', cannot check '%s'.",
			target.module.Prober, target.url)
		// notify the "client" that scraping this target resulted in an error
		ch <- prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, 0., target.url)
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

	extLogger := logger.GoKitLoggerWrapper(logger.V(2))
	start := time.Now()

	// do all the actual work
	success := probeFn(ctx, target.url, target.module, registry, extLogger)

	duration := time.Since(start).Seconds()

	mfs, err := registry.Gather()
	if err != nil {
		logger.V(1).Printf("blackbox_exporter: error while gathering metrics: %v", err)
		// notify the "client" that scraping this target resulted in an error
		ch <- prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, 0., target.url)
		return
	}

	hardcodedLabels := []labelPair{
		{name: "instance", value: target.url},
		// Exposing the module name allow us the client to differenciate probes in case the same URL is
		// scrapped by different modules.
		{name: "module", value: target.moduleName},
	}
	// write all the gathered metrics to our upper registry
	writeMFsToChan(mfs, hardcodedLabels, ch)

	successVal := 0.
	if success {
		successVal = 1
	}
	ch <- prometheus.MustNewConstMetric(probeDurationDesc, prometheus.GaugeValue, float64(duration), target.url)
	ch <- prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, successVal, target.url)
}

type CollectorWithLabels struct {
	Collector prometheus.Collector
	Labels    map[string]string
}

// NewCollector creates a new collector for blackbox_exporter
func NewCollector(options Options) ([]CollectorWithLabels, error) {
	unknownModules := []string{}
	collectors := []CollectorWithLabels{}

	// Extract the list of unknown modules specified by the users in glouton's configuration file
	// and build a list of prometheus Collectors (one per target)
OuterBreak:
	for _, curTarget := range options.Targets {
		module, present := options.BlackboxConfig.Modules[curTarget.ModuleName]
		// if the module is unknown, add it to the list
		if !present {
			// prevent duplicates
			for _, v := range unknownModules {
				if curTarget.ModuleName == v {
					continue OuterBreak
				}
			}
			unknownModules = append(unknownModules, curTarget.ModuleName)
			continue
		}

		// the user overrided the timeout value, let's respect his choice
		if curTarget.Timeout != 0 {
			module.Timeout = time.Duration(curTarget.Timeout) * time.Second
		}
		// neither blackbox's nor gloutons's config files specify a timeout, let's default to 10s,
		// as we do not have access to the scrape time to derive more precise timeouts, right ?
		if module.Timeout == 0 {
			module.Timeout = 10 * time.Second
		}

		collectors = append(collectors,
			CollectorWithLabels{
				Collector: &target{url: curTarget.URL, module: module, moduleName: curTarget.ModuleName},
				Labels: map[string]string{
					types.LabelProbeTarget: curTarget.URL,
				},
			})
	}

	if len(unknownModules) > 0 {
		return nil, fmt.Errorf("Unknown blackbox modules found in your configuration: %v. "+
			"Maybe check that these modules are present in your config file ?", unknownModules)
	}

	return collectors, nil
}
