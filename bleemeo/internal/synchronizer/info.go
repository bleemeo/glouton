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

func (s *Synchronizer) syncInfo(fullSync bool) error {
	// retrieve the min supported glouton version the API supports
	var globalInfo types.GlobalInfo

	_, err := s.client.Do("GET", "v1/info/", nil, nil, &globalInfo)
	// maybe the API does not support this version reporting ? We do not consider this an error for the moment
	if err != nil {
		logger.V(2).Printf("Couldn't retrieve global informations, got '%v'", err)
		return nil
	}

	if globalInfo.Agents.MinVersions.Glouton != "" {
		if version.Compare(version.Version, globalInfo.Agents.MinVersions.Glouton) {
			// the agent is recent enought, let's clear the state about it
			_ = s.option.GlobalOption.State.Set(types.StateEntryAgentTooOld, false)
		} else {
			logger.V(0).Printf("Your agent is unsupported, consider upgrading it (got version %s, expected version >= %s)", version.Version, globalInfo.Agents.MinVersions.Glouton)
			s.option.DisableCallback(types.DisableAgentTooOld, time.Now().Add(24*time.Hour))

			// force syncing the version again when the synchronizer runs again
			s.forceSync["info"] = true
			// we also force the agent sync, because when we start the agent and it was disabled
			// previously because its version was obsolete, we switch on maintenance mode. Tha
			// allows us to check almost immdiately for the version, instead of disabling the
			// bleemeo connector for a whole day like what we do here. The flipside is that we
			// must also sync the agent to get out of the maintenance mode.
			s.forceSync["agent"] = true

			// let's store that this is an old agent, and make sure that we remember it on next startup
			_ = s.option.GlobalOption.State.Set(types.StateEntryAgentTooOld, true)
		}
	}

	return nil
}
