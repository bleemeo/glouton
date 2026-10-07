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
	"encoding/json"
	"strings"
	"testing"
)

// TestGloutonFeaturesFromAPI checks the Agent's glouton_features as the API may send them: a
// value Glouton does not know, such as a state other than on and off, must not fail reading
// the whole Agent, and only true enables a feature.
func TestGloutonFeaturesFromAPI(t *testing.T) {
	body := `{"id": "agent-id", "glouton_features": {"send_registration_status": true, "off": false, "partial": "partial", "nested": {"a": 1}}}`

	var agent Agent

	if err := json.Unmarshal([]byte(body), &agent); err != nil {
		t.Fatalf("an Agent with unknown feature values does not decode: %v", err)
	}

	for feature, want := range map[string]bool{
		FeatureSendRegistrationStatus: true,
		"off":                         false,
		"partial":                     false,
		"nested":                      false,
		"absent":                      false,
	} {
		if got := agent.GloutonFeatures.Enabled(feature); got != want {
			t.Errorf("Enabled(%q) = %v, want %v", feature, got, want)
		}
	}

	// An Agent from an API without the field, or from an older cache, enables nothing.
	var older Agent

	if err := json.Unmarshal([]byte(`{"id": "agent-id"}`), &older); err != nil {
		t.Fatal(err)
	}

	if older.GloutonFeatures.Enabled(FeatureSendRegistrationStatus) {
		t.Error("an Agent without glouton_features enables a feature")
	}
}

// TestGloutonFeaturesNotSent checks an Agent without features is sent without the field: the
// Agent is also the payload registering SNMP and vSphere agents, which must not send a null.
func TestGloutonFeaturesNotSent(t *testing.T) {
	body, err := json.Marshal(Agent{ID: "agent-id"})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(body), "glouton_features") {
		t.Errorf("an Agent without features is sent with them: %s", body)
	}
}
