// Copyright 2015-2022 Bleemeo
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
// +build windows

package windows

import (
	"fmt"
	"glouton/inputs"
	"glouton/logger"
	"glouton/prometheus/exporter/common"
	"time"

	"github.com/prometheus-community/windows_exporter/collector"
	"github.com/prometheus/client_golang/prometheus"
	"gopkg.in/alecthomas/kingpin.v2"
)

const maxScrapeDuration time.Duration = 9500 * time.Millisecond

func NewCollector(enabledCollectors []string, options inputs.CollectorConfig) (prometheus.Collector, error) {
	var args []string

	if len(options.IODiskWhitelist) > 0 {
		// this will not fail, as we checked the validity of this regexp earlier
		whitelist, _ := common.MergeREs(options.IODiskWhitelist)
		args = append(args, fmt.Sprintf("--collector.logical_disk.volume-whitelist=%s", whitelist))
	}

	if len(options.IODiskBlacklist) > 0 {
		// this will not fail, as we checked the validity of this regexp earlier
		blacklist, _ := common.MergeREs(options.IODiskBlacklist)
		args = append(args, fmt.Sprintf("--collector.logical_disk.volume-blacklist=%s", blacklist))
	}

	if len(options.NetIfBlacklist) > 0 {
		blacklistREs := make([]string, 0, len(options.NetIfBlacklist))

		for _, inter := range options.NetIfBlacklist {
			blacklistRE, err := common.ReFromPrefix(inter)
			if err != nil {
				logger.V(1).Printf("windows_exporter: failed to parse the network interface blacklist: %v", err)
			} else {
				blacklistREs = append(blacklistREs, blacklistRE)
			}
		}

		// this will not fail, as we checked the validity of every regexp earlier
		blacklist, _ := common.ReFromREs(blacklistREs)
		args = append(args, fmt.Sprintf("--collector.net.nic-blacklist=%s", blacklist))
	}

	if _, err := kingpin.CommandLine.Parse(args); err != nil {
		return nil, fmt.Errorf("windows_exporter: kingpin initialization failed: %w", err)
	}

	collectors := map[string]collector.Collector{}

	for _, name := range enabledCollectors {
		c, err := collector.Build(name)
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
