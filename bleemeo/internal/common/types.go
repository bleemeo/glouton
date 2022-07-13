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
	"strings"
)

// Maximal length of fields on Bleemeo API.
const (
	APIMetricItemLength          int = 100
	APIMetricItemLengthIfService int = 50
)

// MetricOnlyHasItem return true if the metric only has a name and an item (which could be empty).
// Said otherwise, the metrics don't need to use labels_text on Bleemeo API to store its labels.
// instance_uuid is ignore in this check if it match agentID.
func MetricOnlyHasItem(labels map[string]string, agentID string) bool {
	if len(labels) > 3 {
		return false
	}

	for k, v := range labels {
		if k != types.LabelName && k != types.LabelItem && k != types.LabelInstanceUUID {
			return false
		}

		if k == types.LabelInstanceUUID && v != agentID {
			return false
		}
	}

	return true
}

// LabelsToText convert labels & annotation to a string version.
// When using the Bleemeo Mode, result is the name + the item annotation.
func LabelsToText(labels map[string]string, annotations types.MetricAnnotations, bleemeoMode bool) string {
	if bleemeoMode && labels[types.LabelItem] != TruncateItem(labels[types.LabelItem], annotations.ServiceName != "") {
		labelsCopy := make(map[string]string, len(labels)+1)
		for k, v := range labels {
			labelsCopy[k] = v
		}

		labelsCopy[types.LabelItem] = TruncateItem(labels[types.LabelItem], annotations.ServiceName != "")

		return types.LabelsToText(labelsCopy)
	}

	return types.LabelsToText(labels)
}

// TruncateItem truncate the item to match maximal length allowed by Bleemeo API.
func TruncateItem(item string, isService bool) string {
	if len(item) > APIMetricItemLength {
		item = item[:APIMetricItemLength]
	}

	if isService && len(item) > APIMetricItemLengthIfService {
		item = item[:APIMetricItemLengthIfService]
	}

	return item
}

// MetricLookupFromList return a map[MetricLabelItem]Metric.
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

// IsServiceCheckMetric returns whether this metric is a service check and should always be allowed.
func IsServiceCheckMetric(labels map[string]string, annotations types.MetricAnnotations) bool {
	return annotations.ServiceName != "" && strings.HasSuffix(labels[types.LabelName], "_status")
}
