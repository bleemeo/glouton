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

// +build windows

package winperfcounters

import (
	"errors"
	"fmt"
	"glouton/inputs"
	"glouton/inputs/internal"
	"io/ioutil"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/win_perf_counters"
	"github.com/influxdata/toml"
	"github.com/influxdata/toml/ast"
)

const diskIOModuleName string = "win_diskio"

type winCollector struct {
	option inputs.CollectorConfig
}

// New initialise win_perf_counters.Input.
func New(configFilePath string, inputsConfig inputs.CollectorConfig) (result telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["win_perf_counters"]
	if !ok {
		return result, errors.New("input 'win_perf_counters' is not enabled in Telegraf")
	}

	tmpInput := input()

	winInput, ok := tmpInput.(*win_perf_counters.Win_PerfCounters)
	if !ok {
		return result, fmt.Errorf("invalid type for telegraf input 'win_perf_counters', got %T, expected *win_perf_counters.Win_PerfCounters", tmpInput)
	}

	// load and parse the config, and "patch" the telegraf input with the decoded structure
	configFD, err := os.Open(configFilePath)
	if err != nil {
		return result, fmt.Errorf("couldn't load win_perf_counters' config: %v", err)
	}

	config, err := ioutil.ReadAll(configFD)
	if err != nil {
		return result, fmt.Errorf("reading in_perf_counters failed: %v", err)
	}

	var parsedConfig *ast.Table

	parsedConfig, err = toml.Parse(config)
	if err != nil {
		return result, err
	}

	if val, ok := parsedConfig.Fields["inputs"]; ok {
		inputsConfig, ok := val.(*ast.Table)
		if !ok {
			return result, errors.New("cannot find 'inputs' in your win_perfs_counters config file")
		}

		if val, ok := inputsConfig.Fields["win_perf_counters"]; ok {
			winConfig, ok := val.([]*ast.Table)
			if !ok {
				return result, errors.New("cannot find toml parsedConfig inputs.win_perfs_counters in win_perfs_counters config")
			}

			if len(winConfig) > 1 {
				return result, errors.New("running multiple win_perfs_counters instances simultaneously is not currently supported")
			}

			if len(winConfig) != 0 {
				if err = toml.UnmarshalTable(winConfig[0], &winInput); err != nil {
					return result, fmt.Errorf("cannot unmarshal inputs.win_perf_counters: %v", err)
				}
			}
		}
	}

	option := winCollector{option: inputsConfig}

	result = &internal.Input{
		Input: winInput,
		Accumulator: internal.Accumulator{
			TransformMetrics: option.transformMetrics,
			RenameMetrics:    option.renameMetrics,
			RenameGlobal:     option.renameGlobal,
		},
	}

	return result, nil
}

func (c winCollector) renameGlobal(originalContext internal.GatherContext) (newContext internal.GatherContext, drop bool) {
	if originalContext.Measurement == diskIOModuleName {
		instance, present := originalContext.Tags["instance"]
		if !present || instance == "_Total" {
			return originalContext, true
		}

		// 'instance' has a pattern '<DISK_NUMBER> (<PARTITION_NAME> )+', e.g. "0 C:" or "0_C:_D:"
		// (here we have two partitions on the same disk). We keep the lowest letter, as it is more
		// probably an essential device).
		splitInstance := strings.Split(instance, " ")
		if len(splitInstance) < 2 {
			return originalContext, false
		}

		if _, err := strconv.Atoi(splitInstance[0]); err != nil {
			return originalContext, false
		}

		partitions := splitInstance[1:]
		sort.Strings(partitions)

		instance = partitions[0]
		originalContext.Tags["instance"] = instance

		for _, r := range c.option.IODiskBlacklist {
			if r.MatchString(instance) {
				return originalContext, true
			}
		}

		// if the whitelist is empty, we retrieve all the disks that are not blacklisted
		// if it is not empty, we filter them with the whitelist
		keep := len(c.option.IODiskWhitelist) == 0
		if !keep {
			for _, r := range c.option.IODiskWhitelist {
				if r.MatchString(instance) {
					keep = true
					break
				}
			}
		}

		if !keep {
			return originalContext, true
		}
	}

	return originalContext, false
}

func (c winCollector) transformMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	if currentContext.Measurement == diskIOModuleName {
		if freePerc, present := fields["Percent_Idle_Time"]; present {
			fields["utilization"] = 100. - freePerc
			// io_time is the number of ms spent doing IO in the last second.
			// utilization is 100% when we spent 1000ms during one second
			fields["time"] = fields["utilization"] * 1000. / 100.
		}
	}

	return fields
}

func (c winCollector) renameMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, metricName string) (newMeasurement string, newMetricName string) {
	newMeasurement, newMetricName = currentContext.Measurement, metricName

	if currentContext.Measurement == diskIOModuleName {
		switch metricName {
		case "utilization", "time":
			newMeasurement = "io"
		}
	}

	return
}
