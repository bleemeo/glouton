package renamer

import (
	"glouton/types"

	"github.com/prometheus/prometheus/pkg/labels"
)

func GetDefaultRules() []Rule {
	return []Rule{
		{
			MetricName: "hrProcessorLoad",
			RewriteRules: []RewriteRule{
				{
					LabelName: types.LabelName,
					NewValue:  "cpu_used",
				},
				{
					LabelName: "hrDeviceDescr",
					NewValue:  "",
				},
				{
					LabelName:    "hrDeviceIndex",
					NewLabelName: "core",
					NewValue:     "$1",
				},
			},
		},
		{
			MetricName: "cpmCPUTotal1minRev",
			RewriteRules: []RewriteRule{
				{
					LabelName: types.LabelName,
					NewValue:  "cpu_used",
				},
				{
					LabelName:    "cpmCPUTotalIndex",
					NewLabelName: "core",
					NewValue:     "$1",
				},
			},
		},
		{
			MetricName: "rlCpuUtilDuringLastMinute",
			RewriteRules: []RewriteRule{
				{
					LabelName: types.LabelName,
					NewValue:  "cpu_used",
				},
			},
		},
		{
			MetricName: "hrStorageUsed",
			LabelMatchers: []*labels.Matcher{
				labels.MustNewMatcher(labels.MatchEqual, "hrStorageDescr", "Real Memory"),
			},
			RewriteRules: []RewriteRule{
				{
					LabelName: types.LabelName,
					NewValue:  "mem_used",
				},
				{
					LabelName: "hrStorageDescr",
					NewValue:  "",
				},
			},
		},
		{
			MetricName: "ciscoMemoryPoolUsed",
			LabelMatchers: []*labels.Matcher{
				labels.MustNewMatcher(labels.MatchRegexp, "ciscoMemoryPoolName", "(Processor|System memory)"),
			},
			RewriteRules: []RewriteRule{
				{
					LabelName: types.LabelName,
					NewValue:  "mem_used",
				},
				{
					LabelName: "ciscoMemoryPoolName",
					NewValue:  "",
				},
			},
		},
		{
			MetricName: "cpmCPUMemoryUsed",
			RewriteRules: []RewriteRule{
				{
					LabelName: types.LabelName,
					NewValue:  "mem_used",
				},
				{
					LabelName: "cpmCPUTotalIndex",
					NewValue:  "",
				},
			},
		},
		{
			MetricName: "ciscoMemoryPoolFree",
			LabelMatchers: []*labels.Matcher{
				labels.MustNewMatcher(labels.MatchRegexp, "ciscoMemoryPoolName", "(Processor|System memory)"),
			},
			RewriteRules: []RewriteRule{
				{
					LabelName: types.LabelName,
					NewValue:  "mem_free",
				},
				{
					LabelName: "ciscoMemoryPoolName",
					NewValue:  "",
				},
			},
		},
		{
			MetricName: "cpmCPUMemoryFree",
			RewriteRules: []RewriteRule{
				{
					LabelName: types.LabelName,
					NewValue:  "mem_free",
				},
				{
					LabelName: "cpmCPUTotalIndex",
					NewValue:  "",
				},
			},
		},
		{
			MetricName: "ciscoEnvMonTemperatureStatusValue",
			RewriteRules: []RewriteRule{
				{
					LabelName: types.LabelName,
					NewValue:  "temperature",
				},
				{
					LabelName:    "ciscoEnvMonTemperatureStatusDescr",
					NewLabelName: "sensor",
					NewValue:     "$1",
				},
			},
		},
	}
}
