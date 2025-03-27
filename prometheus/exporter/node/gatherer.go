// Copyright 2015-2025 Bleemeo
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

package node

import (
	"strings"

	"github.com/bleemeo/glouton/facts/container-runtime/veth"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// AddNodeExporter add a node_exporter to collector.
func AddNodeExporter(gloutonRegistry *registry.Registry, option Option, vethProvider *veth.Provider) error {
	collector, err := NewCollector(option)
	if err != nil {
		return err
	}

	reg := prometheus.NewRegistry()

	err = reg.Register(collector)
	if err != nil {
		return err
	}

	_, err = gloutonRegistry.RegisterGatherer(
		registry.RegistrationOption{
			Description: "node_exporter",
			JitterSeed:  0,
		},
		nodeGatherer{
			gatherer:     reg,
			vethProvider: vethProvider,
		},
	)

	return err
}

// nodeGatherer adds containerID label to container interfaces.
type nodeGatherer struct {
	gatherer     prometheus.Gatherer
	vethProvider *veth.Provider
}

func (ng nodeGatherer) Gather() ([]*dto.MetricFamily, error) {
	mfs, err := ng.gatherer.Gather()
	if err != nil {
		return nil, err
	}

	labelMetaContainerID := types.LabelMetaContainerID

	for _, mf := range mfs {
		if !strings.HasPrefix(mf.GetName(), "node_network") {
			continue
		}

		for i, metric := range mf.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() != "device" {
					continue
				}

				containerID, err := ng.vethProvider.ContainerID(label.GetValue())
				if err != nil {
					logger.V(1).Printf("Failed to get container interfaces: %s", err)

					continue
				}

				containerLabel := &dto.LabelPair{
					Name:  &labelMetaContainerID,
					Value: &containerID,
				}

				mf.Metric[i].Label = append(mf.GetMetric()[i].GetLabel(), containerLabel)
			}
		}
	}

	return mfs, err
}
