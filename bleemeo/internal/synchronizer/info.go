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
	"context"
	"encoding/json"
	"glouton/bleemeo/types"
	"glouton/logger"
	"glouton/version"
	"net/http"
	"time"
)

// syncInfo retrieves the minimum supported glouton version the API supports.
func (s *Synchronizer) syncInfo(fullSync bool) error {
	var globalInfo types.GlobalInfo

	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	req, err := s.client.PrepareRequest("GET", "v1/info/", nil, nil)
	if err != nil {
		logger.V(2).Printf("Couldn't preprate the requestto retrieve global informations, got '%v'", err)
		return nil
	}

	res, err := http.DefaultClient.Do(req.WithContext(ctx))
	// maybe the API does not support this version reporting ? We do not consider this an error for the moment
	if err != nil {
		logger.V(2).Printf("Couldn't retrieve global informations, got error '%v'", err)
		return nil
	}

	defer res.Body.Close()

	if res.StatusCode >= 300 {
		logger.V(2).Printf("Couldn't retrieve global informations, got HTTP status code %d", res.StatusCode)
		return nil
	}

	err = json.NewDecoder(res.Body).Decode(&globalInfo)
	if err != nil {
		logger.V(2).Printf("Couldn't retrieve global informations, decoding failed with error '%v'", err)
		return nil
	}

	if globalInfo.Agents.MinVersions.Glouton != "" {
		if !version.Compare(version.Version, globalInfo.Agents.MinVersions.Glouton) {
			logger.V(0).Printf("Your agent is unsupported, consider upgrading it (got version %s, expected version >= %s)", version.Version, globalInfo.Agents.MinVersions.Glouton)
			s.option.DisableCallback(types.DisableAgentTooOld, time.Now().Add(24*time.Hour))

			// force syncing the version again when the synchronizer runs again
			s.forceSync["info"] = true
		}
	}

	return nil
}
