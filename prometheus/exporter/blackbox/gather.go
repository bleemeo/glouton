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

package blackbox

import (
	"glouton/logger"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// writeMFsToChan converts metrics families to new metrics, before writing them on the 'ch' channel.
func writeMFsToChan(mfs []*dto.MetricFamily, ch chan<- prometheus.Metric) {
	for _, mf := range mfs {
		metrics := mf.GetMetric()
		if len(metrics) == 0 {
			continue
		}

		for _, metric := range metrics {
			labels := []string{}

			for _, labelPair := range metric.GetLabel() {
				// add a label, if it isn't already registered
				found := false

				for _, v := range labels {
					if v == *labelPair.Name {
						found = true
						break
					}
				}

				if !found {
					labels = append(labels, *labelPair.Name)
				}
			}

			desc := prometheus.NewDesc(
				prometheus.BuildFQName("", "", mf.GetName()),
				mf.GetHelp(),
				labels,
				nil,
			)

			labelsValues := []string{}
			// let's take great care to preserve the order of the labels, or weird things are gonna happen
			for _, label := range labels {
				labelValue := ""

				for _, v := range metric.GetLabel() {
					if *v.Name == label {
						labelValue = *v.Value
						break
					}
				}

				labelsValues = append(labelsValues, labelValue)
			}

			// in theory, this should only be a counter or a gauge, given the fact that we only do this probing operation once (and then we start again from scratch)
			switch {
			case metric.GetCounter() != nil:
				ch <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, metric.GetCounter().GetValue(), labelsValues...)
			case metric.GetGauge() != nil:
				ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, metric.GetGauge().GetValue(), labelsValues...)
			default:
				logger.V(1).Printf("blackbox_exporter: invalid type supplied to a probe, got %v", metric)
			}
		}
	}
}
