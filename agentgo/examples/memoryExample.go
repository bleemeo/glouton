// Copyright 2015-2018 Bleemeo
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

// Example of memory module use

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
