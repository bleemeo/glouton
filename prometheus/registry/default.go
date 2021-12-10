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
			TargetName:  "mem_free",
			PromQLQuery: `sum without (cpmCPUTotalIndex) (cpmCPUMemoryFree * 1024)`,
		},
		{
			TargetName:  "mem_used",
			PromQLQuery: `sum without (cpmCPUTotalIndex) (cpmCPUMemoryUsed * 1024)`,
		},
	}
}
