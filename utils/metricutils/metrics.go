package metricutils

import "glouton/types"

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
