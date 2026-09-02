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

package api

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bleemeo/glouton/config"
)

func TestDenyBrowserRequest(t *testing.T) {
	t.Parallel()

	// Allows "localhost" (the default configuration), plus the name of a proxy the user added.
	allowedHosts := []string{
		"localhost",
		"Glouton.Example.com.",
	}

	cases := []struct {
		name       string
		host       string
		secFetch   string
		mode       string
		dest       string
		wantDenied bool
	}{
		{
			name:     "scraper without Sec-Fetch", // e.g. curl, Prometheus scraper, ...
			host:     "glouton.default.svc.cluster.local:8015",
			secFetch: "",
		},
		{
			name:     "URL typed in the address bar",
			host:     "localhost:8015",
			secFetch: "none",
		},
		{
			name:     "local UI fetching its own API",
			host:     "127.0.0.1:8015",
			secFetch: "same-origin",
		},
		{
			name:     "local UI reached over IPv6",
			host:     "[::1]:8015",
			secFetch: "same-origin",
		},
		{
			name:     "local UI reached on an IP of the machine",
			host:     "192.168.0.1:8015",
			secFetch: "same-origin",
		},
		{
			name:     "local UI reached through a configured proxy name",
			host:     "glouton.example.com:8015",
			secFetch: "same-origin",
		},
		{
			name:     "link from another website to the local UI",
			host:     "localhost:8015",
			secFetch: "cross-site",
			mode:     "navigate",
			dest:     "document",
		},
		{
			name:       "another website framing the local UI",
			host:       "localhost:8015",
			secFetch:   "cross-site",
			mode:       "navigate",
			dest:       "iframe",
			wantDenied: true,
		},
		{
			name:       "malicious website reading our answer",
			host:       "127.0.0.1:8015",
			secFetch:   "cross-site",
			mode:       "cors",
			dest:       "empty",
			wantDenied: true,
		},
		{
			name:       "page served from another local port",
			host:       "localhost:8015",
			secFetch:   "same-site",
			wantDenied: true,
		},
		{
			name:       "DNS rebinding",
			host:       "evil.example:8015",
			secFetch:   "same-origin",
			wantDenied: true,
		},
		{
			name:       "DNS rebinding on a top-level navigation",
			host:       "evil.example",
			secFetch:   "none",
			wantDenied: true,
		},
		{
			name:       "DNS rebinding using a look-alike name",
			host:       "localhost.evil.example:8015",
			secFetch:   "same-origin",
			wantDenied: true,
		},
		{
			name:       "name of the machine, not configured",
			host:       "my-server.example.com:8015",
			secFetch:   "same-origin",
			wantDenied: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/data/config", nil)
			req.Host = tc.host

			if tc.secFetch != "" {
				req.Header.Set("Sec-Fetch-Site", tc.secFetch)
			}

			if tc.mode != "" {
				req.Header.Set("Sec-Fetch-Mode", tc.mode)
				req.Header.Set("Sec-Fetch-Dest", tc.dest)
			}

			reason, hint := denyBrowserRequest(req, normalizeHosts(allowedHosts))
			if denied := reason != ""; denied != tc.wantDenied {
				t.Errorf("denied = %v (%q), want %v", denied, reason, tc.wantDenied)
			}

			if (hint != "") != (reason != "") {
				t.Errorf("reason = %q but hint = %q, want both or neither", reason, hint)
			}
		})
	}
}

// TestDenyBrowserRequestWithoutAllowedHosts checks that emptying
// web.listener.allowed_hosts does remove localhost.
func TestDenyBrowserRequestWithoutAllowedHosts(t *testing.T) {
	t.Parallel()

	for _, host := range []string{"localhost:8015", "127.0.0.1:8015"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/data/config", nil)
		req.Host = host
		req.Header.Set("Sec-Fetch-Site", "same-origin")

		reason, _ := denyBrowserRequest(req, nil)

		// The IP address stays allowed, it needs no allow-list.
		wantDenied := host == "localhost:8015"
		if denied := reason != ""; denied != wantDenied {
			t.Errorf("Host %s: denied = %v (%q), want %v", host, denied, reason, wantDenied)
		}
	}
}

// TestRouterCrossOrigin checks that a website can neither read nor even
// reach the local API through the browser of a user running Glouton.
func TestRouterCrossOrigin(t *testing.T) {
	t.Parallel()

	api := &API{Config: config.DefaultConfig()}
	api.init()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:8015/data/config", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")

	rec := httptest.NewRecorder()
	api.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want no such header", got)
	}
}

// TestRouterSameOrigin checks the local UI still works.
func TestRouterSameOrigin(t *testing.T) {
	t.Parallel()

	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "# a metric")
	})

	api := &API{Config: config.DefaultConfig(), PrometheusExporter: metrics}
	api.init()

	for _, path := range []string{"/", "/data/config", "/metrics"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost:8015"+path, nil)
		req.Header.Set("Sec-Fetch-Site", "same-origin")

		rec := httptest.NewRecorder()
		api.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want %d", path, rec.Code, http.StatusOK)
		}

		if rec.Body.Len() == 0 {
			t.Errorf("%s: empty body", path)
		}
	}
}

func TestIsLoopbackListener(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr net.Addr
		want bool
	}{
		{name: "loopback", addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8015}, want: true},
		{name: "another loopback address", addr: &net.TCPAddr{IP: net.IPv4(127, 0, 1, 1), Port: 8015}, want: true},
		{name: "IPv6 loopback", addr: &net.TCPAddr{IP: net.IPv6loopback, Port: 8015}, want: true},
		{name: "every IPv4 interface", addr: &net.TCPAddr{IP: net.IPv4zero, Port: 8015}, want: false},
		{name: "every IPv6 interface", addr: &net.TCPAddr{IP: net.IPv6zero, Port: 8015}, want: false},
		{name: "unspecified", addr: &net.TCPAddr{Port: 8015}, want: false},
		{name: "an address of the machine", addr: &net.TCPAddr{IP: net.IPv4(192, 168, 0, 1), Port: 8015}, want: false},
		{name: "not TCP", addr: &net.UnixAddr{Name: "/run/glouton.sock"}, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := isLoopbackListener(tc.addr); got != tc.want {
				t.Errorf("isLoopbackListener(%s) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

// TestIsLoopbackListenerReal checks the assumption that a real TCP listener
// tells us the address it is bound to.
func TestIsLoopbackListenerReal(t *testing.T) {
	t.Parallel()

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(t.Context(), "tcp", "localhost:0")
	if err != nil {
		t.Skipf("can't listen on localhost: %v", err)
	}

	defer listener.Close()

	if !isLoopbackListener(listener.Addr()) {
		t.Errorf("isLoopbackListener(%s) = false, want true", listener.Addr())
	}
}
