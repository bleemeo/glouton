// Copyright 2015-2019 Bleemeo
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

package synchronizer

import (
	"encoding/json"
	"fmt"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/config"
	"glouton/logger"
	"glouton/prometheus/exporter/blackbox"
)

// syncMonitors updates the list of metrics that must be watched locally (to be sent over MQTT, a metric must
// be declared both on the bleemeo API AND in the config file for now).
// FIXME: we are ignoring the notion of blackbox modules, and we shouldn't.
func (s *Synchronizer) syncMonitors(fullSync bool) error {
	// 1. extract the list of targets from the config
	// Note: I'm moderately happy with this cast, as it isn't checked statically by go
	cfg, present := s.option.Config.(*config.Configuration)
	if !present {
		logger.V(2).Println("probes: the configuration is not of the expected type '*config.Configuration'")
		return nil
	}

	bbConfRaw, present := cfg.Get("blackbox")
	if !present {
		return nil
	}

	bbConf, bbEnabled := blackbox.ReadConfig(bbConfRaw)
	if !bbEnabled {
		return nil
	}

	monitorsURL := map[string]bool{}

	for _, target := range bbConf.Targets {
		monitorsURL[target.URL] = true
	}

	// 2. obtain the list of user-declared monitors (exposed by bleemeo's API)
	apiMonitors, err := s.getMonitorsFromAPI()
	if err != nil {
		return err
	}

	// 3. filter probes to keep only those that are also present in the metrics
	newMonitors := map[string]bleemeoTypes.Monitor{}

	for _, apiMonitor := range apiMonitors {
		for url := range monitorsURL {
			if url == apiMonitor.URL {
				newMonitors[url] = apiMonitor
				break
			}
		}
	}

	oldMonitors := s.option.Cache.Monitors()

	// TODO: I kind of hope there is some more elegant way (like Rust's 'symmetric_difference' on HashSets)
	// to compute that difference ?
	for _, newMonitor := range newMonitors {
		found := false

		for _, oldMonitor := range oldMonitors {
			if oldMonitor.ID == newMonitor.ID {
				found = true
				break
			}
		}

		if !found {
			logger.V(2).Printf("New probe registered for '%s'", newMonitor.URL)
		}
	}

	for _, oldMonitor := range oldMonitors {
		found := false

		for _, newMonitor := range newMonitors {
			if oldMonitor.ID == newMonitor.ID {
				found = true
				break
			}
		}

		if !found {
			logger.V(2).Printf("The probe for '%s' is now deactivated", oldMonitor.URL)
		}
	}

	s.option.Cache.SetMonitors(newMonitors)

	return nil
}

func (s *Synchronizer) getMonitorsFromAPI() ([]bleemeoTypes.Monitor, error) {
	monitors := []bleemeoTypes.Monitor{}

	params := map[string]string{
		"monitor": "true",
		"active":  "true",
		"fields":  "id,agent,monitor_url",
	}

	result, err := s.client.Iter("service", params)
	if err != nil {
		return monitors, err
	}

	monitors = make([]bleemeoTypes.Monitor, 0, len(result))

	for _, jsonMessage := range result {
		var monitor bleemeoTypes.Monitor

		if err := json.Unmarshal(jsonMessage, &monitor); err != nil {
			return monitors, fmt.Errorf("couldn't parse monitor %v", jsonMessage)
		}

		monitors = append(monitors, monitor)
	}

	return monitors, nil
}
