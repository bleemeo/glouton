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

	"github.com/bleemeo/glouton/facts"
)

// TestIgnoreHighPortKeepsDeliberatePorts covers the filter that drops the random high port
// some services also bind -- HAProxy's syslog-over-UDP socket, the JMX/RMI ports of the
// Java ones -- without dropping the ports the service actually chose.
//
// The boundary is the ephemeral range the kernel assigns from, read from the kernel rather
// than fixed at 32000, so that a host whose range starts higher keeps the deliberate ports
// below it. Docker Desktop's VM answers "55000 65535", so this is not a hypothetical
// difference.
func TestIgnoreHighPortKeepsDeliberatePorts(t *testing.T) {
	firstEphemeralPort := facts.FirstEphemeralPort()

	cases := []struct {
		testName string
		port     int
		want     bool
	}{
		{"a well-known service port", 9092, true},
		{"the port just below the ephemeral range", firstEphemeralPort - 1, true},
		{"the first ephemeral port", firstEphemeralPort, false},
		{"a port above the ephemeral range start", firstEphemeralPort + 1000, false},
	}

	dd := &DynamicDiscovery{}                                               //nolint:exhaustruct
	di := discoveryInfo{IgnoreHighPort: true, ServiceProtocol: tcpProtocol} //nolint:exhaustruct

	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			service := &Service{ //nolint:exhaustruct
				ServiceType: KafkaService,
				IPAddress:   testIP127001,
				ListenAddresses: []facts.ListenAddress{
					{NetworkFamily: tcpProtocol, Address: testIP127001, Port: c.port},
				},
			}

			dd.updateListenAddresses(service, di)

			kept := false

			for _, a := range service.ListenAddresses {
				if a.Port == c.port {
					kept = true
				}
			}

			if kept != c.want {
				t.Errorf(
					"port %d kept = %v, want %v (ephemeral range starts at %d)",
					c.port, kept, c.want, firstEphemeralPort,
				)
			}
		})
	}
}

// TestIgnoreHighPortOffKeepsEverything pins that the filter only applies to the service
// types that asked for it: every other service keeps whatever it is listening on.
func TestIgnoreHighPortOffKeepsEverything(t *testing.T) {
	highPort := facts.FirstEphemeralPort() + 1000

	dd := &DynamicDiscovery{}                                                //nolint:exhaustruct
	di := discoveryInfo{IgnoreHighPort: false, ServiceProtocol: tcpProtocol} //nolint:exhaustruct

	service := &Service{ //nolint:exhaustruct
		ServiceType: KafkaService,
		IPAddress:   testIP127001,
		ListenAddresses: []facts.ListenAddress{
			{NetworkFamily: tcpProtocol, Address: testIP127001, Port: highPort},
		},
	}

	dd.updateListenAddresses(service, di)

	if len(service.ListenAddresses) != 1 || service.ListenAddresses[0].Port != highPort {
		t.Errorf("port %d was dropped without IgnoreHighPort: %v", highPort, service.ListenAddresses)
	}
}
