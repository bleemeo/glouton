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

//go:build linux

package process

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	"github.com/ncabatoff/process-exporter/proc"
	"github.com/prometheus/prometheus/storage"
)

var errNotProcIter = errors.New("iter isn't a proc.Iter")

// bleemeoExporter is similar to process exporter with metrics renamed.
type bleemeoExporter struct {
	exporter *Exporter

	lastCount map[string]proc.Counts
	lastTime  map[string]time.Time
}

// Collect sends process metrics to the Appender.
func (b *bleemeoExporter) CollectWithState(_ context.Context, state registry.GatherState, app storage.Appender) error {
	points, err := b.points(state.T0)
	if err != nil {
		return err
	}

	err = model.SendPointsToAppender(points, app)
	if err != nil {
		return fmt.Errorf("send points to appender: %w", err)
	}

	return app.Commit()
}

// points returns the points to send to the appender.
func (b *bleemeoExporter) points(t0 time.Time) ([]types.MetricPoint, error) {
	b.exporter.init()

	b.exporter.l.Lock()
	defer b.exporter.l.Unlock()

	if b.lastCount == nil {
		b.lastCount = make(map[string]proc.Counts)
		b.lastTime = make(map[string]time.Time)
	}

	now := time.Now()

	iter, ok := b.exporter.Source().(proc.Iter)
	if !ok {
		b.exporter.scrapeErrors++

		return nil, errNotProcIter
	}

	permErrs, groups, err := b.exporter.grouper.Update(iter)

	b.exporter.scrapePartialErrors += permErrs.Partial

	if err != nil {
		b.exporter.scrapeErrors++

		return nil, fmt.Errorf("update processes: %w", err)
	}

	// We get a maximum of 11 metrics per process.
	const nbMetricPerGroup = 11

	points := make([]types.MetricPoint, 0, nbMetricPerGroup*len(groups))

	for gname, gcounts := range groups {
		if gcounts.Procs > 0 {
			b.exporter.groupActive[gname] = true
		}

		if !b.exporter.groupActive[gname] {
			continue
		}

		// We set the group inactive after testing for inactive group & skipping
		// to allow emitting metrics one last time.
		if gcounts.Procs == 0 {
			b.exporter.groupActive[gname] = false
		}

		previous := b.lastCount[gname]
		previousTime := b.lastTime[gname]
		delta := gcounts.Counts.Sub(previous)

		b.lastCount[gname] = gcounts.Counts
		b.lastTime[gname] = now

		memBytes := float64(gcounts.ResidentBytes)
		if gcounts.ProportionalBytes > 0 {
			memBytes = float64(gcounts.ProportionalBytes)
		}

		fields := make(map[string]float64, nbMetricPerGroup)

		fields["num_procs"] = float64(gcounts.Procs)
		fields["mem_bytes"] = memBytes
		fields["open_filedesc"] = float64(gcounts.OpenFDs)
		fields["worst_fd_ratio"] = gcounts.WorstFDratio
		fields["num_threads"] = float64(gcounts.NumThreads)

		if !previousTime.IsZero() {
			deltaT := now.Sub(previousTime).Seconds()

			fields["cpu_user"] = delta.CPUUserTime / deltaT * 100
			fields["cpu_system"] = delta.CPUSystemTime / deltaT * 100
			fields["major_fault"] = float64(delta.MajorPageFaults) / deltaT
			fields["context_switch"] = float64(delta.CtxSwitchVoluntary+delta.CtxSwitchNonvoluntary) / deltaT
			fields["io_read_bytes"] = float64(delta.ReadBytes) / deltaT
			fields["io_write_bytes"] = float64(delta.WriteBytes) / deltaT
		}

		for name, value := range fields {
			points = append(points,
				types.MetricPoint{
					Labels: map[string]string{
						types.LabelName: "process_" + name,
						types.LabelItem: gname,
					},
					Point: types.Point{
						Time:  t0,
						Value: value,
					},
				},
			)
		}
	}

	return points, nil
}
