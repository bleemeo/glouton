// Copyright 2015-2025 Bleemeo
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
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/delay"
	"github.com/bleemeo/glouton/logger"
	gloutonTypes "github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/version"

	"github.com/prometheus/prometheus/model/labels"
)

type mqttUpdatePayload struct {
	CurrentStatus     bleemeo.Status `json:"current_status"`
	StatusDescription []string       `json:"status_descriptions"`
}

const mqttUpdateResponseFields = "current_status,status_descriptions"

// syncInfo retrieves the minimum supported glouton version the API supports.
func (s *Synchronizer) syncInfo(ctx context.Context, syncType types.SyncType, execution types.SynchronizationExecution) (updateThresholds bool, err error) {
	_ = syncType

	return s.syncInfoReal(ctx, execution, true)
}

// syncInfoReal retrieves the minimum supported glouton version the API supports.
func (s *Synchronizer) syncInfoReal(ctx context.Context, execution types.SynchronizationExecution, disableOnTimeDrift bool) (updateThresholds bool, err error) {
	apiClient := execution.BleemeoAPIClient()

	globalInfo, err := apiClient.GetGlobalInfo(ctx)
	if err != nil && strings.Contains(err.Error(), "certificate has expired") {
		// This could happen when local time is really too far away from real time.
		// Since this request is unauthenticated, we can retry it with insecure TLS
		transportOpts := &gloutonTypes.CustomTransportOptions{
			UserAgentHeader: version.UserAgent(),
			RequestCounter:  &s.requestCounter,
			EnableLogger:    true,
		}

		insecureClient, cErr := bleemeo.NewClient(
			bleemeo.WithEndpoint(s.option.Config.Bleemeo.APIBase),
			bleemeo.WithHTTPClient(&http.Client{Transport: gloutonTypes.NewHTTPTransport(&tls.Config{InsecureSkipVerify: true}, transportOpts)}), //nolint:gosec
		)
		if cErr != nil {
			return false, cErr
		}

		var respBody []byte

		statusCode, respBody, err := insecureClient.Do(ctx, http.MethodGet, "/v1/info/", nil, false, nil)
		if err == nil && statusCode == 200 {
			err = json.Unmarshal(respBody, &globalInfo)
			if err != nil {
				logger.V(2).Printf("Couldn't unmarshal global information, got '%v'", err)

				return false, nil
			}
		} else if statusCode >= 300 {
			logger.V(2).Printf("Couldn't retrieve global information, got HTTP status code %d", statusCode)

			return false, nil
		}
	}

	if err != nil {
		logger.V(2).Printf("Couldn't retrieve global information, got '%v'", err)

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
			map[string]string{gloutonTypes.LabelName: agentStatusName, gloutonTypes.LabelInstanceUUID: s.agentID},
		)
		if metric, ok := s.option.Cache.MetricLookupFromList()[metricKey]; ok {
			payload := mqttUpdatePayload{
				CurrentStatus:     bleemeo.Status_Critical,
				StatusDescription: []string{"Agent local time too different from actual time"},
			}

			err = apiClient.UpdateMetric(ctx, metric.ID, payload, mqttUpdateResponseFields)
			if err != nil {
				return false, err
			}
		}
	}

	s.lastInfo = globalInfo

	return false, nil
}

func (s *Synchronizer) updateMQTTStatus(ctx context.Context, apiClient types.MetricClient) error {
	s.l.Lock()
	isMQTTConnected := s.isMQTTConnected != nil && *s.isMQTTConnected
	shouldUpdate := s.shouldUpdateMQTTStatus
	s.shouldUpdateMQTTStatus = false
	s.l.Unlock()

	// When the agent is not connected, check whether MQTT is accessible.
	if !shouldUpdate || isMQTTConnected {
		return nil
	}

	mqttAddress := net.JoinHostPort(s.option.Config.Bleemeo.MQTT.Host, strconv.Itoa(s.option.Config.Bleemeo.MQTT.Port))

	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, dialErr := s.dialer.DialContext(timeoutCtx, "tcp", mqttAddress)
	if conn != nil {
		defer conn.Close()
	}

	if dialErr == nil {
		// The agent can access MQTT, nothing to do.
		return nil
	}

	msg := fmt.Sprintf(
		"Agent can't access Bleemeo MQTT on port %d, is the port blocked by a firewall?",
		s.option.Config.Bleemeo.MQTT.Port,
	)

	logger.Printf(msg)

	// Mark the agent_status as disconnected because MQTT is not accessible.
	metricKey := gloutonTypes.LabelsToText(
		map[string]string{gloutonTypes.LabelName: agentStatusName, gloutonTypes.LabelInstanceUUID: s.agentID},
	)
	if metric, ok := s.option.Cache.MetricLookupFromList()[metricKey]; ok {
		payload := mqttUpdatePayload{
			CurrentStatus:     bleemeo.Status_Critical,
			StatusDescription: []string{msg},
		}

		err := apiClient.UpdateMetric(ctx, metric.ID, payload, mqttUpdateResponseFields)
		if err != nil {
			return err
		}
	}

	return nil
}
