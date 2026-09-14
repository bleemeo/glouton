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

// TestInfluxDBPort covers the port the check probes and the input reads, which is not the
// same for every InfluxDB: 1.x and 2.x serve 8086, 3.x serves 8181. Getting it from one
// fixed default reported a healthy 1.x as down and gave its input no address at all.
//
// The variant is what tells them apart -- VariantInfluxd for 1.x and 2.x, VariantInfluxDB3
// for 3.x -- since their command lines are otherwise identical. Where netstat saw a port,
// that wins over any guess.
func TestInfluxDBPort(t *testing.T) {
	listening := func(ports ...int) []facts.ListenAddress {
		addresses := make([]facts.ListenAddress, 0, len(ports))
		for _, port := range ports {
			addresses = append(addresses, facts.ListenAddress{
				NetworkFamily: tcpProtocol, Address: "172.20.0.5", Port: port,
			})
		}

		return addresses
	}

	cases := []struct {
		testName string
		variant  ServiceVariant
		listen   []facts.ListenAddress
		config   config.Service
		wantPort int
	}{
		{
			testName: "3.x listening where it should",
			variant:  VariantInfluxDB3,
			listen:   listening(8181),
			wantPort: 8181,
		},
		{
			testName: "1.x or 2.x listening where it should",
			variant:  VariantInfluxd,
			listen:   listening(8086),
			wantPort: 8086,
		},
		{
			// The case that was broken: netstat saw 8086, the default said 8181, and
			// AddressForPort matched neither so the service had no address.
			testName: "the variant decides when both are listening",
			variant:  VariantInfluxd,
			listen:   listening(8086, 8181),
			wantPort: 8086,
		},
		{
			// No netstat information: the invented address is the one the variant
			// implies, which is all there is to go on.
			testName: "1.x with nothing listening",
			variant:  VariantInfluxd,
			listen:   listening(8086),
			wantPort: 8086,
		},
		{
			// A service the user declared without naming a variant. Nothing says which
			// line it is, so where it listens has to.
			testName: "no variant, listening on the older port",
			variant:  VariantUnknown,
			listen:   listening(8086),
			wantPort: 8086,
		},
		{
			testName: "no variant, listening on the 3.x port",
			variant:  VariantUnknown,
			listen:   listening(8181),
			wantPort: 8181,
		},
		{
			// An explicit port in the configuration is the user's decision and beats both.
			testName: "the configuration wins over everything",
			variant:  VariantInfluxDB3,
			listen:   listening(8086, 8181, 9999),
			config:   config.Service{Port: 9999}, //nolint:exhaustruct
			wantPort: 9999,
		},
	}

	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			service := Service{ //nolint:exhaustruct
				Name:            string(InfluxDBService),
				ServiceType:     InfluxDBService,
				ServiceVariant:  c.variant,
				ListenAddresses: c.listen,
				IPAddress:       "172.20.0.5",
				Config:          c.config,
			}

			address, port := service.AddressPort()

			if port != c.wantPort {
				t.Errorf("AddressPort() port = %d, want %d", port, c.wantPort)
			}

			if address == "" {
				t.Error("AddressPort() address is empty, so neither the check nor the input has anywhere to go")
			}
		})
	}
}

// TestInfluxDBDefaultPortByVariant pins the choice made before any listen address is known,
// which is what updateListenAddresses invents an address on when netstat found nothing.
//
// The empty variant is a service the user declared without naming one; it falls back to the
// service type's plain default, and an unrecognised value does the same rather than
// inventing a port of its own.
func TestInfluxDBDefaultPortByVariant(t *testing.T) {
	di := servicesDiscoveryInfo[InfluxDBService]

	cases := map[ServiceVariant]int{
		VariantInfluxd:   8086,
		VariantInfluxDB3: 8181,
		VariantUnknown:   8181,
		"something-else": 8181,
	}

	for variant, want := range cases {
		service := Service{ServiceVariant: variant} //nolint:exhaustruct

		if got := service.defaultPort(di); got != want {
			t.Errorf("defaultPort(%q) = %d, want %d", variant, got, want)
		}
	}
}

// TestInfluxDBPortSurvivesUnknownExePath is the regression this mechanism also fixes:
// /proc/<pid>/exe briefly fails to resolve right after a container restart, and the port
// used to be read from it. A 1.x server would then be probed on 3.x's 8181 for that window
// -- and, since updateListenAddresses invents a matching listen address when netstat has
// nothing, the wrong port looked confirmed rather than guessed.
func TestInfluxDBPortSurvivesUnknownExePath(t *testing.T) {
	di := servicesDiscoveryInfo[InfluxDBService]

	service := Service{ //nolint:exhaustruct
		Name:           string(InfluxDBService),
		ServiceType:    InfluxDBService,
		ServiceVariant: VariantInfluxd,
		ExePath:        "",
	}

	if got := service.defaultPort(di); got != 8086 {
		t.Errorf("defaultPort() with no ExePath = %d, want 8086", got)
	}
}
