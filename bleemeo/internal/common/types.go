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

package common

import (
	"fmt"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/types"
)

// Maximal length of fields on Bleemeo API.
const (
	APIMetricItemLength          int = 100
	APIMetricItemLengthIfService int = 50
)

// MetricLabelItem is the couple (Label, Item) which uniquely identify a metric.
type MetricLabelItem struct {
	Label string
	Item  string
}

// TruncateItem truncate the item to match maximal length allowed by Bleemeo API.
func (key *MetricLabelItem) TruncateItem(isService bool) {
	if len(key.Item) > APIMetricItemLength {
		key.Item = key.Item[:APIMetricItemLength]
	}

	if isService && len(key.Item) > APIMetricItemLengthIfService {
		key.Item = key.Item[:APIMetricItemLengthIfService]
	}
}

func (key MetricLabelItem) String() string {
	if key.Item != "" {
		return fmt.Sprintf("%s (item %s)", key.Label, key.Item)
	}

	return key.Label
}

// MetricLabelItemFromMetric create a MetricLabelItem from a local or remote metric (or labels of local one).
func MetricLabelItemFromMetric(input interface{}) MetricLabelItem {
	if metric, ok := input.(bleemeoTypes.Metric); ok {
		key := MetricLabelItem{Label: metric.Label, Item: metric.Labels["item"]}
		key.TruncateItem(metric.ServiceID != "")

		return key
	}

	if metric, ok := input.(types.Metric); ok {
		labels := metric.Labels()
		key := MetricLabelItem{Label: labels["__name__"], Item: labels["item"]}
		key.TruncateItem(labels["service_name"] != "")

		return key
	}

	if labels, ok := input.(map[string]string); ok {
		key := MetricLabelItem{Label: labels["__name__"], Item: labels["item"]}
		key.TruncateItem(labels["service_name"] != "")

		return key
	}

	key := MetricLabelItem{}

	return key
}

// MetricLookupFromList return a map[MetricLabelItem]Metric.
func MetricLookupFromList(registeredMetrics []bleemeoTypes.Metric) map[MetricLabelItem]bleemeoTypes.Metric {
	registeredMetricsByKey := make(map[MetricLabelItem]bleemeoTypes.Metric, len(registeredMetrics))

	for _, v := range registeredMetrics {
		key := MetricLabelItem{Label: v.Label, Item: v.Labels["item"]}
		if existing, ok := registeredMetricsByKey[key]; !ok || !existing.DeactivatedAt.IsZero() {
			registeredMetricsByKey[key] = v
		}
	}

	return registeredMetricsByKey
}
