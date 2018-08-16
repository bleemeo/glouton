package main

import (
	"../system"
	"fmt"
	"time"
)

func main() {
	var virtualMemory = system.InitMemoryCollector()
	for {
		var memoryMetrics = virtualMemory.Gather()
		fmt.Println("----------------------------------------------------------------------------")
		for _, metricPoint := range memoryMetrics {
			var unit = ""
			switch metricPoint.Metric.Unit {
			case 1:
				unit = "B"
			case 2:
				unit = "%"
			default:
				unit = "unecpected unit"

			}
			fmt.Printf("%s : %f %s\n", metricPoint.Metric.Name, metricPoint.Value, unit)
		}
		time.Sleep(2 * 1000000000)
	}
}
