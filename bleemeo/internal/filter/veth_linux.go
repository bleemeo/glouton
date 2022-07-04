package filter

import (
	"glouton/logger"
	"glouton/types"
	"strings"

	"github.com/vishvananda/netlink"
)

// isVethNetworkMetric returns whether this metric is a network metric on a virtual interface.
func (f *Filter) isVethNetworkMetric(labels map[string]string) bool {
	// Network metrics can come from the "net" telegraf input or from node exporter.
	if !(strings.HasPrefix(labels[types.LabelName], "net") || strings.HasPrefix(labels[types.LabelName], "node_network")) {
		return false
	}

	isVeth, ok := f.isVethByInterface[labels[types.LabelItem]]
	if ok {
		return isVeth
	}

	// Update cache.
	links, err := netlink.LinkList()
	if err != nil {
		logger.V(2).Printf("Failed to get link by name: %s", err)

		// If we failed to query netlink, fallback on a simpler veth detection.
		return strings.HasPrefix(labels[types.LabelItem], "veth")
	}

	isVethByInterface := make(map[string]bool, len(links))
	for _, link := range links {
		isVethByInterface[link.Attrs().Name] = link.Attrs().NetNsID >= 0
	}

	f.isVethByInterface = isVethByInterface

	return f.isVethByInterface[labels[types.LabelItem]]
}
