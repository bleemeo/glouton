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
	"context"
	"glouton/facts"
	"glouton/logger"
	"glouton/types"
	"time"
)

const maxAge = 1 * time.Second

type processProvider interface {
	Processes(ctx context.Context, maxAge time.Duration) (processes map[int]facts.Process, err error)
}

type Input struct {
	ps     processProvider
	pusher types.PointPusher
}

// New initialise process.Input.
func New(ps processProvider, pusher types.PointPusher) Input {
	return Input{
		ps:     ps,
		pusher: pusher,
	}
}

// Gather send metrics to the PointPusher.
func (i Input) Gather() {
	ctx := context.Background()

	proc, err := i.ps.Processes(ctx, maxAge)
	if err != nil {
		logger.V(1).Printf("unable to gather process metrics: %v", err)
		return
	}

	// Glouton should always sent those counters
	counts := map[string]int{
		"sleeping": 0,
		"blocked":  0,
		"zombies":  0,
		"paging":   0,
		"running":  0,
		"stopped":  0,
	}
	total := 0
	totalThreads := 0

	for _, p := range proc {
		status := p.Status

		switch status {
		case "idle":
			// Merge idle & sleeping
			status = "sleeping"
		case "disk-sleep":
			status = "blocked"
		case "zombie":
			status = "zombies"
		case "?":
			status = "unknown"
		}

		counts[status]++
		total++

		totalThreads += p.NumThreads
	}

	now := time.Now()
	points := []types.MetricPoint{
		{
			Labels: map[string]string{
				types.LabelName: "process_total",
			},
			Point: types.Point{
				Time:  now,
				Value: float64(total),
			},
		},
		{
			Labels: map[string]string{
				types.LabelName: "process_total_threads",
			},
			Point: types.Point{
				Time:  now,
				Value: float64(totalThreads),
			},
		},
	}

	for name, count := range counts {
		points = append(points, types.MetricPoint{
			Labels: map[string]string{
				types.LabelName: "process_status_" + name,
			},
			Point: types.Point{
				Time:  now,
				Value: float64(count),
			},
		})
	}

	i.pusher.PushPoints(points)
}
