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

package process

import (
	"time"

	"github.com/influxdata/telegraf"
	"github.com/ncabatoff/process-exporter/proc"
)

type input struct {
	exporter *Exporter

	lastCount map[string]proc.Counts
	lastTime  map[string]time.Time
}

func (i *input) Description() string {
	return "Monitoring of processes group"
}

func (i *input) SampleConfig() string {
	return ""
}

func (i *input) Gather(acc telegraf.Accumulator) error {
	i.exporter.init()
	i.exporter.l.Lock()
	defer i.exporter.l.Unlock()

	if i.lastCount == nil {
		i.lastCount = make(map[string]proc.Counts)
		i.lastTime = make(map[string]time.Time)
	}

	now := time.Now()
	permErrs, groups, err := i.exporter.grouper.Update(i.exporter.source.AllProcs())

	i.exporter.scrapePartialErrors += permErrs.Partial

	if err != nil {
		i.exporter.scrapeErrors++
	} else {
		for gname, gcounts := range groups {
			if gcounts.Procs > 0 {
				i.exporter.groupActive[gname] = true
			}

			if !i.exporter.groupActive[gname] {
				continue
			}

			// we set the group inactive after testing for inactive group & skipping
			// to allow emitting metrics one last time
			if gcounts.Procs == 0 {
				i.exporter.groupActive[gname] = false
			}

			previous := i.lastCount[gname]
			previousTime := i.lastTime[gname]
			delta := gcounts.Counts.Sub(previous)

			i.lastCount[gname] = gcounts.Counts
			i.lastTime[gname] = now

			fields := map[string]interface{}{
				"num_procs":      gcounts.Procs,
				"mem_bytes":      gcounts.ResidentBytes,
				"open_filedesc":  gcounts.OpenFDs,
				"worst_fd_ratio": gcounts.WorstFDratio,
				"num_threads":    gcounts.NumThreads,
			}
			if !previousTime.IsZero() {
				deltaT := now.Sub(previousTime).Seconds()
				if deltaT > 0 {
					fields["cpu_user"] = delta.CPUUserTime / deltaT * 100
					fields["cpu_system"] = delta.CPUSystemTime / deltaT * 100
					fields["major_fault"] = float64(delta.MajorPageFaults) / deltaT
					fields["context_switch"] = float64(delta.CtxSwitchVoluntary+delta.CtxSwitchNonvoluntary) / deltaT
					fields["io_read_bytes"] = float64(delta.ReadBytes) / deltaT
					fields["io_write_bytes"] = float64(delta.WriteBytes) / deltaT
				}
			}

			tags := map[string]string{
				"item": gname,
			}

			acc.AddFields("process", fields, tags, now)
		}
	}

	return err
}
