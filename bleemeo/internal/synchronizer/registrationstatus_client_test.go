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

package synchronizer

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bleemeo/bleemeo-go"
)

// registrationStatusAPI is a Bleemeo API answering the agent PATCH as bleemeo-api does: with the
// fields asked for plus those the request sets.
type registrationStatusAPI struct {
	body   map[string]json.RawMessage
	fields string
}

func (api *registrationStatusAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.URL.Path == "/o/token/" {
		_, _ = io.WriteString(w, `{"access_token":"token","token_type":"Bearer","refresh_token":"refresh","expires_in":3600}`)

		return
	}

	if r.Method != http.MethodPatch || r.URL.Path != "/v1/agent/agent-id/" {
		http.NotFound(w, r)

		return
	}

	api.fields = r.URL.Query().Get("fields")

	if err := json.NewDecoder(r.Body).Decode(&api.body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	response := map[string]json.RawMessage{
		"id":                  json.RawMessage(`"agent-id"`),
		"registration_status": api.body["registration_status"],
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// TestUpdateAgentRegistrationStatus checks the request sent.
func TestUpdateAgentRegistrationStatus(t *testing.T) {
	api := &registrationStatusAPI{}

	server := httptest.NewServer(api)
	defer server.Close()

	client, err := bleemeo.NewClient(
		bleemeo.WithCredentials("user", "password"),
		bleemeo.WithOAuthClient(gloutonOAuthClientID, ""),
		bleemeo.WithEndpoint(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	cl := &wrapperClient{client: client}

	if err := cl.UpdateAgentRegistrationStatus(t.Context(), "agent-id", map[string][]int{"services": {}}); err != nil {
		t.Fatal(err)
	}

	if got := string(api.body["registration_status"]); got != `{"services":[]}` {
		t.Errorf("registration_status sent = %s, want {\"services\":[]}", got)
	}

	if api.fields != "id" {
		t.Errorf("fields = %q, want id", api.fields)
	}
}
