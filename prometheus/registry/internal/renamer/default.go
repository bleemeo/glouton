// Copyright 2015-2023 Bleemeo
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

package renamer

import (
	"glouton/types"

	"github.com/prometheus/prometheus/model/labels"
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
				{
					LabelName: "ciscoMemoryPoolType",
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
				{
					LabelName: "ciscoMemoryPoolType",
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
		{
			MetricName: "rlPhdUnitEnvParamTempSensorValue",
			LabelMatchers: []*labels.Matcher{
				labels.MustNewMatcher(labels.MatchEqual, "rlPhdUnitEnvParamStackUnit", "1"),
			},
			RewriteRules: []RewriteRule{
				{
					LabelName: types.LabelName,
					NewValue:  "temperature",
				},
				{
					LabelName:    "rlPhdUnitEnvParamStackUnit",
					NewLabelName: "sensor",
					NewValue:     "CPU",
				},
			},
		},
	}
}
