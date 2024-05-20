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
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/delay"
	"github.com/bleemeo/glouton/logger"
	gloutonTypes "github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/version"

	"github.com/prometheus/prometheus/model/labels"
)

// syncInfo retrieves the minimum supported glouton version the API supports.
func (s *Synchronizer) syncInfo(ctx context.Context, syncType types.SyncType, execution types.SynchronizationExecution) (updateThresholds bool, err error) {
	_ = syncType

	return s.syncInfoReal(ctx, execution, true)
}

// syncInfoReal retrieves the minimum supported glouton version the API supports.
func (s *Synchronizer) syncInfoReal(ctx context.Context, execution types.SynchronizationExecution, disableOnTimeDrift bool) (updateThresholds bool, err error) {
	var globalInfo bleemeoTypes.GlobalInfo

	apiClient := execution.BleemeoAPIClient()

	statusCode, err := s.realClient.DoUnauthenticated(ctx, "GET", "v1/info/", nil, nil, &globalInfo)
	if err != nil && strings.Contains(err.Error(), "certificate has expired") {
		// This could happen when local time is really to far away from real time.
		// Since this request is unauthenticated we retry it with insecure TLS
		statusCode, err = s.realClient.DoTLSInsecure(ctx, "GET", "v1/info/", nil, nil, &globalInfo)
	}

	if err != nil {
		logger.V(2).Printf("Couldn't retrieve global information, got '%v'", err)

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
			execution.RequestSynchronization(types.EntityInfo, true)
		}
	}

	err = s.updateMQTTStatus(ctx, apiClient)
	if err != nil {
		return false, err
	}

	if s.option.SetBleemeoInMaintenanceMode != nil {
		s.option.SetBleemeoInMaintenanceMode(ctx, globalInfo.MaintenanceEnabled)
	}

	if globalInfo.CurrentTime != 0 {
		delta := globalInfo.TimeDrift()

		_, err := s.option.PushAppender.Append(
			0,
			labels.FromMap(map[string]string{
				gloutonTypes.LabelName: "time_drift",
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
			execution.RequestSynchronization(types.EntityInfo, true)
		}
	}

	s.l.Lock()
	defer s.l.Unlock()

	if globalInfo.IsTimeDriftTooLarge() && !s.lastInfo.IsTimeDriftTooLarge() {
		// Mark the agent_status as disconnected with reason being the time drift.
		metricKey := gloutonTypes.LabelsToText(
			map[string]string{gloutonTypes.LabelName: "agent_status", gloutonTypes.LabelInstanceUUID: s.agentID},
		)
		if metric, ok := s.option.Cache.MetricLookupFromList()[metricKey]; ok {
			type payloadType struct {
				CurrentStatus     int      `json:"current_status"`
				StatusDescription []string `json:"status_descriptions"`
			}

			_, err := apiClient.Do(
				ctx,
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
				return false, err
			}
		}
	}

	s.lastInfo = globalInfo

	return false, nil
}

func (s *Synchronizer) updateMQTTStatus(ctx context.Context, apiClient types.RawClient) error {
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
	metricKey := gloutonTypes.LabelsToText(
		map[string]string{gloutonTypes.LabelName: "agent_status", gloutonTypes.LabelInstanceUUID: s.agentID},
	)
	if metric, ok := s.option.Cache.MetricLookupFromList()[metricKey]; ok {
		type payloadType struct {
			CurrentStatus     int      `json:"current_status"`
			StatusDescription []string `json:"status_descriptions"`
		}

		_, err := apiClient.Do(
			ctx,
			"PATCH",
			fmt.Sprintf("v1/metric/%s/", metric.ID),
			map[string]string{"fields": "current_status,status_descriptions"},
			payloadType{
				CurrentStatus:     2, // critical
				StatusDescription: []string{msg},
			},
			nil,
		)
		if err != nil {
			return err
		}
	}

	return nil
}
