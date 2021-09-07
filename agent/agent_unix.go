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
	"glouton/logger"
	"glouton/prometheus/exporter/node"
)

func (a *agent) initOSSpecificParts() {
}

func (a *agent) registerOSSpecificComponents() {
	if a.config.Bool("agent.node_exporter.enable") {
		nodeOption := node.Option{
			RootFS:            a.hostRootPath,
			EnabledCollectors: a.config.StringList("agent.node_exporter.collectors"),
		}

		nodeOption.WithPathIgnore(a.config.StringList("df.path_ignore"))
		nodeOption.WithNetworkIgnore(a.config.StringList("network_interface_blacklist"))
		nodeOption.WithDiskIgnore(a.config.StringList("disk_ignore"))
		nodeOption.WithPathIgnoreFSType(a.config.StringList("df.ignore_fs_type"))

		if err := a.gathererRegistry.AddNodeExporter(nodeOption); err != nil {
			logger.Printf("Unable to start node_exporter, system metrics will be missing: %v", err)
		}
	}
}
