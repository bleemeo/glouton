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

package check

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bleemeo/glouton/types"
)

func TestNewHTTP_mainTCPAddress(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{name: "ipv4", url: "http://172.17.0.5:8080/", want: "172.17.0.5:8080"},
		{name: "ipv6", url: "http://[2001:db8::1]:8080/", want: "[2001:db8::1]:8080"},
		{name: "ipv6-default-port", url: "http://[2001:db8::1]/", want: "[2001:db8::1]:80"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hc := NewHTTP(c.url, "", nil, false, 0, nil, nil, types.MetricAnnotations{}, nil)

			if hc.mainTCPAddress != c.want {
				t.Errorf("NewHTTP(%q).mainTCPAddress = %q, want %q", c.url, hc.mainTCPAddress, c.want)
			}

			if _, _, err := net.SplitHostPort(hc.mainTCPAddress); err != nil {
				t.Errorf("net.SplitHostPort(%q) failed: %v", hc.mainTCPAddress, err)
			}
		})
	}
}

// TestHTTPOkStatusCodes covers the codes a server answers by design being taken for Ok,
// without widening what else is accepted.
//
// The case it exists for is InfluxDB 3, which authenticates every route and so answers 401
// on "/ping" when the check holds no token -- the server saying it is up and asking who is
// calling. The 1.x and 2.x lines answer 204 there, so both have to read as Ok.
func TestHTTPOkStatusCodes(t *testing.T) {
	cases := []struct {
		name          string
		statusCode    int
		okStatusCodes []int
		want          types.Status
	}{
		// What InfluxDB actually answers: 204 from 1.x and 2.x, 401 from 3.x with
		// authentication on, both from a healthy server.
		{name: "influxdb 1.x and 2.x ping", statusCode: http.StatusNoContent, okStatusCodes: []int{http.StatusUnauthorized}, want: types.StatusOk},
		{name: "influxdb 3 ping without a token", statusCode: http.StatusUnauthorized, okStatusCodes: []int{http.StatusUnauthorized}, want: types.StatusOk},

		// The exception stays narrow: every other failure reads as it did before.
		{name: "another 4xx is still a warning", statusCode: http.StatusNotFound, okStatusCodes: []int{http.StatusUnauthorized}, want: types.StatusWarning},
		{name: "5xx is still critical", statusCode: http.StatusServiceUnavailable, okStatusCodes: []int{http.StatusUnauthorized}, want: types.StatusCritical},

		// And it changes nothing for the services that pass none.
		{name: "401 without the exception is a warning", statusCode: http.StatusUnauthorized, okStatusCodes: nil, want: types.StatusWarning},
		{name: "success without the exception is ok", statusCode: http.StatusOK, okStatusCodes: nil, want: types.StatusOk},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.statusCode)
			}))
			defer srv.Close()

			hc := NewHTTP(srv.URL, "", nil, false, 0, c.okStatusCodes, nil, types.MetricAnnotations{}, nil)

			got := hc.httpMainCheck(t.Context())
			if got.CurrentStatus != c.want {
				t.Errorf(
					"status %d with okStatusCodes %v = %v (%q), want %v",
					c.statusCode, c.okStatusCodes, got.CurrentStatus, got.StatusDescription, c.want,
				)
			}
		})
	}
}

// TestHTTPExpectedStatusCodeWinsOverOkStatusCodes pins that a status code named in the
// configuration still means exactly that one: it is the user saying what this service
// answers, so a built-in exception must not widen it behind their back.
func TestHTTPExpectedStatusCodeWinsOverOkStatusCodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	hc := NewHTTP(srv.URL, "", nil, false, http.StatusOK, []int{http.StatusUnauthorized}, nil, types.MetricAnnotations{}, nil)

	if got := hc.httpMainCheck(t.Context()); got.CurrentStatus != types.StatusCritical {
		t.Errorf("401 with expectedStatusCode 200 = %v (%q), want critical", got.CurrentStatus, got.StatusDescription)
	}
}
