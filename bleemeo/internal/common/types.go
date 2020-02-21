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
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/types"
)

// Maximal length of fields on Bleemeo API
const (
	APIMetricItemLength          int = 100
	APIMetricItemLengthIfService int = 50
)

// LabelsToText convert labels to a string version.
// When using the Bleemeo Mode, the only labels used are name and __bleemeo_item
func LabelsToText(labels map[string]string, bleemeoMode bool) string {
	if bleemeoMode {
		labelsCopy := map[string]string{
			types.LabelName:        labels[types.LabelName],
			types.LabelBleemeoItem: TruncateItem(labels[types.LabelBleemeoItem], labels[types.LabelServiceName] != ""),
		}
		return types.LabelsToText(labelsCopy)
	}

	return types.LabelsToText(labels)
}

// TruncateItem truncate the item to match maximal length allowed by Bleemeo API
func TruncateItem(item string, isService bool) string {
	if len(item) > APIMetricItemLength {
		item = item[:APIMetricItemLength]
	}

	if isService && len(item) > APIMetricItemLengthIfService {
		item = item[:APIMetricItemLengthIfService]
	}

	return item
}

// MetricLookupFromList return a map[MetricLabelItem]Metric
func MetricLookupFromList(registeredMetrics []bleemeoTypes.Metric) map[string]bleemeoTypes.Metric {
	registeredMetricsByKey := make(map[string]bleemeoTypes.Metric, len(registeredMetrics))
	for _, v := range registeredMetrics {
		key := v.LabelsText
		if existing, ok := registeredMetricsByKey[key]; !ok || !existing.DeactivatedAt.IsZero() {
			registeredMetricsByKey[key] = v
		}
	}
	return registeredMetricsByKey
}
