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

// Example of cpu module use

package main

import (
	"agentgo/inputs/cpu"
	"agentgo/types"
	"fmt"
	"github.com/influxdata/telegraf"
	"time"
)

var inputsgroups = make(map[int]map[int]telegraf.Input)

func main() {
	inputsgroups[1] = make(map[int]telegraf.Input)
	inputsgroups[1][1] = cpu.NewInput()
	for {
		fmt.Println("----------------------------------------------------")
		acc := types.InitAccumulator()
		fmt.Println("Go: ", acc)
		var err = inputsgroups[1][1].Gather(&acc)
		fmt.Println("Go: ", acc)
		if err == nil {
			var metricPoints = acc.GetMetricPointSlice()
			for _, metric := range metricPoints {
				fmt.Println(metric.Name, ": ", metric.Value)
			}
			time.Sleep(2000 * time.Millisecond)
		} else {
			fmt.Println("Error")
		}
	}
}
