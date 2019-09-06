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

package api

import (
	"agentgo/types"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Return the most recent point. ok is false if no point are found
func getLastPoint(m types.Metric) (point types.Point, ok bool) {
	points, err := m.Points(time.Now().Add(-5*time.Minute), time.Now())
	if err != nil {
		return
	}
	for _, p := range points {
		ok = true
		if p.Time.After(point.Time) {
			point = p.Point
		}
	}
	return
}

// Describe implment Describe of a Prometheus collector
func (a API) Describe(chan<- *prometheus.Desc) {
}

// Collect implment Collect of a Prometheus collector
func (a API) Collect(ch chan<- prometheus.Metric) {
	metrics, err := a.db.Metrics(nil)
	if err != nil {
		return
	}
	for _, m := range metrics {
		if p, ok := getLastPoint(m); ok {
			labels := make([]string, 0)
			labelValues := make([]string, 0)
			for l, v := range m.Labels() {
				if l != "__name__" {
					labels = append(labels, l)
					labelValues = append(labelValues, v)
				}
			}
			ch <- prometheus.NewMetricWithTimestamp(p.Time, prometheus.MustNewConstMetric(
				prometheus.NewDesc(m.Labels()["__name__"], "", labels, nil),
				prometheus.UntypedValue,
				p.Value,
				labelValues...,
			))
		}
	}
}
