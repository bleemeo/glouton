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
	"sync"
	"testing"

	"github.com/bleemeo/glouton/facts"
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

// TestGetCheckNowDuringReconfigure is a regression test for a data race between NRPE and
// the discovery: NRPE looks up custom checks with GetCheckNow from its connection goroutines
// while the discovery adds and removes checks. Run it with -race.
func TestGetCheckNowDuringReconfigure(t *testing.T) {
	const reconfigureCount = 100

	key := NameInstance{Name: string(RedisService), Instance: ""}
	services := map[NameInstance]Service{
		key: { //nolint:exhaustruct
			Name:            string(RedisService),
			ServiceType:     RedisService,
			IPAddress:       "127.0.0.1",
			Active:          true,
			ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: "127.0.0.1", Port: 6379}},
		},
	}

	expectedAdded := make([]string, 0, reconfigureCount/2)
	for range reconfigureCount / 2 {
		expectedAdded = append(expectedAdded, "check for "+string(RedisService))
	}

	d := &Discovery{ //nolint:exhaustruct
		metricRegistry: &mockRegistry{ //nolint:exhaustruct
			ExpectedAddedContains: expectedAdded,
		},
		activeCheck: make(map[NameInstance]CheckDetails),
	}

	var (
		wg   sync.WaitGroup
		done = make(chan struct{})
	)

	wg.Go(func() {
		defer close(done)

		// The service appears and disappears, like a container being started and stopped.
		for i := range reconfigureCount {
			oldServices, newServices := map[NameInstance]Service{}, services
			if i%2 == 1 {
				oldServices, newServices = services, map[NameInstance]Service{}
			}

			// The discovery reconfigures the checks while holding its lock.
			d.l.Lock()
			d.configureChecks(oldServices, newServices)
			d.l.Unlock()
		}
	})

	wg.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
				_, _ = d.GetCheckNow(key)
			}
		}
	})

	wg.Wait()
}
