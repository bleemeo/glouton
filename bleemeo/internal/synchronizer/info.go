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
	"fmt"
	"glouton/bleemeo/internal/common"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/logger"
	"glouton/types"
	"glouton/version"
	"time"
)

// syncInfo retrieves the minimum supported glouton version the API supports.
func (s *Synchronizer) syncInfo(full bool, onlyEssential bool) error {
	return s.syncInfoReal(true)
}

// syncInfoReal retrieves the minimum supported glouton version the API supports.
func (s *Synchronizer) syncInfoReal(disableOnTimeDrift bool) error {
	var globalInfo bleemeoTypes.GlobalInfo

	statusCode, err := s.client.DoUnauthenticated(s.ctx, "GET", "v1/info/", nil, nil, &globalInfo)
	if err != nil {
		logger.V(2).Printf("Couldn't retrieve global informations, got '%v'", err)
		return nil
	}

	if statusCode >= 300 {
		logger.V(2).Printf("Couldn't retrieve global informations, got HTTP status code %d", statusCode)
		return nil
	}

	globalInfo.FetchedAt = s.now()

	if globalInfo.Agents.MinVersions.Glouton != "" {
		if !version.Compare(version.Version, globalInfo.Agents.MinVersions.Glouton) {
			delay := common.JitterDelay(24*time.Hour.Seconds(), 0.1, 24*time.Hour.Seconds())

			logger.V(0).Printf("Your agent is unsupported, consider upgrading it (got version %s, expected version >= %s)", version.Version, globalInfo.Agents.MinVersions.Glouton)
			s.option.DisableCallback(bleemeoTypes.DisableAgentTooOld, s.now().Add(delay))

			// force syncing the version again when the synchronizer runs again
			s.l.Lock()
			s.forceSync["info"] = true
			s.l.Unlock()
		}
	}

	if s.option.SetBleemeoInMaintenanceMode != nil {
		s.option.SetBleemeoInMaintenanceMode(globalInfo.MaintenanceEnabled)
	}

	if globalInfo.CurrentTime != 0 {
		delta := globalInfo.TimeDrift()

		s.option.Acc.AddFields("", map[string]interface{}{"time_drift": delta.Seconds()}, nil, s.now().Truncate(time.Second))

		if disableOnTimeDrift && globalInfo.IsTimeDriftTooLarge() {
			delay := common.JitterDelay(30*time.Minute.Seconds(), 0.1, 30*time.Minute.Seconds())
			s.option.DisableCallback(bleemeoTypes.DisableTimeDrift, s.now().Add(delay))

			// force syncing the version again when the synchronizer runs again
			s.l.Lock()
			s.forceSync["info"] = true
			s.l.Unlock()
		}
	}

	s.l.Lock()
	defer s.l.Unlock()

	if globalInfo.IsTimeDriftTooLarge() && !s.lastInfo.IsTimeDriftTooLarge() {
		// Mark the agent_status as disconnecte with reason being the time drift
		metricKey := common.LabelsToText(
			map[string]string{types.LabelName: "agent_status"},
			types.MetricAnnotations{},
			s.option.MetricFormat == types.MetricFormatBleemeo,
		)
		if metric, ok := s.option.Cache.MetricLookupFromList()[metricKey]; ok {
			type payloadType struct {
				CurrentStatus     int      `json:"current_status"`
				StatusDescription []string `json:"status_descriptions"`
			}

			_, err := s.client.Do(
				s.ctx,
				"PATCH",
				fmt.Sprintf("v1/metric/%s/", metric.ID),
				map[string]string{"fields": "current_status,status_descriptions"},
				payloadType{
					CurrentStatus:     2, // critical
					StatusDescription: []string{"Agent local time too different from actual time"},
				},
				nil,
			)
			if err != nil {
				return err
			}
		}
	}

	s.lastInfo = globalInfo

	return nil
}

// IsMaintenance returns whether the synchronizer is currently in maintenance mode (not making any request except info/agent).
func (s *Synchronizer) IsMaintenance() bool {
	s.l.Lock()
	defer s.l.Unlock()

	return s.maintenanceMode
}

// IsTimeDriftTooLarge returns whether the local time it too wrong and Bleemeo connection should be disabled.
func (s *Synchronizer) IsTimeDriftTooLarge() bool {
	s.l.Lock()
	defer s.l.Unlock()

	return s.lastInfo.IsTimeDriftTooLarge()
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
