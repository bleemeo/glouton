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

//go:build !windows
// +build !windows

package agent

import (
	"glouton/config"
	"glouton/facts/container-runtime/veth"
	"glouton/logger"
	"glouton/prometheus/exporter/node"
	"os"
)

func initOSSpecificParts(chan<- os.Signal) {
}

func (a *agent) registerOSSpecificComponents(vethProvider *veth.Provider) {
	if a.config.Agent.NodeExporter.Enable {
		filter, err := config.NewDFFSTypeMatcher(a.config)
		if err != nil {
			logger.Printf("Unable to start node_exporter, system metrics will be missing: %v", err)

			return
		}

		nodeOption := node.Option{
			RootFS:            a.hostRootPath,
			EnabledCollectors: a.config.Agent.NodeExporter.Collectors,
		}

		nodeOption.WithPathIgnore(a.config.DF.PathIgnore)
		nodeOption.WithNetworkIgnore(a.config.NetworkInterfaceDenylist)
		nodeOption.WithDiskIgnore(a.config.DiskIgnore)
		nodeOption.WithPathIgnoreFSType(filter)

		if err := a.gathererRegistry.AddNodeExporter(nodeOption, vethProvider); err != nil {
			logger.Printf("Unable to start node_exporter, system metrics will be missing: %v", err)
		}
	}
}
