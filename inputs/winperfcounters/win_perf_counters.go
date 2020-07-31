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
	"math"
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

const (
	diskIOModuleName    string = "win_diskio"
	memModuleName       string = "win_mem"
	swapModuleName      string = "win_swap"
	processorModuleName string = "win_processor"
	systemModuleName    string = "win_system"
)

const config string = `
[[inputs.win_perf_counters]]
  [[inputs.win_perf_counters.object]]
    ObjectName = "System"
    Instances = ["*"]
    Counters = ["Processor Queue Length"]
    Measurement = "win_system"

  [[inputs.win_perf_counters.object]]
    ObjectName = "Processor"
    Instances = ["_Total"]
    Counters = ["% Idle Time"]
    Measurement = "win_processor"

  [[inputs.win_perf_counters.object]]
    ObjectName = "PhysicalDisk"
    Instances = ["*"]
    Counters = [
      "% Idle Time",
    ]
    IncludeTotal = true
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
	runLength   float64
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
			return result, errors.New("cannot find 'inputs' in the win_perfs_counters config")
		}

		if val, ok := inputsConfig.Fields["win_perf_counters"]; ok {
			winConfig, ok := val.([]*ast.Table)
			if !ok {
				return result, errors.New("cannot find toml parsedConfig inputs.win_perfs_counters in the win_perfs_counters config")
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

	option := &winCollector{
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

func (c *winCollector) renameGlobal(originalContext internal.GatherContext) (newContext internal.GatherContext, drop bool) {
	if originalContext.Measurement == diskIOModuleName {
		instance, present := originalContext.Tags["instance"]
		if !present {
			return originalContext, true
		}

		if instance == "_Total" {
			return originalContext, false
		}

		// 'instance' has a pattern '<DISK_NUMBER> (<PARTITION_NAME> )+', e.g. "0 C:" or "0 C: D:"
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
		originalContext.Annotations.BleemeoItem = instance

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

func (c *winCollector) transformMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	res := make(map[string]float64, len(fields))

	if currentContext.Measurement == diskIOModuleName {
		if freePerc, present := fields["Percent_Idle_Time"]; present {
			// we clamp the min value to zero as due to what I believe to be timing imprecisions a sightly negative value can be returned
			res["utilization"] = math.Max(0., 100.-freePerc)
			// io_time is the number of ms spent doing IO in the last second.
			// utilization is 100% when we spent 1000ms during one second
			res["time"] = res["utilization"] * 1000. / 100.
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

	if currentContext.Measurement == processorModuleName {
		if val, present := fields["Percent_Idle_Time"]; present {
			// Reproduce the behavior exhibited in the python agent: we estimate the load to be
			// the current cpu_usage + Processor Queue Length (the number of starved threads).
			res["load1"] = 1. - val/100. + c.runLength
		}
	}

	if currentContext.Measurement == systemModuleName {
		// we will have a offset of up to 10s, depending on the order of collection.
		// However, 'System' should be collected prior to the processor, so it should be pretty accurate.
		// And 10s of delay is probably ok too.
		if val, present := fields["Processor_Queue_Length"]; present {
			c.runLength = val
		}
	}

	return res
}

func (c winCollector) renameMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, metricName string) (string, string) {
	newMeasurement := currentContext.Measurement

	switch currentContext.Measurement {
	case diskIOModuleName:
		newMeasurement = "io"
	case memModuleName:
		newMeasurement = "mem"
	case swapModuleName:
		newMeasurement = "swap"
	case processorModuleName:
		newMeasurement = "system"
	}

	return newMeasurement, metricName
}
