package common

import "strings"

// AllowMetric return True if current configuration allow this metrics
func AllowMetric(labels map[string]string, whitelist map[string]bool) bool {
	if len(whitelist) == 0 {
		return true
	}
	if labels["service_name"] != "" && strings.HasSuffix(labels["__name__"], "_status") {
		return true
	}
	return whitelist[labels["__name__"]]
}
