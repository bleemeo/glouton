// Copyright 2015-2023 Bleemeo
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

package zfs

import (
	"glouton/inputs"
	"glouton/inputs/internal"
	"glouton/types"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/zfs"
)

// New initialise zfs.Input.
func New() (telegraf.Input, *inputs.GathererOptions, error) {
	input, ok := telegraf_inputs.Inputs["zfs"]
	if !ok {
		return nil, nil, inputs.ErrDisabledInput
	}

	zfsInput, ok := input().(*zfs.Zfs)
	if !ok {
		return nil, nil, inputs.ErrUnexpectedType
	}

	// Glouton currently don't use metrics from Kstat. This will gain 2 sysctl process execution
	// each Gather (each 10 seconds), which seems a valuable gain.
	// We need to have at least one entry in that list, because empty list means default of all stats.
	zfsInput.KstatMetrics = []string{"vdev_cache_stats"}
	zfsInput.PoolMetrics = true

	internalInput := &internal.Input{
		Input:       zfsInput,
		Accumulator: internal.Accumulator{},
		Name:        "zfs",
	}

	options := &inputs.GathererOptions{
		Rules: []types.SimpleRule{
			{
				TargetName:  "disk_used",
				PromQLQuery: `sum by (item)(label_replace((sum by (pool)(zfs_pool_allocated)), "item", "$1", "pool", "(.*)"))`,
			},
			{
				TargetName:  "disk_used_perc",
				PromQLQuery: `sum by (item)(label_replace((sum by (pool)(zfs_pool_allocated)/(sum by (pool)(zfs_pool_size)))*100, "item", "$1", "pool", "(.*)"))`,
			},
			{
				TargetName:  "disk_total",
				PromQLQuery: `sum by (item)(label_replace((sum by (pool)(zfs_pool_size)), "item", "$1", "pool", "(.*)"))`,
			},
			{
				TargetName:  "disk_free",
				PromQLQuery: `sum by (item)(label_replace((sum by (pool)(zfs_pool_free)), "item", "$1", "pool", "(.*)"))`,
			},
		},
	}

	return internalInput, options, nil
}
