// Copyright 2015-2022 Bleemeo
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
	"fmt"
	"glouton/facts"
	"glouton/logger"
	"glouton/prometheus/model"
	"glouton/types"
	"time"

	"github.com/prometheus/prometheus/storage"
)

const maxAge = 1 * time.Second

type processProvider interface {
	Processes(ctx context.Context, maxAge time.Duration) (processes map[int]facts.Process, err error)
}

// StatusSource collects process status metrics.
type StatusSource struct {
	ps processProvider
}

// NewStatusSource initializes a StatusSource.
func NewStatusSource(ps processProvider) StatusSource {
	return StatusSource{ps: ps}
}

// Collect sends process metrics to the Appender.
func (s StatusSource) Collect(ctx context.Context, app storage.Appender) error {
	proc, err := s.ps.Processes(ctx, maxAge)
	if err != nil {
		return fmt.Errorf("unable to gather process metrics: %w", err)
	}

	// Glouton should always send those counters.
	counts := map[string]int{
		"sleeping": 0,
		"blocked":  0,
		"zombies":  0,
		"running":  0,
		"stopped":  0,
	}
	total := 0
	totalThreads := 0

	for _, p := range proc {
		status := p.Status

		switch status {
		case facts.ProcessStatusIdle, facts.ProcessStatusSleeping:
			// Merge idle & sleeping
			counts["sleeping"]++
		case facts.ProcessStatusRunning:
			counts["running"]++
		case facts.ProcessStatusStopped, facts.ProcessStatusDead, facts.ProcessStatusTracingStop:
			counts["stopped"]++
		case facts.ProcessStatusIOWait:
			counts["blocked"]++
		case facts.ProcessStatusZombie:
			counts["zombies"]++
		case facts.ProcessStatusUnknown:
			logger.V(2).Printf("Process %v has status unknown, assume sleeping", p)

			counts["sleeping"]++
		default:
			logger.V(2).Printf("Process %v has status unknown (%#v), assume sleeping", p, status)

			counts["sleeping"]++
		}

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

	err = model.SendPointsToAppender(points, app)
	if err != nil {
		return fmt.Errorf("send points to appender: %w", err)
	}

	return app.Commit()
}
