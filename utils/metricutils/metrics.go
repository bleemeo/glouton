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

package metricutils

import "github.com/bleemeo/glouton/types"

// MetricOnlyHasItem returns true if the metric only has a name and an item (which could be empty).
// Said otherwise, the metric doesn't need to use labels_text on Bleemeo API to store its labels.
// instance_uuid and instance are ignored in this check if instance_uuid matches agentID.
func MetricOnlyHasItem(labels map[string]string, agentID string) bool {
	if len(labels) > 4 {
		return false
	}

	for k, v := range labels {
		if k != types.LabelName && k != types.LabelItem && k != types.LabelInstanceUUID && k != types.LabelInstance {
			return false
		}

		if k == types.LabelInstanceUUID && v != agentID {
			return false
		}
	}

	return true
}

// MetricKey return a unique key that could be used in for lookup in cache.MetricLookupFromList
//
// This is working correctly because metricFromAPI generate the correct format before adding them to the cache.
func MetricKey(lbls map[string]string, annotations types.MetricAnnotations, agentID string) string {
	if lbls[types.LabelInstanceUUID] == "" || lbls[types.LabelInstanceUUID] == annotations.BleemeoAgentID || (annotations.BleemeoAgentID == "" && lbls[types.LabelInstanceUUID] == agentID) {
		// In name+item mode, we treat empty instance_uuid and instance_uuid=agentID as the same.
		// This reflect in:
		// * metricFromAPI which fill the instance_uuid when labels_text is empty
		// * MetricOnlyHasItem that cause instance_uuid to not be sent on registration in name+item mode
		//
		// Also in this mode, we ignore instance when instance_uuid=agentID. This reflect in:
		// * MetricOnlyHasItem that cause instance to not be sent on registration in name+item mode
		// * instance being dropped here in metricKey
		agentID := agentID

		if annotations.BleemeoAgentID != "" {
			agentID = annotations.BleemeoAgentID
		}

		if MetricOnlyHasItem(lbls, agentID) {
			tmp := make(map[string]string, len(lbls)+1)

			for k, v := range lbls {
				if k == types.LabelInstance {
					continue
				}

				tmp[k] = v
			}

			if agentID != "" {
				tmp[types.LabelInstanceUUID] = agentID
			}

			lbls = tmp
		}
	}

	return types.LabelsToText(lbls)
}
