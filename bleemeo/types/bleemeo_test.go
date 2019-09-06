// Copyright 2015-2019 Bleemeo
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
	"reflect"
	"testing"
)

func TestMetricsAgentWhitelistMap(t *testing.T) {
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
			" cpu used  ,agent_status\n\t",
			map[string]bool{
				"cpu used":     true,
				"agent_status": true,
			},
		},
	}

	for _, c := range cases {
		ac := AccountConfig{
			MetricsAgentWhitelist: c.flat,
		}
		got := ac.MetricsAgentWhitelistMap()
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("MetricsAgentWhitelistMap(%#v) == %v, want %v", c.flat, got, c.want)
		}
	}
}
