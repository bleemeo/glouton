// Copyright 2015-2018 Bleemeo
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

// Memory metrics module

package system

import (
	"../types"
	"github.com/shirou/gopsutil/mem"
)

// MemoryCollector gather metrics about memory usages
//
// Memory usages is gathered across all memory available for the system.
// The following metrics are emitted:
// * mem_available: memory available for application in bytes.
// * mem_available_perc: memory available for application in percent.
// * mem_buffered: memory used for raw block cache in bytes.
// * mem_cached: memory used for file cache in bytes.
// * mem_free: memory unused in bytes.
// * mem_total: memory size in bytes.
// * mem_used: memory used by applications in bytes.
// * mem_used_perc: memory used by applications in percent.
type MemoryCollector struct {
	memAvailable     types.Metric
	memAvailablePerc types.Metric
	memBuffered      types.Metric
	memCached        types.Metric
	memFree          types.Metric
	memTotal         types.Metric
	memUsed          types.Metric
	memUsedPerc      types.Metric
}

// InitMemoryCollector initializes a MemoryCollector struct.
func InitMemoryCollector() MemoryCollector {
	return MemoryCollector{
		memAvailable: types.Metric{
			Name:  "mem_available",
			Tag:   make(map[string]string),
			Chart: "",
			Unit:  types.Bytes,
		},
		memAvailablePerc: types.Metric{
			Name:  "mem_available_perc",
			Tag:   make(map[string]string),
			Chart: "",
			Unit:  types.Percent,
		},
		memBuffered: types.Metric{
			Name:  "mem_buffered",
			Tag:   make(map[string]string),
			Chart: "memory_bytes",
			Unit:  types.Bytes,
		},
		memCached: types.Metric{
			Name:  "mem_cached",
			Tag:   make(map[string]string),
			Chart: "memory_bytes",
			Unit:  types.Bytes,
		},
		memFree: types.Metric{
			Name:  "mem_free",
			Tag:   make(map[string]string),
			Chart: "memory_bytes",
			Unit:  types.Bytes,
		},
		memTotal: types.Metric{
			Name:  "mem_total",
			Tag:   make(map[string]string),
			Chart: "",
			Unit:  types.Bytes,
		},
		memUsed: types.Metric{
			Name:  "mem_used",
			Tag:   make(map[string]string),
			Chart: "memory_bytes",
			Unit:  types.Bytes,
		},
		memUsedPerc: types.Metric{
			Name:  "mem_used_perc",
			Tag:   make(map[string]string),
			Chart: "memory_percent",
			Unit:  types.Percent,
		},
	}
}

// Gather returns a MetricPoint for each metric of MemoryCollector.
//MetricPoints are returns in a list.
func (memoryCollector MemoryCollector) Gather() []types.MetricPoint {
	var virtualMemory, virtualMemoryError = mem.VirtualMemory()
	var result [8]types.MetricPoint
	if virtualMemoryError == nil {
		result[0] = types.MetricPoint{
			Metric: &memoryCollector.memAvailable,
			Value:  float64(virtualMemory.Available),
		}
		result[1] = types.MetricPoint{
			Metric: &memoryCollector.memAvailablePerc,
			Value:  float64(virtualMemory.Available) / float64(virtualMemory.Total) * 100,
		}
		result[2] = types.MetricPoint{
			Metric: &memoryCollector.memBuffered,
			Value:  float64(virtualMemory.Buffers),
		}
		result[3] = types.MetricPoint{
			Metric: &memoryCollector.memCached,
			Value:  float64(virtualMemory.Cached),
		}
		result[4] = types.MetricPoint{
			Metric: &memoryCollector.memFree,
			Value:  float64(virtualMemory.Free),
		}
		result[5] = types.MetricPoint{
			Metric: &memoryCollector.memTotal,
			Value:  float64(virtualMemory.Total),
		}
		result[6] = types.MetricPoint{
			Metric: &memoryCollector.memUsed,
			Value:  float64(virtualMemory.Total) - float64(virtualMemory.Available),
		}
		result[7] = types.MetricPoint{
			Metric: &memoryCollector.memUsedPerc,
			Value:  float64(virtualMemory.UsedPercent),
		}
		return result[:]
	}
	return result[:0]
}
