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

package registry

import (
	"fmt"
	"strings"
	"time"

	"github.com/bleemeo/glouton/types"
)

func DefaultSNMPRules(resolution time.Duration) []types.SimpleRule {
	defaultRules := []types.SimpleRule{
		{
			TargetName:  "mem_used",
			PromQLQuery: `sum without (hrStorageDescr, hrStorageIndex) (hrStorageUsed{hrStorageDescr="Real Memory"} * hrStorageAllocationUnits)`,
		},
		{
			TargetName:  "mem_free",
			PromQLQuery: `sum without (hrStorageDescr, hrStorageIndex) ((hrStorageSize{hrStorageDescr="Real Memory"} - hrStorageUsed) * hrStorageAllocationUnits)`,
		},
		{
			TargetName:  "mem_used_perc",
			PromQLQuery: `sum without (hrStorageDescr, hrStorageIndex) (hrStorageUsed{hrStorageDescr="Real Memory"}/hrStorageSize) * 100`,
		},
		{
			TargetName:  "mem_free",
			PromQLQuery: `sum without (cpmCPUTotalIndex) (cpmCPUMemoryFree * 1024)`,
		},
		{
			TargetName:  "mem_used",
			PromQLQuery: `sum without (cpmCPUTotalIndex) (cpmCPUMemoryUsed * 1024)`,
		},
		{
			TargetName:  "mem_used_perc",
			PromQLQuery: `sum without (cpmCPUTotalIndex) (cpmCPUMemoryUsed/(cpmCPUMemoryUsed+cpmCPUMemoryFree)) * 100`,
		},
		{
			TargetName:  "mem_used_perc",
			PromQLQuery: `sum without (ciscoMemoryPoolName, ciscoMemoryPoolType) (ciscoMemoryPoolUsed{ciscoMemoryPoolName=~"(Processor|System memory)"}/(ciscoMemoryPoolUsed+ciscoMemoryPoolFree)) * 100`,
		},
		{
			TargetName:  "net_bits_recv",
			PromQLQuery: "rate(ifInOctets[$__rate_interval]) * 8",
		},
		{
			TargetName:  "net_bits_sent",
			PromQLQuery: "rate(ifOutOctets[$__rate_interval]) * 8",
		},
		{
			TargetName:  "net_err_in",
			PromQLQuery: "rate(ifInErrors[$__rate_interval])",
		},
		{
			TargetName:  "net_err_out",
			PromQLQuery: "rate(ifOutErrors[$__rate_interval])",
		},
	}

	rateInterval := 4 * int(resolution.Seconds())

	replacer := strings.NewReplacer("$__rate_interval", fmt.Sprintf("%ds", rateInterval))
	for i, rule := range defaultRules {
		defaultRules[i].PromQLQuery = replacer.Replace(rule.PromQLQuery)
	}

	return defaultRules
}
