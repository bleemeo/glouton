// Copyright 2015-2026 Bleemeo
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

// Package syncservicesstate builds the services state Glouton reports on its Agent object,
// so that Bleemeo can tell a user why a service is or is not monitored.
package syncservicesstate

import (
	"github.com/bleemeo/glouton/bleemeo/internal/common"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"
)

// ServiceState is one service as reported to Bleemeo: what discovery knows about it, plus
// the Service object it is registered as.
type ServiceState struct {
	discovery.ServiceState

	// ServiceUUID is the ID of the Service object registered for this service, empty when
	// there is none: registration is pending, failed, or the service cannot be registered
	// at all (an instance too long for the API, for example). Such a service is reported
	// anyway, as it is precisely one Bleemeo cannot see otherwise.
	ServiceUUID string `json:"service_uuid"`
}

// WithServiceUUID adds to each service the ID of the Service object it is registered as.
//
// registered is the cache's ServiceLookupFromList, so a service is matched the way the
// services synchronization matches it -- by name and instance, the youngest object winning
// when there are duplicates -- and gets the UUID that synchronization updates.
//
// The order of states is kept: discovery sorts it, and the result is compared to decide
// whether the API needs updating.
func WithServiceUUID(
	states []discovery.ServiceState,
	registered map[common.ServiceNameInstance]bleemeoTypes.Service,
) []ServiceState {
	result := make([]ServiceState, 0, len(states))

	for _, state := range states {
		key := common.ServiceNameInstance{Name: state.Name, Instance: state.Instance}

		result = append(result, ServiceState{
			ServiceState: state,
			ServiceUUID:  registered[key].ID,
		})
	}

	return result
}
