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
	"glouton/bleemeo/types"
	"glouton/logger"
	"glouton/version"
	"time"
)

// syncInfo retrieves the minimum supported glouton version the API supports.
func (s *Synchronizer) syncInfo(fullSync bool, onlyEssential bool) error {
	var globalInfo types.GlobalInfo

	statusCode, err := s.client.DoUnauthenticated(s.ctx, "GET", "v1/info/", nil, nil, &globalInfo)
	if err != nil {
		logger.V(2).Printf("Couldn't retrieve global informations, got '%v'", err)
		return nil
	}

	if statusCode >= 300 {
		logger.V(2).Printf("Couldn't retrieve global informations, got HTTP status code %d", statusCode)
		return nil
	}

	if globalInfo.Agents.MinVersions.Glouton != "" {
		if !version.Compare(version.Version, globalInfo.Agents.MinVersions.Glouton) {
			logger.V(0).Printf("Your agent is unsupported, consider upgrading it (got version %s, expected version >= %s)", version.Version, globalInfo.Agents.MinVersions.Glouton)
			s.option.DisableCallback(types.DisableAgentTooOld, s.now().Add(24*time.Hour))

			// force syncing the version again when the synchronizer runs again
			s.forceSync["info"] = true
		}
	}

	if s.option.SetBleemeoInMaintenanceMode != nil {
		s.option.SetBleemeoInMaintenanceMode(globalInfo.MaintenanceEnabled)
	}

	return nil
}

// IsMaintenance returns whether the synchronizer is currently in maintenance mode (not making any request except info/agent).
func (s *Synchronizer) IsMaintenance() bool {
	s.l.Lock()
	defer s.l.Unlock()

	return s.maintenanceMode
}

// SetMaintenance allows to trigger the maintenance mode for the synchronize.
// When running in maintenance mode, only the general infos, the agent and its configuration are synced.
func (s *Synchronizer) SetMaintenance(maintenance bool) {
	if s.IsMaintenance() && !maintenance {
		// getting out of maintenance, let's check for a duplicated state.json file
		err := s.checkDuplicated()
		if err != nil {
			// it's not a critical error at all, we will perform this check again on the next synchronization pass
			logger.V(2).Printf("Couldn't check for duplicated agent: %v", err)
		}
	}

	s.l.Lock()
	defer s.l.Unlock()

	s.maintenanceMode = maintenance
}

// UpdateMaintenance requests to check for the maintenance mode again.
func (s *Synchronizer) UpdateMaintenance() {
	s.l.Lock()
	defer s.l.Unlock()

	s.forceSync["info"] = false
}
