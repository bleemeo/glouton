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

package common

import (
	"fmt"
	"time"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
)

// Maximal length of fields on Bleemeo API.
const (
	APIMetricItemLength      int = 250
	APIServiceInstanceLength int = 250
	APIContainerNameLength   int = 250
	APIConfigItemKeyLength   int = 100
	APIConfigItemPathLength  int = 250
)

type ServiceNameInstance struct {
	Name     string
	Instance string
}

func (sni ServiceNameInstance) String() string {
	if sni.Instance != "" {
		return fmt.Sprintf("%s on %s", sni.Name, sni.Instance)
	}

	return sni.Name
}

// MetricLookupFromList return a map[MetricLabelItem]Metric.
func MetricLookupFromList(registeredMetrics []bleemeoTypes.Metric) map[string]bleemeoTypes.Metric {
	registeredMetricsByKey := make(map[string]bleemeoTypes.Metric, len(registeredMetrics))

	for _, v := range registeredMetrics {
		key := v.LabelsText
		if existing, ok := registeredMetricsByKey[key]; !ok || !existing.DeactivatedAt.IsZero() {
			registeredMetricsByKey[key] = v
		}
	}

	return registeredMetricsByKey
}

// ServiceLookupFromList returns a map[ServiceNameInstance]bleemeoTypes.Service
// from the given list, while excluding any duplicated service.
// It prioritizes the exclusion of oldest services over the youngest ones.
func ServiceLookupFromList(registeredServices []bleemeoTypes.Service) map[ServiceNameInstance]bleemeoTypes.Service {
	registeredServicesByKey := make(map[ServiceNameInstance]bleemeoTypes.Service, len(registeredServices))

	for _, srv := range registeredServices {
		key := ServiceNameInstance{Name: srv.Label, Instance: srv.Instance}
		if existing, ok := registeredServicesByKey[key]; !ok {
			registeredServicesByKey[key] = srv
		} else { // Compare creation dates and keep the youngest service
			existingCreationDate, err := time.Parse(time.RFC3339, existing.CreationDate)
			if err != nil {
				logger.V(1).Printf("Failed to parse creation date %q of service %s: %v", existing.CreationDate, existing.ID, err)

				continue
			}

			srvCreationDate, err := time.Parse(time.RFC3339, srv.CreationDate)
			if err != nil {
				logger.V(1).Printf("Failed to parse creation date %q of service %s: %v", srv.CreationDate, srv.ID, err)

				continue
			}

			if srvCreationDate.After(existingCreationDate) {
				registeredServicesByKey[key] = srv
			}
		}
	}

	return registeredServicesByKey
}

// IgnoreContainer returns true if the metric's container information should be ignored.
// If this is true, docker integration is also disabled.
func IgnoreContainer(cfg bleemeoTypes.GloutonAccountConfig, labels map[string]string) bool {
	if cfg.DockerIntegration {
		return false
	}

	return labels[types.LabelName] == types.MetricServiceStatus
}
