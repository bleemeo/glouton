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

//go:build !windows
// +build !windows

package agent

import (
	"glouton/facts/container-runtime/veth"
	"glouton/logger"
	"glouton/prometheus/exporter/node"
)

func (a *agent) initOSSpecificParts() {
}

func (a *agent) registerOSSpecificComponents(vethProvider *veth.Provider) {
	if a.oldConfig.Bool("agent.node_exporter.enable") {
		nodeOption := node.Option{
			RootFS:            a.hostRootPath,
			EnabledCollectors: a.oldConfig.StringList("agent.node_exporter.collectors"),
		}

		nodeOption.WithPathIgnore(a.oldConfig.StringList("df.path_ignore"))
		nodeOption.WithNetworkIgnore(a.oldConfig.StringList("network_interface_blacklist"))
		nodeOption.WithDiskIgnore(a.oldConfig.StringList("disk_ignore"))
		nodeOption.WithPathIgnoreFSType(a.oldConfig.StringList("df.ignore_fs_type"))

		if err := a.gathererRegistry.AddNodeExporter(nodeOption, vethProvider); err != nil {
			logger.Printf("Unable to start node_exporter, system metrics will be missing: %v", err)
		}
	}
}
