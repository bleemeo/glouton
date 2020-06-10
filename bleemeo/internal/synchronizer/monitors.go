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
)

// update the list of metrics that must be watched locally (must be declared both on the bleemeo API
// AND in the config file for now).
// TODO: caching
// FIXME: we are ignoring the notion of modules, and we shouldn't.
func (s *Synchronizer) syncMonitors(fullSync bool) error {
	params := map[string]string{
		// the whole point of this query is to get the agent uuid, to be able to submit queries as the
		// monitor itself
		// "agent":  s.agentID,
		"monitor": "true",
		"fields":  "id,agent,monitor_url",
	}

	result, err := s.client.Iter("service", params)
	if err != nil {
		return err
	}

	monitors := make([]bleemeoTypes.Monitor, 0, len(result))

	for _, jsonMessage := range result {
		var monitor bleemeoTypes.Monitor

		if err := json.Unmarshal(jsonMessage, &monitor); err != nil {
			return fmt.Errorf("Couldn't parse monitor %v", jsonMessage)
		}

		monitors = append(monitors, monitor)
	}
	s.option.Cache.SetMonitors(monitors)

	return nil
}
