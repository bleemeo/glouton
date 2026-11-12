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

package types

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestAuthenticatedSuccessCounter checks only the authenticated requests the server answered
// without error are counted.
func TestAuthenticatedSuccessCounter(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	var requests, successes atomic.Uint32

	client := &http.Client{Transport: NewHTTPTransport(nil, &CustomTransportOptions{
		UserAgentHeader:             "test",
		RequestCounter:              &requests,
		AuthenticatedSuccessCounter: &successes,
	})}

	for _, tt := range []struct {
		path          string
		authorization string
	}{
		{path: "/ok", authorization: "Bearer token"},
		{path: "/fail", authorization: "Bearer token"},
		{path: "/ok"},
		{path: "/ok", authorization: "Basic dXNlcjpwYXNz"},
	} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+tt.path, nil)
		if err != nil {
			t.Fatal(err)
		}

		if tt.authorization != "" {
			req.Header.Set("Authorization", tt.authorization)
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		_ = resp.Body.Close()
	}

	if requests.Load() != 4 || successes.Load() != 1 {
		t.Errorf("%d requests and %d authenticated successes, want 4 and 1", requests.Load(), successes.Load())
	}
}
