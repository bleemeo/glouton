// Copyright 2015-2026 Bleemeo
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

// Package syncinfo reads the global information of Bleemeo (/v1/info/): the maintenance mode,
// the minimum supported agent version and the time of the API. It disables the connector when
// the agent is too old or its time too far off, and marks agent_status critical when the time
// is too far off or MQTT is unreachable.
package syncinfo

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/delay"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/model"
	gloutonTypes "github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/version"

	"github.com/prometheus/prometheus/model/labels"
)

const (
	// outdatedAfter is the age after which the global information is fetched again, when the
	// synchronizer runs anyway.
	outdatedAfter = 30 * time.Minute
	// maintenancePollInterval is the minimum delay between two polls of the maintenance mode,
	// while MQTT isn't connected.
	maintenancePollInterval = 15 * time.Minute
	mqttDialTimeout         = 5 * time.Second

	agentStatusUpdateFields = "current_status,status_descriptions"
)

var errUnexpectedStatus = errors.New("unexpected response")

type agentStatusUpdatePayload struct {
	CurrentStatus     bleemeo.Status `json:"current_status"`
	StatusDescription []string       `json:"status_descriptions"`
}

// SyncInfo fetches the global information of Bleemeo and applies it.
type SyncInfo struct {
	now func() time.Time
	// requestCounter is the synchronizer's request counter, given to the client used when the
	// certificate looks expired.
	requestCounter *atomic.Uint32
	dial           func(ctx context.Context, network, address string) (net.Conn, error)

	l sync.Mutex
	// lastInfo is the global information last applied.
	lastInfo bleemeoTypes.GlobalInfo
	// isMQTTConnected is nil until the MQTT connection state is known.
	isMQTTConnected *bool
	// shouldUpdateMQTTStatus is set when MQTT got disconnected. While it is set, info checks
	// whether MQTT is reachable. It is cleared once MQTT is connected or reachable, or
	// agent_status is marked critical.
	shouldUpdateMQTTStatus bool
	// timeDriftMarked is set once agent_status is marked critical for the time drift, and
	// cleared when the time drift isn't too large anymore.
	timeDriftMarked bool
	// lastMaintenanceSync is when the maintenance mode was last polled. While MQTT isn't
	// connected, no notification tells the end of the maintenance: info polls it every 15
	// minutes. For instance, an agent started during a maintenance can't register agent_status,
	// and MQTT isn't ready without it.
	lastMaintenanceSync time.Time
}

// New returns the entity. requestCounter counts the requests made outside the Bleemeo client.
// now is the synchronizer's clock.
func New(requestCounter *atomic.Uint32, now func() time.Time) *SyncInfo {
	return &SyncInfo{
		now:            now,
		requestCounter: requestCounter,
		dial:           (&net.Dialer{}).DialContext,
	}
}

func (s *SyncInfo) Name() types.EntityName {
	return types.EntityInfo
}

// EnabledInMaintenance returns true: info is how the synchronizer learns the maintenance ended.
func (s *SyncInfo) EnabledInMaintenance() bool {
	return true
}

func (s *SyncInfo) EnabledInSuspendedMode() bool {
	return false
}

// NeedSynchronizationWhileSkipped returns true: the maintenance is polled in suspended mode too,
// so that info runs as soon as the suspension ends.
func (s *SyncInfo) NeedSynchronizationWhileSkipped() bool {
	return true
}

func (s *SyncInfo) PrepareExecution(_ context.Context, execution types.SynchronizationExecution) (types.EntitySynchronizerExecution, error) {
	return &syncInfoExecution{
		parent:    s,
		execution: execution,
	}, nil
}

// LastInfo returns the global information last applied.
func (s *SyncInfo) LastInfo() bleemeoTypes.GlobalInfo {
	s.l.Lock()
	defer s.l.Unlock()

	return s.lastInfo
}

// IsOutdated returns whether the global information should be fetched again, when the
// synchronizer runs anyway. Keeping it recent detects a change on /v1/info/ quickly, and keeps
// the time_drift metric recent enough to avoid its deactivation.
func (s *SyncInfo) IsOutdated() bool {
	s.l.Lock()
	fetchedAt := s.lastInfo.FetchedAt
	s.l.Unlock()

	return s.now().Sub(fetchedAt) > outdatedAfter
}

// SetMQTTConnected records whether MQTT is connected. It returns true when MQTT is reported
// disconnected while it was connected or not reported yet: info should then be synchronized, to
// check whether MQTT is reachable.
func (s *SyncInfo) SetMQTTConnected(isConnected bool) bool {
	s.l.Lock()
	defer s.l.Unlock()

	justDisconnected := (s.isMQTTConnected == nil || *s.isMQTTConnected) && !isConnected
	s.isMQTTConnected = &isConnected

	if justDisconnected {
		s.shouldUpdateMQTTStatus = true
	}

	return justDisconnected
}

// SyncBeforeFirstExecution fetches and applies the global information outside the
// synchronization loop: the MQTT connection depends on the maintenance and on the agent
// version. When disableOnTimeDrift is false, a large time drift doesn't disable the connector:
// a new agent can then register its agent_status, which the next synchronization of info marks.
func (s *SyncInfo) SyncBeforeFirstExecution(ctx context.Context, execution types.SynchronizationExecution, disableOnTimeDrift bool) error {
	globalInfo, ok := s.fetch(ctx, execution)
	if !ok {
		return nil
	}

	return s.apply(ctx, execution, globalInfo, disableOnTimeDrift)
}

func (s *SyncInfo) DiagnosticArchive(_ context.Context, archive gloutonTypes.ArchiveWriter) error {
	file, err := archive.Create("bleemeo-sync-info.json")
	if err != nil {
		return err
	}

	s.l.Lock()
	defer s.l.Unlock()

	obj := struct {
		LastInfo               bleemeoTypes.GlobalInfo
		LastInfoFetchedAt      time.Time
		LastMaintenanceSync    time.Time
		IsMQTTConnected        *bool
		ShouldUpdateMQTTStatus bool
		TimeDriftMarked        bool
	}{
		LastInfo:               s.lastInfo,
		LastInfoFetchedAt:      s.lastInfo.FetchedAt,
		LastMaintenanceSync:    s.lastMaintenanceSync,
		IsMQTTConnected:        s.isMQTTConnected,
		ShouldUpdateMQTTStatus: s.shouldUpdateMQTTStatus,
		TimeDriftMarked:        s.timeDriftMarked,
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

type syncInfoExecution struct {
	parent    *SyncInfo
	execution types.SynchronizationExecution

	// globalInfo is what RefreshCache fetched. fetched is false when it failed.
	globalInfo bleemeoTypes.GlobalInfo
	fetched    bool
}

// NeedSynchronization polls the maintenance mode while MQTT can't tell when it ends.
// Otherwise, info is synchronized on request, and on full synchronizations.
func (e *syncInfoExecution) NeedSynchronization(_ context.Context) (bool, error) {
	if !e.execution.GlobalState().IsMaintenance() || e.execution.Option().IsMqttConnected() {
		return false, nil
	}

	startedAt := e.execution.StartedAt()

	e.parent.l.Lock()
	defer e.parent.l.Unlock()

	if !startedAt.After(e.parent.lastMaintenanceSync.Add(maintenancePollInterval)) {
		return false, nil
	}

	e.parent.lastMaintenanceSync = startedAt

	return true, nil
}

func (e *syncInfoExecution) RefreshCache(ctx context.Context, _ types.SyncType) error {
	e.globalInfo, e.fetched = e.parent.fetch(ctx, e.execution)

	return nil
}

func (e *syncInfoExecution) SyncRemoteAndLocal(ctx context.Context, _ types.SyncType) error {
	if !e.fetched {
		return nil
	}

	return e.parent.apply(ctx, e.execution, e.globalInfo, true)
}

func (e *syncInfoExecution) FinishExecution(_ context.Context) {
}

// fetch returns the global information. It returns false when it couldn't be fetched: the
// failure is only logged, the information will be fetched again later.
func (s *SyncInfo) fetch(ctx context.Context, execution types.SynchronizationExecution) (bleemeoTypes.GlobalInfo, bool) {
	globalInfo, err := execution.BleemeoAPIClient().GetGlobalInfo(ctx)
	if err != nil && strings.Contains(err.Error(), "certificate has expired") {
		// A valid certificate looks expired when the local time is far off. The request is
		// unauthenticated, so it is sent again without checking the certificate.
		globalInfo, err = fetchGlobalInfoInsecure(ctx, execution.Option().Config.Bleemeo.APIBase, s.requestCounter)
	}

	if err != nil {
		logger.V(2).Printf("Couldn't retrieve global information, got '%v'", err)

		return bleemeoTypes.GlobalInfo{}, false
	}

	globalInfo.FetchedAt = s.now()

	return globalInfo, true
}

// fetchGlobalInfoInsecure returns the global information of the API at apiBase, without
// checking its certificate. requestCounter counts the request.
func fetchGlobalInfoInsecure(ctx context.Context, apiBase string, requestCounter *atomic.Uint32) (bleemeoTypes.GlobalInfo, error) {
	transportOpts := &gloutonTypes.CustomTransportOptions{
		UserAgentHeader: version.UserAgent(),
		RequestCounter:  requestCounter,
		EnableLogger:    true,
	}

	// A plain HTTP client: the Bleemeo client can't be created without credentials.
	client := &http.Client{Transport: gloutonTypes.NewHTTPTransport(&tls.Config{InsecureSkipVerify: true}, transportOpts)} //nolint:gosec

	infoURL, err := url.JoinPath(apiBase, "/v1/info/")
	if err != nil {
		return bleemeoTypes.GlobalInfo{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, infoURL, nil)
	if err != nil {
		return bleemeoTypes.GlobalInfo{}, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return bleemeoTypes.GlobalInfo{}, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return bleemeoTypes.GlobalInfo{}, fmt.Errorf("%w: HTTP status code %d", errUnexpectedStatus, resp.StatusCode)
	}

	var globalInfo bleemeoTypes.GlobalInfo

	if err := json.NewDecoder(resp.Body).Decode(&globalInfo); err != nil {
		return bleemeoTypes.GlobalInfo{}, err
	}

	return globalInfo, nil
}

// apply disables the connector when the agent is too old or its time too far off, checks
// whether MQTT is reachable, sets the maintenance mode, pushes the time_drift metric and marks
// agent_status critical for the time drift.
func (s *SyncInfo) apply(ctx context.Context, execution types.SynchronizationExecution, globalInfo bleemeoTypes.GlobalInfo, disableOnTimeDrift bool) error {
	option := execution.Option()
	apiClient := execution.BleemeoAPIClient()

	var agentID string
	if option.State != nil {
		agentID, _ = option.State.BleemeoCredentials()
	}

	if globalInfo.Agents.MinVersions.Glouton != "" && !version.Compare(version.Version, globalInfo.Agents.MinVersions.Glouton) {
		logger.V(0).Printf("Your agent is unsupported, consider upgrading it (got version %s, expected version >= %s)", version.Version, globalInfo.Agents.MinVersions.Glouton)
		option.DisableCallback(bleemeoTypes.DisableAgentTooOld, s.now().Add(delay.JitterDelay(24*time.Hour, 0.1)))

		// Check the version again on the next execution, once the connector is enabled again.
		execution.RequestSynchronization(types.EntityInfo, true)
	}

	if err := s.updateMQTTStatus(ctx, apiClient, option.Cache, agentID, option.Config.Bleemeo.MQTT); err != nil {
		return err
	}

	if option.SetBleemeoInMaintenanceMode != nil {
		option.SetBleemeoInMaintenanceMode(ctx, globalInfo.MaintenanceEnabled)
	}

	if globalInfo.CurrentTime != 0 {
		pushTimeDrift(option.PushAppender, globalInfo)

		if disableOnTimeDrift && globalInfo.IsTimeDriftTooLarge() {
			option.DisableCallback(bleemeoTypes.DisableTimeDrift, s.now().Add(delay.JitterDelay(30*time.Minute, 0.1)))

			// Check the time again on the next execution, once the connector is enabled again.
			execution.RequestSynchronization(types.EntityInfo, true)
		}
	}

	if err := s.updateTimeDriftStatus(ctx, apiClient, option.Cache, agentID, globalInfo.IsTimeDriftTooLarge()); err != nil {
		return err
	}

	s.l.Lock()
	s.lastInfo = globalInfo
	s.l.Unlock()

	return nil
}

// updateTimeDriftStatus marks agent_status as critical once while the time drift is too large.
// Until agent_status is registered, there is nothing to mark: it is marked on the next call
// after its registration.
func (s *SyncInfo) updateTimeDriftStatus(ctx context.Context, apiClient types.MetricClient, c *cache.Cache, agentID string, tooLarge bool) error {
	s.l.Lock()
	marked := s.timeDriftMarked

	if !tooLarge {
		s.timeDriftMarked = false
	}
	s.l.Unlock()

	if !tooLarge || marked {
		return nil
	}

	marked, err := markAgentStatusCritical(ctx, apiClient, c, agentID, "Agent local time too different from actual time")
	if err != nil {
		return err
	}

	s.l.Lock()
	s.timeDriftMarked = marked
	s.l.Unlock()

	return nil
}

// updateMQTTStatus marks agent_status as critical when MQTT got disconnected and its address
// isn't reachable.
func (s *SyncInfo) updateMQTTStatus(ctx context.Context, apiClient types.MetricClient, c *cache.Cache, agentID string, mqttConfig config.BleemeoMQTT) error {
	s.l.Lock()
	isMQTTConnected := s.isMQTTConnected != nil && *s.isMQTTConnected
	shouldUpdate := s.shouldUpdateMQTTStatus

	if isMQTTConnected {
		s.shouldUpdateMQTTStatus = false
	}
	s.l.Unlock()

	if !shouldUpdate || isMQTTConnected {
		return nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, mqttDialTimeout)
	defer cancel()

	mqttAddress := net.JoinHostPort(mqttConfig.Host, strconv.Itoa(mqttConfig.Port))

	conn, dialErr := s.dial(timeoutCtx, "tcp", mqttAddress)
	if conn != nil {
		defer conn.Close()
	}

	if dialErr == nil {
		// The agent can access MQTT, nothing to do.
		s.l.Lock()
		s.shouldUpdateMQTTStatus = false
		s.l.Unlock()

		return nil
	}

	msg := fmt.Sprintf("Agent can't access Bleemeo MQTT on port %d, is the port blocked by a firewall?", mqttConfig.Port)

	logger.Printf(msg)

	// Until agent_status is marked, each synchronization of info checks MQTT again: agent_status
	// may not be registered yet, or the update may fail.
	marked, err := markAgentStatusCritical(ctx, apiClient, c, agentID, msg)
	if marked {
		s.l.Lock()
		s.shouldUpdateMQTTStatus = false
		s.l.Unlock()
	}

	return err
}

// pushTimeDrift writes the time_drift metric: the difference between the local time and the
// time of the API.
func pushTimeDrift(appender *model.BufferAppender, globalInfo bleemeoTypes.GlobalInfo) {
	_, err := appender.Append(
		0,
		labels.FromMap(map[string]string{
			gloutonTypes.LabelName: "time_drift",
		}),
		globalInfo.BleemeoTime().Truncate(time.Second).UnixMilli(),
		globalInfo.TimeDrift().Seconds(),
	)
	if err != nil {
		logger.V(2).Printf("unable to append time_drift to PushAppender")
	}

	err = appender.Commit()
	if err != nil {
		logger.V(2).Printf("unable to commit on PushAppender")
	}
}

// markAgentStatusCritical sets the agent_status of the agent agentID to critical, with the
// given description. It returns false when agent_status isn't registered yet.
func markAgentStatusCritical(ctx context.Context, apiClient types.MetricClient, c *cache.Cache, agentID string, description string) (bool, error) {
	metricKey := gloutonTypes.LabelsToText(
		map[string]string{gloutonTypes.LabelName: types.AgentStatusName, gloutonTypes.LabelInstanceUUID: agentID},
	)

	metric, ok := c.MetricLookupFromList()[metricKey]
	if !ok {
		return false, nil
	}

	payload := agentStatusUpdatePayload{
		CurrentStatus:     bleemeo.Status_Critical,
		StatusDescription: []string{description},
	}

	if err := apiClient.UpdateMetric(ctx, metric.ID, payload, agentStatusUpdateFields); err != nil {
		return false, err
	}

	return true, nil
}
