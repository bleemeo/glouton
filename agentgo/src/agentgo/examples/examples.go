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
	"agentgo/inputs"
	"agentgo/types"
	"fmt"
	// Needed to run this package
	_ "github.com/influxdata/telegraf/plugins/inputs/all"
	"time"
)

func main() {
	for {
		fmt.Println("----------------------------------------------------")
		acc := types.InitAccumulator()
		input, errInit := inputs.InitInputWithAddress("nginx", "http://172.17.0.3/nginx_status")
		var err = input.Gather(&acc)
		if err == nil && errInit == nil {
			var metricPoints = acc.GetMetricPointSlice()
			for _, metric := range metricPoints {
				fmt.Println(metric.Name, ": ", metric.Value)
			}
			time.Sleep(2000 * time.Millisecond)
		}
	}
}
