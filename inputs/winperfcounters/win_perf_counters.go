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
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/win_perf_counters"
	"github.com/influxdata/toml"
	"github.com/influxdata/toml/ast"
	"github.com/shirou/gopsutil/mem"
)

const diskIOModuleName string = "win_diskio"
const memModuleName string = "win_mem"
const swapModuleName string = "win_swap"
const config string = `
[[inputs.win_perf_counters]]
  [[inputs.win_perf_counters.object]]
    ObjectName = "PhysicalDisk"
    Instances = ["*"]
    Counters = [
      "Disk Read Bytes/sec",
      "Disk Write Bytes/sec",
      "Current Disk Queue Length",
      "Disk Reads/sec",
      "Disk Writes/sec",
      "% Idle Time",
    ]
    Measurement = "win_diskio"

  [[inputs.win_perf_counters.object]]
    # Example query where the Instance portion must be removed to get data back,
    # such as from the Memory object.
    ObjectName = "Memory"
    Counters = [
      "Available Bytes",
      "Standby Cache Reserve Bytes",
      "Standby Cache Normal Priority Bytes",
      "Standby Cache Core Bytes",
    ]
    # Use 6 x - to remove the Instance bit from the query.
    Instances = ["------"]
    Measurement = "win_mem"

  [[inputs.win_perf_counters.object]]
    # Example query where the Instance portion must be removed to get data back,
    # such as from the Paging File object.
    ObjectName = "Paging File"
    Counters = [
      "% Usage",
    ]
    Instances = ["_Total"]
    Measurement = "win_swap"`

type winCollector struct {
	option      inputs.CollectorConfig
	totalMemory uint64
	totalSwap   uint64
}

// New initialise win_perf_counters.Input.
func New(inputsConfig inputs.CollectorConfig) (result telegraf.Input, err error) {
	input, ok := telegraf_inputs.Inputs["win_perf_counters"]
	if !ok {
		return result, errors.New("input 'win_perf_counters' is not enabled in Telegraf")
	}

	tmpInput := input()

	winInput, ok := tmpInput.(*win_perf_counters.Win_PerfCounters)
	if !ok {
		return result, fmt.Errorf("invalid type for telegraf input 'win_perf_counters', got %T, expected *win_perf_counters.Win_PerfCounters", tmpInput)
	}

	var parsedConfig *ast.Table

	parsedConfig, err = toml.Parse([]byte(config))
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

	memInfo, err := mem.VirtualMemory()
	if err != nil {
		return result, err
	}

	swapInfo, err := mem.SwapMemory()
	if err != nil {
		return result, err
	}

	option := winCollector{
		option:      inputsConfig,
		totalMemory: memInfo.Total,
		totalSwap:   swapInfo.Total,
	}

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
	res := make(map[string]float64, len(fields))

	if currentContext.Measurement == diskIOModuleName {
		if freePerc, present := fields["Percent_Idle_Time"]; present {
			res["utilization"] = 100. - freePerc/float64(runtime.NumCPU())
			// io_time is the number of ms spent doing IO in the last second.
			// utilization is 100% when we spent 1000ms during one second
			res["time"] = fields["utilization"] * 1000. / 100.
		}
	}

	if currentContext.Measurement == memModuleName {
		totalMemory := float64(c.totalMemory)

		if val, present := fields["Available_Bytes"]; present {
			res["available"] = val
			res["available_perc"] = val * 100. / totalMemory
			res["used"] = totalMemory - val
			res["used_perc"] = res["used"] * 100. / totalMemory
		}

		cacheReserve, p1 := fields["Standby_Cache_Reserve_Bytes"]
		cacheNormal, p2 := fields["Standby_Cache_Normal_Priority_Bytes"]
		cacheCore, p3 := fields["Standby_Cache_Core_Bytes"]

		if !(p1 && p2 && p3) {
			return res
		}

		res["cached"] = cacheCore + cacheNormal + cacheReserve
		res["free"] = totalMemory - res["used"] - res["cached"]
		res["buffered"] = 0.
	}

	if currentContext.Measurement == swapModuleName {
		if val, present := fields["Percent_Usage"]; present {
			res["used_perc"] = val
		}
	}

	return res
}

func (c winCollector) renameMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, metricName string) (newMeasurement string, newMetricName string) {
	newMeasurement, newMetricName = currentContext.Measurement, metricName

	if currentContext.Measurement == diskIOModuleName {
		switch metricName {
		case "utilization", "time":
			newMeasurement = "io"
		}
	}

	if currentContext.Measurement == memModuleName {
		switch metricName {
		case "available", "available_perc", "used", "used_perc", "cached", "free", "buffered":
			newMeasurement = "mem"
		}
	}

	if currentContext.Measurement == swapModuleName {
		if metricName == "used_perc" {
			newMeasurement = "swap"
		}
	}

	return
}
