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
