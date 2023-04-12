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

package blackbox

import (
	"glouton/logger"
	"glouton/prometheus/model"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// filterMFs filter metric families to exclude some family.
func filterMFs(mfs []*dto.MetricFamily, include func(mf *dto.MetricFamily) bool) []*dto.MetricFamily {
	i := 0

	for _, mf := range mfs {
		if !include(mf) {
			continue
		}

		mfs[i] = mf
		i++
	}

	return mfs[:i]
}

// writeMFsToChan converts metrics families to new metrics, before writing them on the 'ch' channel.
func writeMFsToChan(mfs []*dto.MetricFamily, ch chan<- prometheus.Metric) {
	metrics, err := model.FamiliesToCollector(mfs)
	for _, metric := range metrics {
		ch <- metric
	}

	if err != nil {
		logger.V(1).Printf("blackbox_exporter: error while sending metrics %v", err)
	}
}
