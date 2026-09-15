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
	"net"
	"path/filepath"
	"testing"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
)

// TestUseChronyCommandCheck checks which protocol an NTP service's status is read with.
//
// An NTP server is checked with the NTP protocol, whichever daemon serves it. But a
// chrony that only syncs the local clock -- the default install on most distributions,
// and what a chrony container usually runs -- never answers an NTP query, so that check
// would report a permanent "Connection timed out" on a perfectly healthy daemon: its
// command protocol is the only thing it does answer.
func TestUseChronyCommandCheck(t *testing.T) {
	di := servicesDiscoveryInfo[NTPService]

	// No chronyd socket anywhere near this test, so the executable path decides.
	noSocket := filepath.Join(t.TempDir(), "nonexistent.sock")

	ntpPort := facts.ListenAddress{NetworkFamily: di.ServiceProtocol, Address: "192.168.1.5", Port: di.ServicePort}

	cases := []struct {
		name    string
		service Service
		want    bool
	}{
		{
			// The client-only case: chronyd was recognized by its executable, and
			// discovery never saw it listening on the NTP port -- the listen address it
			// has is the synthetic one updateListenAddresses adds from the service type
			// when there is no netstat information, on the NTP port itself.
			name: "chronyd not serving NTP",
			service: Service{
				ServiceType:     NTPService,
				ServiceVariant:  VariantChrony,
				ListenAddresses: []facts.ListenAddress{ntpPort},
				HasNetstatInfo:  false,
			},
			want: true,
		},
		{
			// Same daemon, but really seen listening on the NTP port: that is the service
			// being monitored, and the NTP protocol is the check for it. The command port
			// is only how its metrics are read.
			name: "chronyd serving NTP",
			service: Service{
				ServiceType:     NTPService,
				ServiceVariant:  VariantChrony,
				ListenAddresses: []facts.ListenAddress{ntpPort},
				HasNetstatInfo:  true,
			},
			want: false,
		},
		{
			// The same daemon on an IPv6-only host, or with "bindaddress ::": netstat names
			// the family in the network ("udp6"), and it is still a chronyd serving NTP.
			name: "chronyd serving NTP over IPv6 only",
			service: Service{
				ServiceType:    NTPService,
				ServiceVariant: VariantChrony,
				ListenAddresses: []facts.ListenAddress{
					{NetworkFamily: di.ServiceProtocol + "6", Address: "::", Port: di.ServicePort},
				},
				HasNetstatInfo: true,
			},
			want: false,
		},
		{
			// Netstat found this daemon's ports, and the NTP one isn't among them.
			name: "chronyd with netstat information but no NTP port",
			service: Service{
				ServiceType:     NTPService,
				ServiceVariant:  VariantChrony,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: udpProtocol, Address: "127.0.0.1", Port: chronyDefaultCmdPort}},
				HasNetstatInfo:  true,
			},
			want: true,
		},
		{
			// ntpd doesn't speak chrony's command protocol at all, whether it serves NTP
			// or not: it is always checked with the NTP protocol.
			name: "ntpd",
			service: Service{
				ServiceType:     NTPService,
				ServiceVariant:  VariantNTPd,
				ListenAddresses: []facts.ListenAddress{ntpPort},
				HasNetstatInfo:  false,
			},
			want: false,
		},
		{
			// A port override moves which port makes the daemon an NTP server.
			name: "chronyd serving NTP on a declared port",
			service: Service{
				ServiceType:     NTPService,
				ServiceVariant:  VariantChrony,
				Config:          config.Service{Port: 1123},
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: udpProtocol, Address: "192.168.1.5", Port: 1123}},
				HasNetstatInfo:  true,
			},
			want: false,
		},
		{
			// The NTP port over TCP is not an NTP server: the protocol is UDP.
			name: "chronyd with a TCP port 123",
			service: Service{
				ServiceType:     NTPService,
				ServiceVariant:  VariantChrony,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: "192.168.1.5", Port: di.ServicePort}},
				HasNetstatInfo:  true,
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := useChronyCommandCheck(tc.service, di, noSocket); got != tc.want {
				t.Errorf("useChronyCommandCheck() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNTPServiceDiscoveryInfo checks the assumptions the NTP check dispatch is built on:
// the NTP protocol's own port and network, which both servesNTPProtocol and the synthetic
// listen address discovery falls back to are read from.
func TestNTPServiceDiscoveryInfo(t *testing.T) {
	di := servicesDiscoveryInfo[NTPService]

	if di.ServicePort != 123 {
		t.Errorf("NTPService port = %d, want 123", di.ServicePort)
	}

	if di.ServiceProtocol != udpProtocol {
		t.Errorf("NTPService protocol = %q, want %q", di.ServiceProtocol, udpProtocol)
	}

	// chronyd's command port must stay distinct from it: they are two different
	// protocols, and confusing them is what made the check time out on a healthy daemon.
	if chronyDefaultCmdPort == di.ServicePort {
		t.Errorf("chrony's command port = %d, want it different from the NTP port", chronyDefaultCmdPort)
	}

	// One address for the service, used by both the input and the check, so they cannot
	// disagree about which daemon they are talking to.
	addr, ok := chronyCmdAddress(Service{ServiceType: NTPService}) //nolint:exhaustruct
	if !ok {
		t.Fatal("chronyCmdAddress() could not name the local daemon")
	}

	if _, _, err := net.SplitHostPort(addr); err != nil {
		t.Errorf("chronyCmdAddress() isn't a host:port: %v", err)
	}
}
