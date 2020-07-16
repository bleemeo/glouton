// +build !windows

package agent

import (
	"glouton/logger"
	"glouton/prometheus/exporter/node"
)

func (a *agent) initOSSpecificParts() {
}

func (a *agent) registerOSSpecificComponents() {
	if a.config.Bool("agent.node_exporter.enabled") {
		nodeOption := node.Option{
			RootFS:            a.hostRootPath,
			EnabledCollectors: a.config.StringList("agent.node_exporter.collectors"),
		}

		nodeOption.WithPathIgnore(a.config.StringList("df.path_ignore"))
		nodeOption.WithNetworkIgnore(a.config.StringList("network_interface_blacklist"))

		if err := a.gathererRegistry.AddNodeExporter(nodeOption); err != nil {
			logger.Printf("Unable to start node_exporter, system metric will be missing: %v", err)
		}
	}
}
