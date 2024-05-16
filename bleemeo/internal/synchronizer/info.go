// Copyright 2015-2023 Bleemeo
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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bleemeo/bleemeo-go"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/delay"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/version"

	"github.com/prometheus/prometheus/model/labels"
)

type mqttUpdatePayload struct {
	CurrentStatus     int      `json:"current_status"`
	StatusDescription []string `json:"status_descriptions"`
}

const mqttUpdateResponseFields = "current_status,status_descriptions"

// syncInfo retrieves the minimum supported glouton version the API supports.
func (s *Synchronizer) syncInfo(ctx context.Context, _ bool, onlyEssential bool) (updateThresholds bool, err error) {
	_ = onlyEssential

	return s.syncInfoReal(ctx, true)
}

// syncInfoReal retrieves the minimum supported glouton version the API supports.
func (s *Synchronizer) syncInfoReal(ctx context.Context, disableOnTimeDrift bool) (updateThresholds bool, err error) {
	statusCode, respBody, err := s.realClient.Do(ctx, http.MethodGet, "/v1/info/", nil, false, nil)
	if err != nil && strings.Contains(err.Error(), "certificate has expired") {
		// This could happen when local time is really too far away from real time.
		// Since this request is unauthenticated, we can retry it with insecure TLS
		insecureClient, cErr := bleemeo.NewClient(
			bleemeo.WithEndpoint(s.option.Config.Bleemeo.APIBase),
			bleemeo.WithHTTPClient(&http.Client{Transport: types.NewHTTPTransport(&tls.Config{InsecureSkipVerify: true}, &s.requestCounter)}), //nolint:gosec
		)
		if cErr != nil {
			return false, cErr
		}

		statusCode, respBody, err = insecureClient.Do(ctx, http.MethodGet, "/v1/info/", nil, false, nil)
	}

	if err != nil {
		logger.V(2).Printf("Couldn't retrieve global information, got '%v'", err)

		return false, nil
	}

	var globalInfo bleemeoTypes.GlobalInfo

	err = json.Unmarshal(respBody, &globalInfo)
	if err != nil {
		logger.V(2).Printf("Couldn't unmarshal global information, got '%v'", err)

		return false, nil
	}

	if statusCode >= 300 {
		logger.V(2).Printf("Couldn't retrieve global information, got HTTP status code %d", statusCode)

		return false, nil
	}

	globalInfo.FetchedAt = s.now()

	if globalInfo.Agents.MinVersions.Glouton != "" {
		if !version.Compare(version.Version, globalInfo.Agents.MinVersions.Glouton) {
			delay := delay.JitterDelay(24*time.Hour, 0.1)

			logger.V(0).Printf("Your agent is unsupported, consider upgrading it (got version %s, expected version >= %s)", version.Version, globalInfo.Agents.MinVersions.Glouton)
			s.option.DisableCallback(bleemeoTypes.DisableAgentTooOld, s.now().Add(delay))

			// force syncing the version again when the synchronizer runs again
			s.l.Lock()
			s.forceSync[syncMethodInfo] = true
			s.l.Unlock()
		}
	}

	err = s.updateMQTTStatus()
	if err != nil {
		return false, err
	}

	if s.option.SetBleemeoInMaintenanceMode != nil {
		s.option.SetBleemeoInMaintenanceMode(globalInfo.MaintenanceEnabled)
	}

	if globalInfo.CurrentTime != 0 {
		delta := globalInfo.TimeDrift()

		_, err := s.option.PushAppender.Append(
			0,
			labels.FromMap(map[string]string{
				types.LabelName: "time_drift",
			}),
			globalInfo.BleemeoTime().Truncate(time.Second).UnixMilli(),
			delta.Seconds(),
		)
		if err != nil {
			logger.V(2).Printf("unable to append time_drift to PushAppender")
		}

		err = s.option.PushAppender.Commit()
		if err != nil {
			logger.V(2).Printf("unable to commit on PushAppender")
		}

		if disableOnTimeDrift && globalInfo.IsTimeDriftTooLarge() {
			delay := delay.JitterDelay(30*time.Minute, 0.1)
			s.option.DisableCallback(bleemeoTypes.DisableTimeDrift, s.now().Add(delay))

			// force syncing the version again when the synchronizer runs again
			s.l.Lock()
			s.forceSync[syncMethodInfo] = true
			s.l.Unlock()
		}
	}

	s.l.Lock()
	defer s.l.Unlock()

	if globalInfo.IsTimeDriftTooLarge() && !s.lastInfo.IsTimeDriftTooLarge() {
		// Mark the agent_status as disconnected with reason being the time drift.
		metricKey := types.LabelsToText(
			map[string]string{types.LabelName: "agent_status", types.LabelInstanceUUID: s.agentID},
		)
		if metric, ok := s.option.Cache.MetricLookupFromList()[metricKey]; ok {
			payload := mqttUpdatePayload{
				CurrentStatus:     2, // critical
				StatusDescription: []string{"Agent local time too different from actual time"},
			}

			err = s.client.Update(ctx, bleemeo.ResourceMetric, metric.ID, payload, mqttUpdateResponseFields, nil)
			if err != nil {
				return false, err
			}
		}
	}

	s.lastInfo = globalInfo

	return false, nil
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
		err := s.checkDuplicated(s.ctx)
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

	s.forceSync[syncMethodInfo] = false
}

func (s *Synchronizer) updateMQTTStatus() error {
	s.l.Lock()
	isMQTTConnected := s.isMQTTConnected != nil && *s.isMQTTConnected
	shouldUpdate := s.shouldUpdateMQTTStatus
	s.shouldUpdateMQTTStatus = false
	s.l.Unlock()

	if !shouldUpdate || isMQTTConnected {
		return nil
	}

	// When the agent is not connected check whether MQTT is accessible.
	if !isMQTTConnected {
		mqttAddress := net.JoinHostPort(s.option.Config.Bleemeo.MQTT.Host, strconv.Itoa(s.option.Config.Bleemeo.MQTT.Port))

		conn, dialErr := net.DialTimeout("tcp", mqttAddress, 5*time.Second)
		if conn != nil {
			defer conn.Close()
		}

		if dialErr == nil {
			// The agent can access MQTT, nothing to do.
			return nil
		}
	}

	msg := fmt.Sprintf(
		"Agent can't access Bleemeo MQTT on port %d, is the port blocked by a firewall?",
		s.option.Config.Bleemeo.MQTT.Port,
	)

	logger.Printf(msg)

	// Mark the agent_status as disconnected because MQTT is not accessible.
	metricKey := types.LabelsToText(
		map[string]string{types.LabelName: "agent_status", types.LabelInstanceUUID: s.agentID},
	)
	if metric, ok := s.option.Cache.MetricLookupFromList()[metricKey]; ok {
		payload := mqttUpdatePayload{
			CurrentStatus:     2, // critical
			StatusDescription: []string{msg},
		}

		err := s.client.Update(s.ctx, bleemeo.ResourceMetric, metric.ID, payload, mqttUpdateResponseFields, nil)
		if err != nil {
			return err
		}
	}

	return nil
}
