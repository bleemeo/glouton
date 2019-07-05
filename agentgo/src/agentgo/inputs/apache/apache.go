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

package apache

import (
	"agentgo/inputs/internal"
	"errors"
	"strings"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/apache"
)

// New initialise apache.Input
func New(url string) (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["apache"]
	if ok {
		apacheInput, ok := input().(*apache.Apache)
		if ok {
			slice := append(make([]string, 0), url)
			apacheInput.Urls = slice
			apacheInput.InsecureSkipVerify = false
			i = &internal.Input{
				Input: apacheInput,
				Accumulator: internal.Accumulator{
					DerivatedMetrics: []string{"TotalAccesses", "TotalkBytes", "handled"},
					TransformMetrics: transformMetrics,
				},
			}
		} else {
			err = errors.New("input Apache is not the expected type")
		}
	} else {
		err = errors.New("input Apache not enabled in Telegraf")
	}
	return
}

func transformMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	newFields := make(map[string]float64)
	maxWorker := 0.0
	for metricName, value := range fields {
		switch metricName {
		case "IdleWorkers":
			newFields["idle_workers"] = value
		case "TotalAccesses":
			newFields["requests"] = value
		case "TotalkBytes":
			newFields["bytes"] = value
		case "ConnsTotal":
			newFields["connections"] = value
		case "Uptime":
			newFields["uptime"] = value
		default:
			if strings.Contains(metricName, "scboard") {
				maxWorker += value
				newFields[strings.ReplaceAll(metricName, "scboard", "scoreboard")] = value
			}
		}
	}
	newFields["max_workers"] = maxWorker
	if idleWorker, ok := newFields["scoreboard_waiting"]; ok {
		if openWorker, ok := newFields["scoreboard_open"]; ok {
			newFields["busy_workers"] = maxWorker - idleWorker - openWorker
			newFields["busy_workers_perc"] = newFields["busy_workers"] / maxWorker * 100
		}
	}
	return newFields
}
