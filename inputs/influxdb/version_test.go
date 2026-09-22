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

package influxdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDetectLine replays what each line really answers on /ping. The status codes and
// header values are the ones measured against influxdb:1.12, influxdb:2 and
// influxdb:3-core, including the "v" that 2.x puts in front of its version and 1.x does
// not, and the 401 that 3.x answers there while the older two do not even with
// authentication enabled.
func TestDetectLine(t *testing.T) {
	cases := []struct {
		testName string
		status   int
		version  string
		token    string
		want     line
	}{
		{
			testName: "influxdb 1.12 answers 204 and its version bare",
			status:   http.StatusNoContent,
			version:  "1.12.4",
			want:     lineV1,
		},
		{
			// Measured with INFLUXDB_HTTP_AUTH_ENABLED=true and no credentials sent: 1.x
			// leaves /ping open, which is what makes this probe usable at all.
			testName: "influxdb 1.12 with auth enabled still answers /ping",
			status:   http.StatusNoContent,
			version:  "1.12.4",
			want:     lineV1,
		},
		{
			testName: "influxdb 2.9 prefixes its version with a v",
			status:   http.StatusNoContent,
			version:  "v2.9.1",
			want:     lineV2,
		},
		{
			testName: "influxdb 3 without auth answers 200 and its version",
			status:   http.StatusOK,
			version:  "3.11.4",
			want:     lineV3,
		},
		{
			// The default settings. Nothing else refuses /ping, so the refusal is the
			// identification.
			testName: "influxdb 3 with auth refuses /ping",
			status:   http.StatusUnauthorized,
			version:  "",
			want:     lineV3,
		},
		{
			testName: "influxdb 3 with a working token answers as itself",
			status:   http.StatusOK,
			version:  "3.11.4",
			token:    "a-token",
			want:     lineV3,
		},
		{
			testName: "something else on the port",
			status:   http.StatusOK,
			version:  "",
			want:     lineUnknown,
		},
	}

	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			var gotAuth string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")

				if c.version != "" {
					w.Header().Set(versionHeader, c.version)
				}

				w.WriteHeader(c.status)
			}))

			t.Cleanup(server.Close)

			got, reported, err := detectLine(context.Background(), server.URL+"/ping", c.token)
			if err != nil {
				t.Fatalf("detectLine() = %v", err)
			}

			if got != c.want {
				t.Errorf("line = %v, want %v", got, c.want)
			}

			if reported != c.version {
				t.Errorf("reported version = %q, want %q", reported, c.version)
			}

			wantAuth := ""
			if c.token != "" {
				wantAuth = "Bearer " + c.token
			}

			if gotAuth != wantAuth {
				t.Errorf("Authorization = %q, want %q", gotAuth, wantAuth)
			}
		})
	}
}

// TestDetectLineOnBadStatus checks a status that identifies nothing is an error rather
// than a guess: reading the wrong endpoint for the wrong line would publish nothing and
// explain nothing.
func TestDetectLineOnBadStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	t.Cleanup(server.Close)

	got, _, err := detectLine(context.Background(), server.URL+"/ping", "")
	if err == nil {
		t.Error("detectLine() = nil error on a 500, want one")
	}

	if got != lineUnknown {
		t.Errorf("line = %v, want %v", got, lineUnknown)
	}
}

// TestLineFromVersion pins the parsing on its own, including the shapes that are not a
// version at all.
func TestLineFromVersion(t *testing.T) {
	cases := map[string]line{
		"1.12.4":            lineV1,
		"1.8.10":            lineV1,
		"v2.9.1":            lineV2,
		"2.7.11":            lineV2,
		"3.11.4":            lineV3,
		"v3.0.0":            lineV3,
		"  3.11.4  ":        lineV3,
		"":                  lineUnknown,
		"unknown":           lineUnknown,
		"4.0.0":             lineUnknown,
		"nightly-abcdef123": lineUnknown,
	}

	for reported, want := range cases {
		if got := lineFromVersion(reported); got != want {
			t.Errorf("lineFromVersion(%q) = %v, want %v", reported, got, want)
		}
	}
}
