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
	"glouton/logger"
	"glouton/types"
)

// update the list of metrics that must be watched locally (to be sent over MQTT, a metric must be declared
// both on the bleemeo API AND in the config file for now).
// TODO: caching
// FIXME: we are ignoring the notion of blackbox modules, and we shouldn't.
func (s *Synchronizer) syncMonitors(fullSync bool) error {
	// 1. extract the probes from the list of metrics
	metrics, err := s.option.Store.Metrics(nil)
	if err != nil {
		return err
	}

	monitorsURL := map[string]bool{}

	for _, metric := range metrics {
		if metric.Annotations().Kind == types.MonitorMetricKind {
			url, present := metric.Labels()["instance"]
			if !present {
				logger.V(2).Printf("Abnormal behavior: Couldn't find label 'instance' on metric %v", metric)
				continue
			}

			monitorsURL[url] = true
		}
	}

	// 2. Obtain the list of user-declared monitors (exposed by bleemeo's API)
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

	// TODO: I hope there is some elegant way (like Rust's 'symmetric_difference' on HashSets)
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
		// the whole point of this query is to get the agent uuid of other agents, to be able to submit queries as the
		// monitors themselves
		// "agent":  s.agentID,
		"monitor": "true",
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
