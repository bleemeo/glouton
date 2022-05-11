package registry

func DefaultSNMPRules() []SimpleRule {
	return []SimpleRule{
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
			PromQLQuery: "irate(ifInOctets[5m]) * 8",
		},
		{
			TargetName:  "net_bits_sent",
			PromQLQuery: "irate(ifOutOctets[5m]) * 8",
		},
		{
			TargetName:  "net_err_in",
			PromQLQuery: "irate(ifInErrors[5m])",
		},
		{
			TargetName:  "net_err_out",
			PromQLQuery: "irate(ifOutErrors[5m])",
		},
	}
}
