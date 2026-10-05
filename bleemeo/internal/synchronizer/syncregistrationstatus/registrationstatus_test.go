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

package syncregistrationstatus

import (
	"encoding/json"
	"testing"

	"github.com/bleemeo/glouton/bleemeo/internal/common"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"

	"github.com/google/go-cmp/cmp"
)

// TestFromDiscovery checks each service gets the UUID of its own Service object, matched
// on name and instance, and an unregistered one is kept with an empty UUID; and its metrics,
// as a word.
func TestFromDiscovery(t *testing.T) {
	states := []discovery.ServiceState{
		{Name: "apache", ServiceType: discovery.ApacheService},
		{Name: "redis", ServiceType: discovery.RedisService, Instance: "cache"},
		{Name: "redis", ServiceType: discovery.RedisService, Instance: "session"},
		{Name: "mysql", ServiceType: discovery.MySQLService},
	}

	registered := common.ServiceLookupFromList([]bleemeoTypes.Service{
		{ID: "uuid-apache", Label: "apache"},
		{ID: "uuid-redis-cache", Label: "redis", Instance: "cache"},
		{ID: "uuid-redis-session", Label: "redis", Instance: "session"},
	})

	metricNames := map[discovery.NameInstance]int{
		{Name: "apache"}:                   1,
		{Name: "redis", Instance: "cache"}: 12,
	}

	got := FromDiscovery(states, registered, metricNames)

	want := []ServiceState{
		{ServiceState: states[0], ServiceUUID: "uuid-apache", Metrics: MetricsOne},
		{ServiceState: states[1], ServiceUUID: "uuid-redis-cache", Metrics: MetricsSome},
		{ServiceState: states[2], ServiceUUID: "uuid-redis-session", Metrics: MetricsZero},
		// Not registered: still reported, so Bleemeo learns about it.
		{ServiceState: states[3], ServiceUUID: "", Metrics: MetricsZero},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("FromDiscovery() mismatch (-want +got):\n%s", diff)
	}
}

// TestFromDiscoveryDuplicates checks that when the API holds two Service objects for one
// service, the UUID reported is the one the services synchronization keeps: the youngest.
func TestFromDiscoveryDuplicates(t *testing.T) {
	states := []discovery.ServiceState{{Name: "apache", ServiceType: discovery.ApacheService}}

	registered := common.ServiceLookupFromList([]bleemeoTypes.Service{
		{ID: "uuid-new", Label: "apache", CreationDate: "2026-09-02T10:00:00Z"},
		{ID: "uuid-old", Label: "apache", CreationDate: "2026-09-01T10:00:00Z"},
	})

	got := FromDiscovery(states, registered, nil)
	if got[0].ServiceUUID != "uuid-new" {
		t.Errorf("got UUID %q, want the youngest Service object %q", got[0].ServiceUUID, "uuid-new")
	}
}

// TestServiceStateJSON checks the payload is flat, as the API expects one object per
// service, and that service_uuid and error are always present, even when empty.
func TestServiceStateJSON(t *testing.T) {
	state := ServiceState{
		Name:           "mysql",
		ServiceType:    discovery.MySQLService,
		Dynamic:        true,
		InputSupported: true,
		Metrics:        MetricsZero,
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	want := map[string]any{
		"name":              "mysql",
		"type":              "mysql",
		"instance":          "",
		"container_name":    "",
		"dynamic_discovery": true,
		"input_supported":   true,
		"error":             "",
		"metrics":           "zero",
		"service_uuid":      "",
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("JSON mismatch (-want +got):\n%s", diff)
	}
}
