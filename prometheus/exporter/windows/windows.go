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
	"fmt"
	"time"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/logger"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alecthomas/kingpin/v2"
	"github.com/go-kit/log"
	"github.com/prometheus-community/windows_exporter/collector"
)

const maxScrapeDuration time.Duration = 9500 * time.Millisecond

func optionsToFlags(option inputs.CollectorConfig) map[string]string {
	result := make(map[string]string)

	if option.IODiskMatcher != nil {
		result["collector.logical_disk.volume-exclude"] = option.IODiskMatcher.AsDenyRegexp()
	}

	if option.NetIfMatcher != nil {
		result["collector.net.nic-exclude"] = option.NetIfMatcher.AsDenyRegexp()
	}

	return result
}

func setKingpinOptions(option inputs.CollectorConfig) error {
	// For option, it's now required to call RegisterCollectorsFlags
	app := kingpin.New("windows_exporter", "A metrics collector for Windows.")
	collector.RegisterCollectorsFlags(app)

	optionMap := optionsToFlags(option)
	args := make([]string, 0, len(optionMap))

	for key, value := range optionMap {
		args = append(args, fmt.Sprintf("--%s=%s", key, value))
	}

	logger.V(2).Printf("Starting node_exporter with %v as args", args)

	if _, err := app.Parse(args); err != nil {
		return fmt.Errorf("kingpin initialization: %w", err)
	}

	return nil
}

func NewCollector(enabledCollectors []string, options inputs.CollectorConfig) (prometheus.Collector, error) {
	return newCollector(enabledCollectors, options)
}

func newCollector(enabledCollectors []string, options inputs.CollectorConfig) (*windowsCollector, error) {
	if err := setKingpinOptions(options); err != nil {
		return nil, err
	}

	extLogger := log.With(logger.GoKitLoggerWrapper(logger.V(2)), "collector", "windows_exporter")

	collectors := map[string]collector.Collector{}

	collector.RegisterCollectors(extLogger)

	for _, name := range enabledCollectors {
		c, err := collector.Build(name, extLogger)
		if err != nil {
			logger.V(0).Printf("windows_exporter: couldn't build the list of collectors: %s", err)

			return nil, err
		}

		collectors[name] = c
	}

	logger.V(2).Printf("windows_exporter: the enabled collectors are %v", keys(collectors))

	return &windowsCollector{collectors: collectors, maxScrapeDuration: maxScrapeDuration}, nil
}

func keys(m map[string]collector.Collector) []string {
	ret := make([]string, 0, len(m))

	for key := range m {
		ret = append(ret, key)
	}

	return ret
}
