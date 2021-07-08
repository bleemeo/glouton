// Copyright 2015-2021 Bleemeo
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

package snmp

import (
	"fmt"
	"glouton/logger"
	"glouton/prometheus/scrapper"
	"glouton/types"
	"net/url"
)

func ConfigToURLs(vMap []interface{}, address string) (result []*scrapper.Target) {
	for dict := range vMap {
		tmp := vMap[dict].(map[string]interface{})
		urlText := fmt.Sprintf("%s/snmp?module=%s&target=%s", address, tmp["module"], tmp["target"])

		u, err := url.Parse(urlText)
		if err != nil {
			logger.Printf("ignoring invalid exporter config: %v", err)
			continue
		}

		name, _ := tmp["name"].(string)

		target := &scrapper.Target{
			ExtraLabels: map[string]string{
				types.LabelMetaScrapeJob: name,
				// HostPort could be empty, but this ExtraLabels is used by Registry which
				// correctly handle empty value value (drop the label).
				types.LabelMetaScrapeInstance: scrapper.HostPort(u),
				types.LabelSnmpTarget:       tmp["target"].(string),
			},
			URL:       u,
			AllowList: []string{},
			DenyList:  []string{},
		}

		result = append(result, target)
	}

	return result
}
