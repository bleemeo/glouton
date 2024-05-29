// Copyright 2015-2024 Bleemeo
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

package cache

import (
	"reflect"
	"testing"

	"github.com/bleemeo/glouton/agent/state"
	"github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/threshold"

	"github.com/google/go-cmp/cmp"
)

func Test_allowListToMap(t *testing.T) {
	cases := []struct {
		flat string
		want map[string]bool
	}{
		{
			"",
			nil,
		},
		{
			"cpu_used,agent_status",
			map[string]bool{
				"cpu_used":     true,
				"agent_status": true,
			},
		},
		{
			"   cpu_used  ,   agent_status   ",
			map[string]bool{
				"cpu_used":     true,
				"agent_status": true,
			},
		},
		{
			" cpu_used  ,agent_status\n\t",
			map[string]bool{
				"cpu_used":     true,
				"agent_status": true,
			},
		},
		{
			"agent_config_warning,\nagent_status,\ncertificate_day_left_status,\n",
			map[string]bool{
				"agent_config_warning":        true,
				"agent_status":                true,
				"certificate_day_left_status": true,
			},
		},
		{
			"agent_config_warning,\r\nagent_status,\n\rcertificate_day_left_status,\rcpu_used",
			map[string]bool{
				"agent_config_warning":        true,
				"agent_status":                true,
				"cpu_used":                    true,
				"certificate_day_left_status": true,
			},
		},
		{
			"metric1 metric-2\tmetric_3\nmetric04,metric:5,,\n\n\t   ,,,metric6",
			map[string]bool{
				"metric1":  true,
				"metric-2": true,
				"metric_3": true,
				"metric04": true,
				"metric:5": true,
				"metric6":  true,
			},
		},
	}

	for _, c := range cases {
		got := allowListToMap(c.flat)
		if diff := cmp.Diff(c.want, got); diff != "" {
			t.Errorf("allowListToMap mismatch (-want +got)\n%s", diff)
		}
	}
}

func TestUpgradeFromV1(t *testing.T) {
	state, err := state.Load("testdata/state-v1.json", "testdata/state-v1.json")
	if err != nil {
		t.Fatal(err)
	}

	cache := Load(state)

	checkMetrics(t, cache)
	checkAccountConfigs(t, cache, false)
}

func TestUpgradeFromV2(t *testing.T) {
	state, err := state.Load("testdata/state-v2.json", "testdata/state-v2.json")
	if err != nil {
		t.Fatal(err)
	}

	cache := Load(state)

	checkMetrics(t, cache)
	checkAccountConfigs(t, cache, true)
	checkMonitors(t, cache)
}

func TestUpgradeFromV6(t *testing.T) {
	state, err := state.Load("testdata/state-v6.json", "testdata/state-v6.json")
	if err != nil {
		t.Fatal(err)
	}

	cache := Load(state)

	checkMetrics(t, cache)
	checkAccountConfigs(t, cache, true)
	checkMonitors(t, cache)
}

func checkMetrics(t *testing.T, cache *Cache) {
	t.Helper()

	wantMetrics := []types.Metric{
		{
			ID:         "8e930d86-8c51-4b3a-8601-cf6a2b1b4997",
			AgentID:    "d5732833-fc1b-43c7-b253-565b56701651", // AgentID on metric was added in v5.
			LabelsText: "__name__=\"agent_status\"",
			Labels:     map[string]string{"__name__": "agent_status"},
			Threshold: types.Threshold{
				LowWarning:   nil,
				LowCritical:  nil,
				HighWarning:  nil,
				HighCritical: floatToPointer(80),
			},
			Unit: threshold.Unit{
				UnitType: 0,
				UnitText: "No unit",
			},
		},
		{
			ID:         "1c412097-e83b-4afa-99a1-7503bc712b70",
			AgentID:    "d5732833-fc1b-43c7-b253-565b56701651",
			LabelsText: "__name__=\"agent_sent_message\",item=\"my_item\"", // _item was renamed to item in v4.
			Labels:     map[string]string{"__name__": "agent_sent_message", "item": "my_item"},
			Threshold: types.Threshold{
				LowWarning:   nil,
				LowCritical:  floatToPointer(20),
				HighWarning:  nil,
				HighCritical: nil,
			},
			Unit: threshold.Unit{
				UnitType: 0,
				UnitText: "No unit",
			},
		},
	}

	gotMetrics := cache.Metrics()

	if len(gotMetrics) != 2 {
		t.Errorf("want 2 metrics, got %d", len(gotMetrics))
	}

	for i, gotMetric := range gotMetrics {
		if !reflect.DeepEqual(gotMetric, wantMetrics[i]) {
			t.Errorf("want %#v, got %#v", wantMetrics[i], gotMetric)
		}
	}
}

func checkAccountConfigs(t *testing.T, cache *Cache, liveProcess bool) {
	t.Helper()

	wantAccountConfig := types.AccountConfig{
		ID:                    "d7b022ba-e230-4776-8018-465e681e096e",
		Name:                  "default",
		LiveProcessResolution: 10,
		LiveProcess:           liveProcess,
		DockerIntegration:     true,
	}

	gotAccountConfigs := cache.AccountConfigs()

	if len(gotAccountConfigs) != 1 {
		t.Fatalf("want 1 account config, got %d", len(gotAccountConfigs))
	}

	if gotAccountConfigs[0] != wantAccountConfig {
		t.Errorf("want %#v, got %#v", wantAccountConfig, gotAccountConfigs[0])
	}
}

func checkMonitors(t *testing.T, cache *Cache) {
	t.Helper()

	gotMonitors := cache.Monitors()
	if len(gotMonitors) != 1 {
		t.Errorf("want 1 monitor, got %d", len(gotMonitors))
	}

	if gotMonitors[0].URL != "example.com" {
		t.Errorf("want monitor url 'example.com', got '%s'", gotMonitors[0].URL)
	}
}

func floatToPointer(f float64) *float64 {
	return &f
}
