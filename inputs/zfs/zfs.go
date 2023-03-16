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
	"glouton/prometheus/model"
	"glouton/types"
	"sort"

	"github.com/gogo/protobuf/proto"
	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/zfs"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/model/labels"
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
		GatherModifier: addZPoolStatus,
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

func addZPoolStatus(mfs []*dto.MetricFamily) []*dto.MetricFamily {
	// ZFS size metric looks like:
	// zfs_pool_size{health="DEGRADED",instance="truenas.local:8015",instance_uuid="f38fd694-a956-463b-96e4-e67a201eb15b",pool="with space"} 2.952790016e+09 1678958720157
	// zfs_pool_size{health="ONLINE",instance="truenas.local:8015",instance_uuid="f38fd694-a956-463b-96e4-e67a201eb15b",pool="boot-pool"} 1.0200547328e+10 1678958720157
	// So we can known the pool name & status
	poolHealth := make(map[string]string)

	for _, mf := range mfs {
		if mf.GetName() != "zfs_pool_size" {
			continue
		}

		for _, serie := range mf.GetMetric() {
			name := ""
			health := ""

			for _, labelPair := range serie.GetLabel() {
				if labelPair.GetName() == "pool" {
					name = labelPair.GetValue()
				}

				if labelPair.GetName() == "health" {
					health = labelPair.GetValue()
				}
			}

			if name != "" && health != "" {
				poolHealth[name] = health
			}
		}
	}

	if len(poolHealth) == 0 {
		return mfs
	}

	for name, health := range poolHealth {
		mf := &dto.MetricFamily{
			Name: proto.String("zfs_pool_health_status"),
			Type: dto.MetricType_GAUGE.Enum(),
		}

		var status types.Status

		switch health {
		case "ONLINE":
			status = types.StatusOk
		case "DEGRADED":
			status = types.StatusWarning
		case "OFFLINE":
			status = types.StatusWarning
		default:
			status = types.StatusCritical
		}

		annotation := types.MetricAnnotations{
			Status: types.StatusDescription{
				CurrentStatus:     status,
				StatusDescription: "ZFS pool is " + health,
			},
		}

		statusLabels := model.AnnotationToMetaLabels(
			labels.FromStrings("item", name),
			annotation,
		)

		var dtoLabel []*dto.LabelPair

		for _, lbl := range statusLabels {
			dtoLabel = append(dtoLabel, &dto.LabelPair{
				Name:  proto.String(lbl.Name),
				Value: proto.String(lbl.Value),
			})
		}

		mf.Metric = append(mf.Metric, &dto.Metric{
			Label: dtoLabel,
			Gauge: &dto.Gauge{
				Value: proto.Float64(float64(annotation.Status.CurrentStatus.NagiosCode())),
			},
		})

		mfs = append(mfs, mf)
	}

	sort.Slice(mfs, func(i, j int) bool {
		return mfs[i].GetName() < mfs[j].GetName()
	})

	return mfs
}
