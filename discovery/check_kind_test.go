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
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/types"
)

// checkStatusAgainst builds the check discovery would build for a service of this type,
// points it at addr, and returns what it reports.
func checkStatusAgainst(t *testing.T, serviceType ServiceName, addr string) types.StatusDescription {
	t.Helper()

	return checkStatusAgainstWithConfig(t, serviceType, addr, config.Service{}) //nolint:exhaustruct
}

// checkStatusAgainstWithConfig is checkStatusAgainst with a service configuration applied,
// for the settings a user can put on a service.
func checkStatusAgainstWithConfig(
	t *testing.T,
	serviceType ServiceName,
	addr string,
	cfg config.Service,
) types.StatusDescription {
	t.Helper()

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q) = %v", addr, err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("Atoi(%q) = %v", portStr, err)
	}

	cfg.Port = port

	d := &Discovery{ //nolint:exhaustruct
		metricRegistry: &mockRegistry{ //nolint:exhaustruct
			ExpectedAddedContains: []string{"check for " + string(serviceType)},
		},
		activeCheck: make(map[NameInstance]CheckDetails),
	}

	service := Service{ //nolint:exhaustruct
		Name:        string(serviceType),
		ServiceType: serviceType,
		IPAddress:   host,
		Active:      true,
		// An explicit port, so the check goes to the test server rather than to the
		// service type's own default.
		Config:          cfg,
		ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: host, Port: port}},
	}

	d.createCheck(service)

	details, ok := d.activeCheck[NameInstance{Name: string(serviceType), Instance: ""}]
	if !ok {
		t.Fatalf("no check was created for %s", serviceType)
	}

	status, err := details.check.CheckNow(t.Context())
	if err != nil {
		t.Fatalf("CheckNow() = %v", err)
	}

	return status
}

// TestVarnishCheckSeesAnUnreachableBackend is why Varnish is checked over HTTP rather than
// TCP, and the failure it prevents is a silent one.
//
// A Varnish whose backend is unreachable answers 503 to every visitor while still accepting
// connections on its port, so a TCP check calls a total outage healthy. Measured against the
// running container: backend up gives 200, backend stopped gives 503, and Varnish stopped
// refuses the connection -- three states that TCP flattens into two.
//
// The same server answers both checks here, so the only thing that differs is the kind of
// check the service type is given.
func TestVarnishCheckSeesAnUnreachableBackend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// What Varnish answers when it cannot reach its backend.
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	addr := srv.Listener.Addr().String()

	if got := checkStatusAgainst(t, VarnishService, addr).CurrentStatus; got != types.StatusCritical {
		t.Errorf("Varnish serving 503 reported %v, want critical: a cache that serves nothing is not healthy", got)
	}

	// The contrast, and the whole point: a plain TCP check of the very same server sees
	// only that something accepted the connection, and calls it healthy. Nats is used
	// because its check is a bare connect -- Redis and the others send a protocol probe
	// and would fail here for a reason that has nothing to do with the status code.
	if got := checkStatusAgainst(t, NatsService, addr).CurrentStatus; got != types.StatusOk {
		t.Errorf("TCP-checked service on a listening port reported %v, want ok", got)
	}
}

// TestInfluxDBCheckAcceptsUnauthorized covers the other half of the HTTP move: InfluxDB 3
// authenticates every route, so a healthy server answers 401 on "/ping" when the check holds
// no token. That has to read as Ok, while the rest of the banding stays as it was.
func TestInfluxDBCheckAcceptsUnauthorized(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		want       types.Status
	}{
		{"1.x and 2.x answer 204", http.StatusNoContent, types.StatusOk},
		{"3.x answers 401 without a token", http.StatusUnauthorized, types.StatusOk},
		{"a server failing is still critical", http.StatusInternalServerError, types.StatusCritical},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/ping" {
					t.Errorf("check probed %q, want /ping", r.URL.Path)
				}

				w.WriteHeader(c.statusCode)
			}))
			defer srv.Close()

			got := checkStatusAgainst(t, InfluxDBService, srv.Listener.Addr().String()).CurrentStatus
			if got != c.want {
				t.Errorf("InfluxDB answering %d reported %v, want %v", c.statusCode, got, c.want)
			}
		})
	}
}

// TestHTTPCheckHonoursServiceConfig covers the way out of the one caveat of checking a
// reverse proxy over HTTP: the check asks for "/" and reports whatever the fronted app says
// there, so an app with nothing at its root reads as a warning and a VCL routing on the Host
// header reads as a critical. Both are the user's to fix with http_path and http_host, which
// means those have to win over the defaults this file sets per service type.
//
// The ordering is the fragile part -- InfluxDB's "/ping" is assigned before the override, so
// moving either would silently pin every InfluxDB check to /ping and ignore the setting.
func TestHTTPCheckHonoursServiceConfig(t *testing.T) {
	t.Run("http_path wins over the service type default", func(t *testing.T) {
		var gotPath string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path

			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		// InfluxDB is the case that has a default of its own to be overridden.
		checkStatusAgainstWithConfig(
			t, InfluxDBService, srv.Listener.Addr().String(),
			config.Service{HTTPPath: "/custom-health"}, //nolint:exhaustruct
		)

		if gotPath != "/custom-health" {
			t.Errorf("check probed %q, want /custom-health: http_path was ignored", gotPath)
		}
	})

	t.Run("http_host sets the Host header", func(t *testing.T) {
		var gotHost string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotHost = r.Host

			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		// Varnish is the case that needs it: a VCL routing on req.http.host has no backend
		// for the address the check would otherwise send.
		checkStatusAgainstWithConfig(
			t, VarnishService, srv.Listener.Addr().String(),
			config.Service{HTTPHost: "www.example.com"}, //nolint:exhaustruct
		)

		if gotHost != "www.example.com" {
			t.Errorf("check sent Host %q, want www.example.com: http_host was ignored", gotHost)
		}
	})

	t.Run("http_path rescues a backend with nothing at its root", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// An API-only backend: 404 everywhere except its health route.
			if r.URL.Path != "/health" {
				w.WriteHeader(http.StatusNotFound)

				return
			}

			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		addr := srv.Listener.Addr().String()

		if got := checkStatusAgainst(t, VarnishService, addr).CurrentStatus; got != types.StatusWarning {
			t.Errorf("Varnish fronting a rootless backend reported %v, want warning", got)
		}

		got := checkStatusAgainstWithConfig(
			t, VarnishService, addr,
			config.Service{HTTPPath: "/health"}, //nolint:exhaustruct
		).CurrentStatus
		if got != types.StatusOk {
			t.Errorf("with http_path=/health the same Varnish reported %v, want ok", got)
		}
	})
}

// TestChronyCheckOnUnlocatableContainerReportsUnknown covers a chrony container the runtime
// reports no address for (network_mode: none, or container:<other>).
//
// The check still has to exist and say it could not run. Creating none instead would publish
// no service_status point at all, which on the platform reads as an agent that stopped
// reporting rather than as a service nothing can locate -- and falling back to Glouton's own
// loopback would be worse still, reporting on whatever chronyd runs next to it under this
// service's name.
func TestChronyCheckOnUnlocatableContainerReportsUnknown(t *testing.T) {
	service := Service{ //nolint:exhaustruct
		Name:           string(NTPService),
		ServiceType:    NTPService,
		ServiceVariant: VariantChrony,
		ContainerID:    "1234",
		Active:         true,
	}

	if _, ok := chronyCmdAddress(service); ok {
		t.Fatal("chronyCmdAddress() located a container with no address")
	}

	d := &Discovery{ //nolint:exhaustruct
		metricRegistry: &mockRegistry{ //nolint:exhaustruct
			ExpectedAddedContains: []string{"check for " + string(NTPService)},
		},
		activeCheck: make(map[NameInstance]CheckDetails),
	}

	d.createCheck(service)

	details, ok := d.activeCheck[NameInstance{Name: string(NTPService), Instance: ""}]
	if !ok {
		t.Fatal("no check was created: the service would publish no status at all")
	}

	status, err := details.check.CheckNow(t.Context())
	if err != nil {
		t.Fatalf("CheckNow() = %v", err)
	}

	if status.CurrentStatus != types.StatusUnknown {
		t.Errorf("status = %v (%q), want unknown", status.CurrentStatus, status.StatusDescription)
	}
}
