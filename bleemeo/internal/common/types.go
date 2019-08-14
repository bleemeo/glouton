package common

import (
	"agentgo/bleemeo/types"
	agentTypes "agentgo/types"
	"fmt"
)

// Maximal length of fields on Bleemeo API
const (
	APIMetricItemLength          int = 100
	APIMetricItemLengthIfService int = 50
)

// MetricLabelItem is the couple (Label, Item) which uniquely identify a metric
type MetricLabelItem struct {
	Label string
	Item  string
}

// TruncateItem truncate the item to match maximal length allowed by Bleemeo API
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

// MetricLabelItemFromMetric create a MetricLabelItem from a local or remote metric (or labels of local one)
func MetricLabelItemFromMetric(input interface{}) MetricLabelItem {
	if metric, ok := input.(types.Metric); ok {
		key := MetricLabelItem{Label: metric.Label, Item: metric.Labels["item"]}
		key.TruncateItem(metric.ServiceID != "")
		return key
	}
	if metric, ok := input.(agentTypes.Metric); ok {
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
