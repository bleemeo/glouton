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
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
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

var (
	errUnexpectedStatus = errors.New("unexpected response")
	errNoCertificate    = errors.New("no certificate")
	errNoCommonValidity = errors.New("the certificates are never valid at the same time")
)

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
	// rootCAs verifies the certificate of the API when it looks expired. nil uses the system
	// roots.
	rootCAs *x509.CertPool
	dial    func(ctx context.Context, network, address string) (net.Conn, error)

	l sync.Mutex
	// lastInfo is the global information last applied.
	lastInfo bleemeoTypes.GlobalInfo
	// mqttDisconnected is whether MQTT was last reported disconnected.
	mqttDisconnected bool
	// shouldUpdateMQTTStatus is set when MQTT got disconnected. While it is set, each
	// synchronization of info checks whether MQTT is reachable, once agent_status is registered.
	// It is cleared once MQTT is connected or reachable, or agent_status is marked critical.
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

// NeedSynchronizationWhileSkipped polls the maintenance like NeedSynchronization: the
// maintenance is polled in suspended mode too, so that info runs as soon as the suspension ends.
func (s *SyncInfo) NeedSynchronizationWhileSkipped(_ context.Context, execution types.SynchronizationExecution) bool {
	return s.needMaintenancePoll(execution)
}

// NeedSynchronizationWithOthers returns whether info is outdated: it is then synchronized when
// the execution synchronizes other entities.
func (s *SyncInfo) NeedSynchronizationWithOthers() bool {
	return s.isOutdated()
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

// isOutdated returns whether the global information should be fetched again, when the
// synchronizer runs anyway. Keeping it recent detects a change on /v1/info/ quickly, and keeps
// the time_drift metric recent enough to avoid its deactivation.
func (s *SyncInfo) isOutdated() bool {
	s.l.Lock()
	fetchedAt := s.lastInfo.FetchedAt
	s.l.Unlock()

	return s.now().Sub(fetchedAt) > outdatedAfter
}

// SetMQTTConnected records whether MQTT is connected. It returns true when MQTT is reported
// disconnected while it was connected or not reported yet: info should then be synchronized, to
// check whether MQTT is reachable. Reporting MQTT connected ends that check.
func (s *SyncInfo) SetMQTTConnected(isConnected bool) bool {
	s.l.Lock()
	defer s.l.Unlock()

	justDisconnected := !s.mqttDisconnected && !isConnected
	s.mqttDisconnected = !isConnected

	switch {
	case isConnected:
		s.shouldUpdateMQTTStatus = false
	case justDisconnected:
		s.shouldUpdateMQTTStatus = true
	}

	return justDisconnected
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
		MQTTDisconnected       bool
		ShouldUpdateMQTTStatus bool
		TimeDriftMarked        bool
	}{
		LastInfo:               s.lastInfo,
		LastInfoFetchedAt:      s.lastInfo.FetchedAt,
		LastMaintenanceSync:    s.lastMaintenanceSync,
		MQTTDisconnected:       s.mqttDisconnected,
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
// Otherwise, info is synchronized on request, on full synchronizations, and with other entities
// when it is outdated.
func (e *syncInfoExecution) NeedSynchronization(_ context.Context) (bool, error) {
	return e.parent.needMaintenancePoll(e.execution), nil
}

func (e *syncInfoExecution) RefreshCache(ctx context.Context, _ types.SyncType) error {
	e.globalInfo, e.fetched = e.parent.fetch(ctx, e.execution)

	return nil
}

func (e *syncInfoExecution) SyncRemoteAndLocal(ctx context.Context, _ types.SyncType) error {
	if !e.fetched {
		return nil
	}

	return e.parent.apply(ctx, e.execution, e.globalInfo)
}

func (e *syncInfoExecution) FinishExecution(_ context.Context) {
}

// needMaintenancePoll returns whether the maintenance should be polled: while in maintenance
// and MQTT isn't connected, every 15 minutes. It records the poll.
func (s *SyncInfo) needMaintenancePoll(execution types.SynchronizationExecution) bool {
	if !execution.GlobalState().IsMaintenance() || execution.Option().IsMqttConnected() {
		return false
	}

	startedAt := execution.StartedAt()

	s.l.Lock()
	defer s.l.Unlock()

	if !startedAt.After(s.lastMaintenanceSync.Add(maintenancePollInterval)) {
		return false
	}

	s.lastMaintenanceSync = startedAt

	return true
}

// fetch returns the global information. It returns false when it couldn't be fetched: the
// failure is only logged, the information will be fetched again later.
func (s *SyncInfo) fetch(ctx context.Context, execution types.SynchronizationExecution) (bleemeoTypes.GlobalInfo, bool) {
	globalInfo, err := execution.BleemeoAPIClient().GetGlobalInfo(ctx)

	var certificateErr x509.CertificateInvalidError
	if errors.As(err, &certificateErr) && certificateErr.Reason == x509.Expired {
		// A valid certificate looks expired, or not yet valid, when the local time is far off.
		// The request is unauthenticated, so it is sent again, checking the certificate at a
		// time when it is valid.
		globalInfo, err = fetchGlobalInfoIgnoringTime(ctx, execution.Option().Config.Bleemeo.APIBase, s.rootCAs, s.requestCounter)
	}

	if err != nil {
		logger.V(2).Printf("Couldn't retrieve global information, got '%v'", err)

		return bleemeoTypes.GlobalInfo{}, false
	}

	globalInfo.FetchedAt = s.now()

	return globalInfo, true
}

// fetchGlobalInfoIgnoringTime returns the global information of the API at apiBase. The
// certificate of the API is verified at a time when it is valid, not at the local time.
// rootCAs, when not nil, replaces the system roots. requestCounter counts the request.
func fetchGlobalInfoIgnoringTime(ctx context.Context, apiBase string, rootCAs *x509.CertPool, requestCounter *atomic.Uint32) (bleemeoTypes.GlobalInfo, error) {
	baseURL, err := url.Parse(apiBase)
	if err != nil {
		return bleemeoTypes.GlobalInfo{}, err
	}

	infoURL := baseURL.JoinPath("/v1/info/").String()
	hostname := baseURL.Hostname()

	transportOpts := &gloutonTypes.CustomTransportOptions{
		UserAgentHeader: version.UserAgent(),
		RequestCounter:  requestCounter,
		EnableLogger:    true,
	}
	tlsConfig := &tls.Config{
		// The default verification uses the local time: VerifyConnection verifies the
		// certificate instead.
		InsecureSkipVerify: true, //nolint:gosec
		VerifyConnection: func(state tls.ConnectionState) error {
			return verifyIgnoringTime(state.PeerCertificates, hostname, rootCAs)
		},
	}
	// A plain HTTP client: the Bleemeo client can't be created without credentials.
	client := &http.Client{Transport: gloutonTypes.NewHTTPTransport(tlsConfig, transportOpts)}
	defer client.CloseIdleConnections()

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

// verifyIgnoringTime verifies the certificate chain sent by the server for hostname, at a time
// when all the certificates of the chain are valid. rootCAs, when not nil, replaces the system
// roots.
func verifyIgnoringTime(certificates []*x509.Certificate, hostname string, rootCAs *x509.CertPool) error {
	if len(certificates) == 0 {
		return errNoCertificate
	}

	leaf := certificates[0]
	notBefore, notAfter := leaf.NotBefore, leaf.NotAfter
	intermediates := x509.NewCertPool()

	for _, cert := range certificates[1:] {
		intermediates.AddCert(cert)

		if cert.NotBefore.After(notBefore) {
			notBefore = cert.NotBefore
		}

		if cert.NotAfter.Before(notAfter) {
			notAfter = cert.NotAfter
		}
	}

	if notAfter.Before(notBefore) {
		return errNoCommonValidity
	}

	_, err := leaf.Verify(x509.VerifyOptions{
		DNSName:       hostname,
		Intermediates: intermediates,
		Roots:         rootCAs,
		CurrentTime:   notBefore.Add(notAfter.Sub(notBefore) / 2),
	})

	return err
}

// apply disables the connector when the agent is too old or its time too far off, checks
// whether MQTT is reachable, sets the maintenance mode, pushes the time_drift metric and marks
// agent_status critical for the time drift. A failed update of agent_status doesn't stop the
// rest: its error is returned at the end, and the update is done again on the next
// synchronization of info.
func (s *SyncInfo) apply(ctx context.Context, execution types.SynchronizationExecution, globalInfo bleemeoTypes.GlobalInfo) error {
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

	mqttErr := s.updateMQTTStatus(ctx, apiClient, option.Cache, agentID, option.Config.Bleemeo.MQTT)

	if option.SetBleemeoInMaintenanceMode != nil {
		option.SetBleemeoInMaintenanceMode(ctx, globalInfo.MaintenanceEnabled)
	}

	if globalInfo.CurrentTime != 0 {
		pushTimeDrift(option.PushAppender, globalInfo)

		// Until the agent is registered, a large time drift doesn't disable the connector: the
		// agent registers its agent_status first, which info then marks.
		if agentID != "" && globalInfo.IsTimeDriftTooLarge() {
			option.DisableCallback(bleemeoTypes.DisableTimeDrift, s.now().Add(delay.JitterDelay(30*time.Minute, 0.1)))

			// Check the time again on the next execution, once the connector is enabled again.
			execution.RequestSynchronization(types.EntityInfo, true)
		}
	}

	timeDriftErr := s.updateTimeDriftStatus(ctx, apiClient, option.Cache, agentID, globalInfo.IsTimeDriftTooLarge())

	s.l.Lock()
	s.lastInfo = globalInfo
	s.l.Unlock()

	return errors.Join(mqttErr, timeDriftErr)
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

	metricID, ok := agentStatusID(c, agentID)
	if !ok {
		return nil
	}

	if err := markAgentStatusCritical(ctx, apiClient, metricID, "Agent local time too different from actual time"); err != nil {
		return err
	}

	s.l.Lock()
	s.timeDriftMarked = true
	s.l.Unlock()

	return nil
}

// updateMQTTStatus marks agent_status as critical when MQTT got disconnected and its address
// isn't reachable.
func (s *SyncInfo) updateMQTTStatus(ctx context.Context, apiClient types.MetricClient, c *cache.Cache, agentID string, mqttConfig config.BleemeoMQTT) error {
	s.l.Lock()
	shouldUpdate := s.shouldUpdateMQTTStatus
	s.shouldUpdateMQTTStatus = false
	s.l.Unlock()

	if !shouldUpdate {
		return nil
	}

	// MQTT isn't ready until agent_status is registered, and there is nothing to mark: the check
	// waits for agent_status.
	metricID, ok := agentStatusID(c, agentID)
	if !ok {
		s.checkMQTTAgain()

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
		return nil
	}

	msg := fmt.Sprintf("Agent can't access Bleemeo MQTT on port %d, is the port blocked by a firewall?", mqttConfig.Port)

	logger.Printf(msg)

	if err := markAgentStatusCritical(ctx, apiClient, metricID, msg); err != nil {
		s.checkMQTTAgain()

		return err
	}

	return nil
}

// checkMQTTAgain sets shouldUpdateMQTTStatus again when agent_status couldn't be marked,
// because it isn't registered yet or its update failed: the next synchronization of info checks
// MQTT again, unless MQTT is connected by then.
func (s *SyncInfo) checkMQTTAgain() {
	s.l.Lock()
	defer s.l.Unlock()

	if s.mqttDisconnected {
		s.shouldUpdateMQTTStatus = true
	}
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

// agentStatusID returns the ID of the agent_status of the agent agentID. It returns false when
// agent_status isn't registered yet.
func agentStatusID(c *cache.Cache, agentID string) (string, bool) {
	metricKey := gloutonTypes.LabelsToText(
		map[string]string{gloutonTypes.LabelName: types.AgentStatusName, gloutonTypes.LabelInstanceUUID: agentID},
	)

	metric, ok := c.MetricLookupFromList()[metricKey]

	return metric.ID, ok
}

// markAgentStatusCritical sets the agent_status metricID to critical, with the given
// description.
func markAgentStatusCritical(ctx context.Context, apiClient types.MetricClient, metricID string, description string) error {
	payload := agentStatusUpdatePayload{
		CurrentStatus:     bleemeo.Status_Critical,
		StatusDescription: []string{description},
	}

	return apiClient.UpdateMetric(ctx, metricID, payload, agentStatusUpdateFields)
}
