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

// Target represents a snmp config instance.
type Target struct {
	InitialName string
	Address     string
	Type        string
	URL         *url.URL
}

func ConfigToURLs(vMap []interface{}, address string) (result []Target) {
	for _, iMap := range vMap {
		tmp, ok := iMap.(map[string]interface{})

		if !ok {
			continue
		}

		module := "if_mib"

		target, ok := tmp["target"].(string)
		if !ok {
			logger.Printf("Warning: target is absent from the snmp configuration. the scrap URL will not work.")

			continue
		}

		initialName, ok := tmp["initial_name"].(string)
		if !ok {
			initialName = target
		}

		urlText := fmt.Sprintf("%s/snmp?module=%s&target=%s", address, module, target)

		u, err := url.Parse(urlText)
		if err != nil {
			logger.Printf("ignoring invalid exporter config: %v", err)

			continue
		}

		t := Target{
			InitialName: initialName,
			Address:     target,
			URL:         u,
		}

		result = append(result, t)
	}

	return result
}

func GenerateScrapperTargets(snmpTargets []Target) (result []*scrapper.Target) {
	for _, t := range snmpTargets {
		target := &scrapper.Target{
			ExtraLabels: map[string]string{
				types.LabelMetaScrapeJob: t.InitialName,
				// HostPort could be empty, but this ExtraLabels is used by Registry which
				// correctly handle empty value value (drop the label).
				types.LabelMetaScrapeInstance: scrapper.HostPort(t.URL),
				types.LabelSNMPTarget:         t.Address,
			},
			URL:       t.URL,
			AllowList: []string{},
			DenyList:  []string{},
		}

		result = append(result, target)
	}

	return result
}
