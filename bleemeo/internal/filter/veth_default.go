//go:build windows || darwin

package filter

import (
	"glouton/types"
	"strings"
)

// isVethNetworkMetric returns whether this metric is a network metric on a virtual interface.
func (f *Filter) isVethNetworkMetric(labels map[string]string) bool {
	// Avoid unused linter warning.
	_ = f.isVethByInterface

	// Network metrics can come from the "net" telegraf input or from node exporter.
	if !(strings.HasPrefix(labels[types.LabelName], "net") || strings.HasPrefix(labels[types.LabelName], "node_network")) {
		return false
	}

	return strings.HasPrefix(labels[types.LabelItem], "veth")
}
