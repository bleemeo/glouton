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
	"testing"
)

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
