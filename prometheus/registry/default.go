package registry

import (
	"fmt"
	"strings"
	"time"
)

func DefaultSNMPRules(resolution time.Duration) []SimpleRule {
	defaultRules := []SimpleRule{
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
