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

// TestServesNTPProtocol covers what decides a chronyd is checked with the NTP protocol
// rather than with chrony's command protocol.
//
// Getting it wrong in either direction is visible to a user: a daemon that serves NTP and is
// probed on its command port reports down whenever that port refuses us -- which a remote
// chronyd does by default, cmdallow being localhost-only -- and a daemon that only syncs the
// local clock, probed with NTP, times out forever on a service that is perfectly healthy.
func TestServesNTPProtocol(t *testing.T) {
	const ntpPort, cmdPort = 123, 323

	di := servicesDiscoveryInfo[ChronyService]

	listen := func(network string, port int) facts.ListenAddress {
		return facts.ListenAddress{NetworkFamily: network, Address: "10.0.0.5", Port: port}
	}

	cases := []struct {
		name      string
		netstat   bool
		addresses []facts.ListenAddress
		cfg       config.Service
		want      bool
	}{
		{
			name:      "listening on the NTP port",
			netstat:   true,
			addresses: []facts.ListenAddress{listen(udpProtocol, ntpPort)},
			want:      true,
		},
		{
			// netstat records the IP family in the network name, so an IPv6-only daemon
			// is "udp6" and must not read as one that doesn't serve NTP.
			name:      "IPv6-only daemon",
			netstat:   true,
			addresses: []facts.ListenAddress{listen("udp6", ntpPort)},
			want:      true,
		},
		{
			name:      "only the command port, so it serves no NTP",
			netstat:   true,
			addresses: []facts.ListenAddress{listen(udpProtocol, cmdPort)},
			want:      false,
		},
		{
			// Without netstat the listen address is synthesised from the type's default
			// port, which for chrony is the NTP port -- so it cannot tell a daemon that
			// serves NTP from one merely recognised as a time daemon.
			name:      "no netstat information",
			netstat:   false,
			addresses: []facts.ListenAddress{listen(udpProtocol, ntpPort)},
			want:      false,
		},
		{
			// The regression: an override rebuilds the listen addresses as one entry
			// typed tcp whatever the service speaks, so matching udp on it reported every
			// overridden chrony as serving no NTP.
			name:      "override sets only the address",
			netstat:   true,
			addresses: []facts.ListenAddress{listen(tcpProtocol, ntpPort)},
			cfg:       config.Service{Address: "10.0.0.5"}, //nolint:exhaustruct
			want:      true,
		},
		{
			name:      "override names the NTP port",
			netstat:   true,
			addresses: []facts.ListenAddress{listen(tcpProtocol, ntpPort)},
			cfg:       config.Service{Port: ntpPort}, //nolint:exhaustruct
			want:      true,
		},
		{
			// Pointing the override at the command port says to reach the daemon there,
			// and the command probe is then the right check -- the override must not turn
			// that into an NTP check against a port that answers none.
			name:      "override names the command port",
			netstat:   true,
			addresses: []facts.ListenAddress{listen(tcpProtocol, cmdPort)},
			cfg:       config.Service{Port: cmdPort}, //nolint:exhaustruct
			want:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := Service{ //nolint:exhaustruct
				Name:            string(ChronyService),
				ServiceType:     ChronyService,
				HasNetstatInfo:  tc.netstat,
				ListenAddresses: tc.addresses,
				Config:          tc.cfg,
			}

			if got := servesNTPProtocol(service, di); got != tc.want {
				t.Errorf("servesNTPProtocol() = %v, want %v", got, tc.want)
			}
		})
	}
}
