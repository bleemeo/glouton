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
