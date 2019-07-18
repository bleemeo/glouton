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

// Package for nginx input not finished

package nginx

import (
	"agentgo/inputs/internal"
	"errors"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/nginx"
)

// New initialise nginx.Input
func New(url string) (i telegraf.Input, err error) {
	var input, ok = telegraf_inputs.Inputs["nginx"]
	if ok {
		nginxInput, ok := input().(*nginx.Nginx)
		if ok {
			slice := append(make([]string, 0), url)
			nginxInput.Urls = slice
			nginxInput.InsecureSkipVerify = true
			i = &internal.Input{
				Input: nginxInput,
				Accumulator: internal.Accumulator{
					DerivatedMetrics: []string{"requests", "accepts", "handled"},
					TransformMetrics: transformMetrics,
				},
			}
		} else {
			err = errors.New("input nginx is not the expected type")
		}
	} else {
		err = errors.New("input nginx is not enabled in Telegraf")
	}
	return
}

func transformMetrics(originalContext internal.GatherContext, currentContext internal.GatherContext, fields map[string]float64, originalFields map[string]interface{}) map[string]float64 {
	newFields := make(map[string]float64)
	for metricName, value := range fields {
		switch metricName {
		case "accepts":
			newFields["nginx_connections_accepted"] = value
		case "handled", "active", "waiting", "reading", "writing":
			newFields["connection_"+metricName] = value
		default:
			newFields[metricName] = value
		}
	}
	return newFields
}
