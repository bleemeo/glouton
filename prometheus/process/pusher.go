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

//go:build linux
// +build linux

package process

import (
	"glouton/types"
	"time"

	"github.com/ncabatoff/process-exporter/proc"
)

type pusher struct {
	exporter *Exporter
	pusher   types.PointPusher

	lastCount map[string]proc.Counts
	lastTime  map[string]time.Time
}

func (p *pusher) push(t0 time.Time) {
	p.exporter.init()
	p.exporter.l.Lock()
	defer p.exporter.l.Unlock()

	if p.lastCount == nil {
		p.lastCount = make(map[string]proc.Counts)
		p.lastTime = make(map[string]time.Time)
	}

	now := time.Now()
	permErrs, groups, err := p.exporter.grouper.Update(p.exporter.Source.AllProcs())

	p.exporter.scrapePartialErrors += permErrs.Partial

	if err != nil {
		p.exporter.scrapeErrors++
	} else {
		for gname, gcounts := range groups {
			if gcounts.Procs > 0 {
				p.exporter.groupActive[gname] = true
			}

			if !p.exporter.groupActive[gname] {
				continue
			}

			// we set the group inactive after testing for inactive group & skipping
			// to allow emitting metrics one last time
			if gcounts.Procs == 0 {
				p.exporter.groupActive[gname] = false
			}

			previous := p.lastCount[gname]
			previousTime := p.lastTime[gname]
			delta := gcounts.Counts.Sub(previous)

			p.lastCount[gname] = gcounts.Counts
			p.lastTime[gname] = now

			memBytes := float64(gcounts.ResidentBytes)
			if gcounts.ProportionalBytes > 0 {
				memBytes = float64(gcounts.ProportionalBytes)
			}

			points := []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "process_num_procs",
						"group_name":    gname,
					},
					Point: types.Point{
						Time:  t0,
						Value: float64(gcounts.Procs),
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: gname,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "process_mem_bytes",
						"group_name":    gname,
					},
					Point: types.Point{
						Time:  t0,
						Value: memBytes,
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: gname,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "process_open_filedesc",
						"group_name":    gname,
					},
					Point: types.Point{
						Time:  t0,
						Value: float64(gcounts.OpenFDs),
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: gname,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "process_worst_fd_ratio",
						"group_name":    gname,
					},
					Point: types.Point{
						Time:  t0,
						Value: gcounts.WorstFDratio,
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: gname,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "process_num_threads",
						"group_name":    gname,
					},
					Point: types.Point{
						Time:  t0,
						Value: float64(gcounts.NumThreads),
					},
					Annotations: types.MetricAnnotations{
						BleemeoItem: gname,
					},
				},
			}
			fields := map[string]interface{}{
				"num_procs":      gcounts.Procs,
				"mem_bytes":      gcounts.ResidentBytes,
				"open_filedesc":  gcounts.OpenFDs,
				"worst_fd_ratio": gcounts.WorstFDratio,
				"num_threads":    gcounts.NumThreads,
			}
			if gcounts.ProportionalBytes > 0 {
				fields["mem_bytes"] = gcounts.ProportionalBytes
			}
			if !previousTime.IsZero() {
				deltaT := now.Sub(previousTime).Seconds()
				points = append(
					points,
					types.MetricPoint{
						Labels: map[string]string{
							types.LabelName: "process_cpu_user",
							"group_name":    gname,
						},
						Point: types.Point{
							Time:  t0,
							Value: delta.CPUUserTime / deltaT * 100,
						},
						Annotations: types.MetricAnnotations{
							BleemeoItem: gname,
						},
					},
					types.MetricPoint{
						Labels: map[string]string{
							types.LabelName: "process_cpu_system",
							"group_name":    gname,
						},
						Point: types.Point{
							Time:  t0,
							Value: delta.CPUSystemTime / deltaT * 100,
						},
						Annotations: types.MetricAnnotations{
							BleemeoItem: gname,
						},
					},
					types.MetricPoint{
						Labels: map[string]string{
							types.LabelName: "process_major_fault",
							"group_name":    gname,
						},
						Point: types.Point{
							Time:  t0,
							Value: float64(delta.MajorPageFaults) / deltaT,
						},
						Annotations: types.MetricAnnotations{
							BleemeoItem: gname,
						},
					},
					types.MetricPoint{
						Labels: map[string]string{
							types.LabelName: "process_context_switch",
							"group_name":    gname,
						},
						Point: types.Point{
							Time:  t0,
							Value: float64(delta.CtxSwitchVoluntary+delta.CtxSwitchNonvoluntary) / deltaT,
						},
						Annotations: types.MetricAnnotations{
							BleemeoItem: gname,
						},
					},
					types.MetricPoint{
						Labels: map[string]string{
							types.LabelName: "process_io_read_bytes",
							"group_name":    gname,
						},
						Point: types.Point{
							Time:  t0,
							Value: float64(delta.ReadBytes) / deltaT,
						},
						Annotations: types.MetricAnnotations{
							BleemeoItem: gname,
						},
					},
					types.MetricPoint{
						Labels: map[string]string{
							types.LabelName: "process_io_write_bytes",
							"group_name":    gname,
						},
						Point: types.Point{
							Time:  t0,
							Value: float64(delta.WriteBytes) / deltaT,
						},
						Annotations: types.MetricAnnotations{
							BleemeoItem: gname,
						},
					},
				)
			}

			p.pusher.PushPoints(points)
		}
	}
}
