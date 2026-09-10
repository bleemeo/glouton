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
// The executable is what tells them apart -- "influxd" for 1.x and 2.x, "influxdb3" for
// 3.x -- because their command lines are otherwise identical. Where netstat saw a port,
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
		exePath  string
		listen   []facts.ListenAddress
		config   config.Service
		wantPort int
	}{
		{
			testName: "3.x listening where it should",
			exePath:  "/usr/bin/influxdb3",
			listen:   listening(8181),
			wantPort: 8181,
		},
		{
			testName: "1.x or 2.x listening where it should",
			exePath:  "/usr/bin/influxd",
			listen:   listening(8086),
			wantPort: 8086,
		},
		{
			// The case that was broken: netstat saw 8086, the default said 8181, and
			// AddressForPort matched neither so the service had no address.
			testName: "the executable decides when both are listening",
			exePath:  "/usr/bin/influxd",
			listen:   listening(8086, 8181),
			wantPort: 8086,
		},
		{
			// No netstat information: the invented address is the one the executable
			// implies, which is all there is to go on.
			testName: "1.x with nothing listening",
			exePath:  "/opt/influxdb/influxd",
			listen:   listening(8086),
			wantPort: 8086,
		},
		{
			// A manually configured service, or a container whose process Glouton cannot
			// see. The executable says nothing, so where it listens has to.
			testName: "no executable, listening on the older port",
			exePath:  "",
			listen:   listening(8086),
			wantPort: 8086,
		},
		{
			testName: "no executable, listening on the 3.x port",
			exePath:  "",
			listen:   listening(8181),
			wantPort: 8181,
		},
		{
			// An explicit port in the configuration is the user's decision and beats both.
			testName: "the configuration wins over everything",
			exePath:  "/usr/bin/influxdb3",
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
				ExePath:         c.exePath,
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

// TestInfluxDBDefaultPortByExe pins the choice made before any listen address is known,
// which is what updateListenAddresses invents an address on when netstat found nothing.
func TestInfluxDBDefaultPortByExe(t *testing.T) {
	di := servicesDiscoveryInfo[InfluxDBService]

	cases := map[string]int{
		"/usr/bin/influxd":      8086,
		"/opt/influxdb/influxd": 8086,
		"/usr/bin/influxdb3":    8181,
		"":                      8181,
		"/usr/bin/something":    8181,
	}

	for exePath, want := range cases {
		service := Service{ExePath: exePath} //nolint:exhaustruct

		if got := service.defaultPort(di); got != want {
			t.Errorf("defaultPort(%q) = %d, want %d", exePath, got, want)
		}
	}
}
