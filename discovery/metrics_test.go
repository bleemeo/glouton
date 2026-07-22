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

package discovery

import (
	"testing"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
)

func TestClickHouseAddress(t *testing.T) {
	cases := []struct {
		name     string
		service  Service
		wantIP   string
		wantPort int
	}{
		{
			name: "port 8123 explicitly published",
			service: Service{
				ServiceType:     ClickHouseService,
				IPAddress:       "172.20.0.16",
				ListenAddresses: []facts.ListenAddress{{Address: "172.20.0.16", Port: 8123, NetworkFamily: "tcp"}},
			},
			wantIP:   "172.20.0.16",
			wantPort: 8123,
		},
		{
			name: "only port 9000 published, no user override -> fallback to 8123",
			service: Service{
				ServiceType:     ClickHouseService,
				IPAddress:       "172.20.0.16",
				ListenAddresses: []facts.ListenAddress{{Address: "172.20.0.16", Port: 9000, NetworkFamily: "tcp"}},
			},
			wantIP:   "172.20.0.16",
			wantPort: 8123,
		},
		{
			name: "user-set Port not actually listening -> still attempted via IPAddress",
			service: Service{
				ServiceType:     ClickHouseService,
				IPAddress:       "172.20.0.16",
				Config:          config.Service{Port: 8124},
				ListenAddresses: []facts.ListenAddress{{Address: "172.20.0.16", Port: 9000, NetworkFamily: "tcp"}},
			},
			wantIP:   "172.20.0.16",
			wantPort: 8124,
		},
		{
			name: "StatsPort takes priority over Port",
			service: Service{
				ServiceType: ClickHouseService,
				IPAddress:   "172.20.0.16",
				Config:      config.Service{Port: 9000, StatsPort: 8123},
			},
			wantIP:   "172.20.0.16",
			wantPort: 8123,
		},
		{
			name: "port not found and IPAddress unknown -> no fallback, no crash",
			service: Service{
				ServiceType:     ClickHouseService,
				ListenAddresses: []facts.ListenAddress{{Address: "172.20.0.16", Port: 9000, NetworkFamily: "tcp"}},
			},
			wantIP:   "",
			wantPort: 8123,
		},
		{
			name: "explicit Config.Address bypasses everything (existing behavior, unaffected)",
			service: Service{
				ServiceType: ClickHouseService,
				Config:      config.Service{Address: "10.0.0.5"},
			},
			wantIP:   "10.0.0.5",
			wantPort: 8123,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotIP, gotPort := clickHouseAddress(tc.service)
			if gotIP != tc.wantIP || gotPort != tc.wantPort {
				t.Errorf("clickHouseAddress() = (%q, %d), want (%q, %d)", gotIP, gotPort, tc.wantIP, tc.wantPort)
			}
		})
	}
}
