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
	cryptoRand "crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"glouton/bleemeo/client"
	"glouton/bleemeo/internal/cache"
	"glouton/bleemeo/internal/common"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/delay"
	"glouton/logger"
	"glouton/types"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
)

var (
	errFQDNNotSet                 = errors.New("unable to register, fqdn is not set")
	errConnectorTemporaryDisabled = errors.New("bleemeo connector temporary disabled")
	errBleemeoUndefined           = errors.New("bleemeo.account_id and/or bleemeo.registration_key is undefined. Please see  https://docs.bleemeo.com/agent/configuration#bleemeoaccount_id ")
	errIncorrectStatusCode        = errors.New("registration status code is")
	errUninitialized              = errors.New("uninitialized")
	errNotExist                   = errors.New("does not exist")
)

const (
	syncMethodInfo          = "info"
	syncMethodAgent         = "agent"
	syncMethodAccountConfig = "accountconfig"
	syncMethodMonitor       = "monitor"
	syncMethodSNMP          = "snmp"
	syncMethodFact          = "facts"
	syncMethodService       = "service"
	syncMethodContainer     = "container"
	syncMethodMetric        = "metric"
)

// Synchronizer synchronize object with Bleemeo.
type Synchronizer struct {
	ctx    context.Context
	option Option
	now    func() time.Time

	realClient       *client.HTTPClient
	client           *wrapperClient
	diagnosticClient *http.Client

	// These fields should always be set in the reload state after being modified.
	nextFullSync  time.Time
	fullSyncCount int

	startedAt               time.Time
	lastSync                time.Time
	lastFactUpdatedAt       string
	lastSNMPcount           int
	successiveErrors        int
	warnAccountMismatchDone bool
	maintenanceMode         bool
	callUpdateLabels        bool
	lastMetricCount         int
	agentID                 string

	// An edge case occurs when an agent is spawned while the maintenance mode is enabled on the backend:
	// the agent cannot register agent_status, thus the MQTT connector cannot start, and we cannot receive
	// notifications to tell us the backend is out of maintenance. So we resort to HTTP polling every 15
	// minutes to check whether we are still in maintenance of not.
	lastMaintenanceSync time.Time

	l                      sync.Mutex
	disabledUntil          time.Time
	disableReason          bleemeoTypes.DisableReason
	forceSync              map[string]bool
	pendingMetricsUpdate   []string
	pendingMonitorsUpdate  []MonitorUpdate
	delayedContainer       map[string]time.Time
	retryableMetricFailure map[bleemeoTypes.FailureKind]bool
	metricRetryAt          time.Time
	lastInfo               bleemeoTypes.GlobalInfo
}

// Option are parameters for the synchronizer.
type Option struct {
	bleemeoTypes.GlobalOption
	Cache *cache.Cache

	// DisableCallback is a function called when Synchronizer request Bleemeo connector to be disabled
	// reason state why it's disabled and until set for how long it should be disabled.
	DisableCallback func(reason bleemeoTypes.DisableReason, until time.Time)

	// UpdateConfigCallback is a function called when Synchronizer detected a AccountConfiguration change
	UpdateConfigCallback func(ctx context.Context)

	// SetInitialized tells the bleemeo connector that the MQTT module can be started
	SetInitialized func()

	// IsMqttConnected returns wether the MQTT connector is operating nominally, and specifically that it can receive mqtt notifications.
	// It is useful for the fallback on http polling described above Synchronizer.lastMaintenanceSync definition.
	// Note: returns false when the mqtt connector is not enabled.
	IsMqttConnected func() bool

	// SetBleemeoInMaintenanceMode makes the bleemeo connector wait a day before checking again for maintenance
	SetBleemeoInMaintenanceMode func(maintenance bool)
}

// New return a new Synchronizer.
func New(option Option) (*Synchronizer, error) {
	return newWithNow(option, time.Now)
}

func newWithNow(option Option, now func() time.Time) (*Synchronizer, error) {
	nextFullSync := now()
	fullSyncCount := 0

	if option.ReloadState != nil {
		if option.ReloadState.NextFullSync().After(nextFullSync) {
			nextFullSync = option.ReloadState.NextFullSync()
		}

		fullSyncCount = option.ReloadState.FullSyncCount()
	}

	s := &Synchronizer{
		option: option,
		now:    now,

		forceSync:              make(map[string]bool),
		nextFullSync:           nextFullSync,
		fullSyncCount:          fullSyncCount,
		retryableMetricFailure: make(map[bleemeoTypes.FailureKind]bool),
	}

	if err := s.option.State.Get("agent_uuid", &s.agentID); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *Synchronizer) DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	s.l.Lock()

	file, err := archive.Create("bleemeo-sync-state.json")
	if err != nil {
		return err
	}

	obj := struct {
		NextFullSync               time.Time
		FullSyncCount              int
		StartedAt                  time.Time
		LastSync                   time.Time
		LastFactUpdatedAt          string
		SuccessiveErrors           int
		WarnAccountMismatchDone    bool
		MaintenanceMode            bool
		LastMetricCount            int
		AgentID                    string
		LastMaintenanceSync        time.Time
		DisabledUntil              time.Time
		DisableReason              bleemeoTypes.DisableReason
		ForceSync                  map[string]bool
		PendingMetricsUpdateCount  int
		PendingMonitorsUpdateCount int
		DelayedContainer           map[string]time.Time
		RetryableMetricFailure     map[bleemeoTypes.FailureKind]bool
		MetricRetryAt              time.Time
		LastInfo                   bleemeoTypes.GlobalInfo
	}{
		NextFullSync:               s.nextFullSync,
		FullSyncCount:              s.fullSyncCount,
		StartedAt:                  s.startedAt,
		LastSync:                   s.lastSync,
		LastFactUpdatedAt:          s.lastFactUpdatedAt,
		SuccessiveErrors:           s.successiveErrors,
		WarnAccountMismatchDone:    s.warnAccountMismatchDone,
		MaintenanceMode:            s.maintenanceMode,
		LastMetricCount:            s.lastMetricCount,
		AgentID:                    s.agentID,
		LastMaintenanceSync:        s.lastMaintenanceSync,
		DisabledUntil:              s.disabledUntil,
		DisableReason:              s.disableReason,
		ForceSync:                  s.forceSync,
		PendingMetricsUpdateCount:  len(s.pendingMetricsUpdate),
		PendingMonitorsUpdateCount: len(s.pendingMonitorsUpdate),
		DelayedContainer:           s.delayedContainer,
		RetryableMetricFailure:     s.retryableMetricFailure,
		MetricRetryAt:              s.metricRetryAt,
		LastInfo:                   s.lastInfo,
	}

	defer s.l.Unlock()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

// Run run the Connector.
//nolint:cyclop
func (s *Synchronizer) Run(ctx context.Context) error {
	s.ctx = ctx
	s.startedAt = s.now()

	firstSync := true

	if s.agentID != "" {
		logger.V(1).Printf("This agent is registered on Bleemeo Cloud platform with UUID %v", s.agentID)

		firstSync = false
	}

	if err := s.setClient(); err != nil {
		return fmt.Errorf("unable to create Bleemeo HTTP client. Is the API base URL correct ? (error is %w)", err)
	}

	// syncInfo early because MQTT connection will establish or not depending on it (maintenance & outdated agent).
	// syncInfo also disable if time drift is too big. We don't do this disable now for a new agent, because
	// we want it to perform registration and creation of agent_status in order to mark this agent as "bad time" on Bleemeo.
	err := s.syncInfoReal(!firstSync)
	if err != nil {
		logger.V(1).Printf("bleemeo: pre-run checks: couldn't sync the global config: %v", err)
	}

	s.option.SetInitialized()

	s.successiveErrors = 0
	successiveAuthErrors := 0

	var minimalDelay time.Duration

	if len(s.option.Cache.FactsByKey()) != 0 {
		logger.V(2).Printf("Waiting a few seconds before running a full synchronization as this agent has a valid cache")

		minimalDelay = delay.JitterDelay(20*time.Second, 0.5)
	}

	for ctx.Err() == nil {
		// TODO: allow WaitDeadline to be interrupted when new metrics arrive
		common.WaitDeadline(ctx, minimalDelay, s.getDisabledUntil, "Synchronization with Bleemeo Cloud platform")

		if ctx.Err() != nil {
			break
		}

		err := s.runOnce(ctx, firstSync)
		if err != nil {
			s.successiveErrors++

			if client.IsAuthError(err) {
				successiveAuthErrors++
			}

			switch {
			case client.IsAuthError(err) && successiveAuthErrors >= 3:
				delay := delay.JitterDelay(
					delay.Exponential(60*time.Second, 1.55, successiveAuthErrors, 6*time.Hour),
					0.1,
				)
				s.option.DisableCallback(bleemeoTypes.DisableAuthenticationError, s.now().Add(delay))
			case client.IsThrottleError(err):
				deadline := s.client.ThrottleDeadline().Add(delay.JitterDelay(15*time.Second, 0.3))
				s.Disable(deadline, bleemeoTypes.DisableTooManyRequests)
			default:
				delay := delay.JitterDelay(
					delay.Exponential(15*time.Second, 1.55, s.successiveErrors, 15*time.Minute),
					0.1,
				)
				s.Disable(s.now().Add(delay), bleemeoTypes.DisableTooManyErrors)

				if client.IsAuthError(err) && successiveAuthErrors == 1 {
					// we disable only to trigger a reconnection on MQTT
					s.option.DisableCallback(bleemeoTypes.DisableAuthenticationError, s.now().Add(10*time.Second))
				}
			}

			switch {
			case client.IsAuthError(err) && s.agentID != "":
				fqdnMessage := ""

				fqdn := s.option.Cache.FactsByKey()[s.agentID]["fqdn"].Value
				if fqdn != "" {
					fqdnMessage = fmt.Sprintf(" with fqdn %s", fqdn)
				}

				logger.Printf(
					"Unable to synchronize with Bleemeo: Unable to login with credentials from state.json. Using agent ID %s%s. Was this server deleted on Bleemeo Cloud platform ?",
					s.agentID,
					fqdnMessage,
				)
			case client.IsAuthError(err):
				registrationKey := []rune(s.option.Config.String("bleemeo.registration_key"))
				for i := range registrationKey {
					if i >= 6 && i < len(registrationKey)-4 {
						registrationKey[i] = '*'
					}
				}

				logger.Printf(
					"Wrong credential for registration. Configuration contains account_id %s and registration_key %s",
					s.option.Config.String("bleemeo.account_id"),
					string(registrationKey),
				)
			default:
				if s.successiveErrors%5 == 1 {
					logger.Printf("Unable to synchronize with Bleemeo: %v", err)
				} else {
					logger.V(1).Printf("Unable to synchronize with Bleemeo: %v", err)
				}
			}
		} else {
			s.successiveErrors = 0
			successiveAuthErrors = 0
		}

		minimalDelay = delay.JitterDelay(15*time.Second, 0.05)

		if firstSync {
			firstSync = false
		}
	}

	return nil //nolint:nilerr
}

// DiagnosticPage return useful information to troubleshoot issue.
func (s *Synchronizer) DiagnosticPage() string {
	var tlsConfig *tls.Config

	builder := &strings.Builder{}

	port := 80

	u, err := url.Parse(s.option.Config.String("bleemeo.api_base"))
	if err != nil {
		fmt.Fprintf(builder, "Bad URL %#v: %v\n", s.option.Config.String("bleemeo.api_base"), err)

		return builder.String()
	}

	if u.Scheme == "https" {
		tlsConfig = &tls.Config{
			InsecureSkipVerify: s.option.Config.Bool("bleemeo.api_ssl_insecure"), //nolint:gosec
		}
		port = 443
	}

	s.l.Lock()

	if s.diagnosticClient == nil {
		s.diagnosticClient = &http.Client{
			Transport: types.NewHTTPTransport(tlsConfig),
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	s.l.Unlock()

	if u.Port() != "" {
		tmp, err := strconv.ParseInt(u.Port(), 10, 0)
		if err != nil {
			fmt.Fprintf(builder, "Bad URL %#v, invalid port: %v\n", s.option.Config.String("bleemeo.api_base"), err)

			return builder.String()
		}

		port = int(tmp)
	}

	tcpMessage := make(chan string)
	httpMessage := make(chan string)

	go func() {
		tcpMessage <- common.DiagnosticTCP(u.Hostname(), port, tlsConfig)
	}()

	go func() {
		httpMessage <- common.DiagnosticHTTP(s.diagnosticClient, u.String())
	}()

	builder.WriteString(<-tcpMessage)
	builder.WriteString(<-httpMessage)

	s.l.Lock()
	if !s.lastInfo.FetchedAt.IsZero() {
		bleemeoTime := s.lastInfo.BleemeoTime()
		delta := s.lastInfo.TimeDrift()
		builder.WriteString(fmt.Sprintf(
			"Bleemeo /v1/info/ fetched at %v. At this moment, time_drift was %v (time expected was %v)\n",
			s.lastInfo.FetchedAt.Format(time.RFC3339),
			delta.Truncate(time.Second),
			bleemeoTime.Format(time.RFC3339),
		))
	}
	s.l.Unlock()

	var count int

	if s.realClient != nil {
		count = s.realClient.RequestsCount()
	}

	builder.WriteString(fmt.Sprintf(
		"Did %d requests since start time at %s (%v ago). Avg of %.2f request/minute\n",
		count,
		s.startedAt.Format(time.RFC3339),
		time.Since(s.startedAt).Round(time.Second),
		float64(count)/time.Since(s.startedAt).Minutes()),
	)

	return builder.String()
}

// NotifyConfigUpdate notify that an Agent configuration change occurred.
// If immediate is true, the configuration change already happened, else it will happen.
func (s *Synchronizer) NotifyConfigUpdate(immediate bool) {
	s.l.Lock()
	defer s.l.Unlock()

	s.forceSync[syncMethodInfo] = true
	s.forceSync[syncMethodAgent] = true
	s.forceSync[syncMethodAccountConfig] = true

	if !immediate {
		return
	}

	s.forceSync[syncMethodMetric] = true
	s.forceSync[syncMethodContainer] = true
	s.forceSync[syncMethodMonitor] = true
}

// UpdateMetrics request to update a specific metrics.
func (s *Synchronizer) UpdateMetrics(metricUUID ...string) {
	s.l.Lock()
	defer s.l.Unlock()

	if len(metricUUID) == 1 && metricUUID[0] == "" {
		// We don't known the metric to update. Update all
		s.forceSync[syncMethodMetric] = true
		s.pendingMetricsUpdate = nil

		return
	}

	s.pendingMetricsUpdate = append(s.pendingMetricsUpdate, metricUUID...)

	s.forceSync[syncMethodMetric] = false
}

// UpdateContainers request to update a containers.
func (s *Synchronizer) UpdateContainers() {
	s.l.Lock()
	defer s.l.Unlock()

	s.forceSync[syncMethodContainer] = false
}

// UpdateInfo request to update a info, which include the time_drift.
func (s *Synchronizer) UpdateInfo() {
	s.l.Lock()
	defer s.l.Unlock()

	s.forceSync[syncMethodInfo] = false
}

// UpdateMonitors requests to update all the monitors.
func (s *Synchronizer) UpdateMonitors() {
	s.l.Lock()
	defer s.l.Unlock()

	s.forceSync[syncMethodAccountConfig] = true
	s.forceSync[syncMethodMonitor] = true
}

func (s *Synchronizer) popPendingMetricsUpdate() []string {
	s.l.Lock()
	defer s.l.Unlock()

	set := make(map[string]bool, len(s.pendingMetricsUpdate))

	for _, m := range s.pendingMetricsUpdate {
		set[m] = true
	}

	s.pendingMetricsUpdate = nil
	result := make([]string, 0, len(set))

	for m := range set {
		result = append(result, m)
	}

	return result
}

func (s *Synchronizer) waitCPUMetric(ctx context.Context) {
	metrics := s.option.Cache.Metrics()
	for _, m := range metrics {
		if m.Labels[types.LabelName] == "cpu_used" || m.Labels[types.LabelName] == "node_cpu_seconds_total" {
			return
		}
	}

	filter := map[string]string{types.LabelName: "cpu_used"}
	filter2 := map[string]string{types.LabelName: "node_cpu_seconds_total"}
	count := 0

	for ctx.Err() == nil && count < 20 {
		count++

		m, _ := s.option.Store.Metrics(filter)
		if len(m) > 0 {
			return
		}

		m, _ = s.option.Store.Metrics(filter2)
		if len(m) > 0 {
			return
		}

		select {
		case <-ctx.Done():
		case <-time.After(1 * time.Second):
		}
	}
}

func (s *Synchronizer) getDisabledUntil() (time.Time, bleemeoTypes.DisableReason) {
	s.l.Lock()
	defer s.l.Unlock()

	return s.disabledUntil, s.disableReason
}

// Disable will disable (or re-enable) the Synchronized until given time.
// To re-enable, use ClearDisable().
func (s *Synchronizer) Disable(until time.Time, reason bleemeoTypes.DisableReason) {
	s.l.Lock()
	defer s.l.Unlock()

	if s.disabledUntil.Before(until) {
		s.disabledUntil = until
		s.disableReason = reason
	}
}

// ClearDisable remove disabling if reason match reasonToClear. It remove the disabling only after delay.
func (s *Synchronizer) ClearDisable(reasonToClear bleemeoTypes.DisableReason, delay time.Duration) {
	s.l.Lock()
	defer s.l.Unlock()

	if time.Now().Before(s.disabledUntil) && s.disableReason == reasonToClear {
		s.disabledUntil = time.Now().Add(delay)
	}
}

// GetJWT to talk to the Bleemeo API.
func (s *Synchronizer) GetJWT(ctx context.Context) (string, error) {
	return s.realClient.VerifyAndGetJWT(ctx, s.agentID)
}

func (s *Synchronizer) setClient() error {
	username := fmt.Sprintf("%s@bleemeo.com", s.agentID)

	var password string

	if err := s.option.State.Get("password", &password); err != nil {
		return err
	}

	client, err := client.NewClient(
		s.option.Config.String("bleemeo.api_base"),
		username,
		password,
		s.option.Config.Bool("bleemeo.api_ssl_insecure"),
		s.option.ReloadState,
	)
	if err != nil {
		return err
	}

	s.realClient = client

	return nil
}

//nolint:cyclop
func (s *Synchronizer) runOnce(ctx context.Context, onlyEssential bool) error {
	var wasCreation bool

	if s.agentID == "" {
		if err := s.register(ctx); err != nil {
			return err
		}

		wasCreation = true

		s.option.NotifyFirstRegistration(ctx)
		// Do one pass of metric registration to register agent_status.
		// MQTT connection require this metric to exists before connecting
		_ = s.syncMetrics(ctx, false, true)

		// Also do syncAgent, because agent configuration is also required for MQTT connection
		_ = s.syncAgent(ctx, false, true)

		// Then wait CPU (which should arrive the all other system metrics)
		// before continuing to process.
		s.waitCPUMetric(ctx)
	}

	syncMethods := s.syncToPerform(ctx)

	if len(syncMethods) == 0 {
		return nil
	}

	s.client = &wrapperClient{
		s:      s,
		client: s.realClient,
	}
	previousCount := s.realClient.RequestsCount()

	syncStep := []struct {
		name                 string
		method               func(context.Context, bool, bool) error
		enabledInMaintenance bool
		skipOnlyEssential    bool // should be true for method that ignore onlyEssential
	}{
		{name: syncMethodInfo, method: s.syncInfo, enabledInMaintenance: true, skipOnlyEssential: true},
		{name: syncMethodAgent, method: s.syncAgent, skipOnlyEssential: true},
		{name: syncMethodAccountConfig, method: s.syncAccountConfig, skipOnlyEssential: true},
		{name: syncMethodFact, method: s.syncFacts},
		{name: syncMethodContainer, method: s.syncContainers},
		{name: syncMethodSNMP, method: s.syncSNMP},
		{name: syncMethodService, method: s.syncServices},
		{name: syncMethodMonitor, method: s.syncMonitors, skipOnlyEssential: true},
		{name: syncMethodMetric, method: s.syncMetrics},
	}
	startAt := s.now()

	var firstErr error

	for _, step := range syncStep {
		if ctx.Err() != nil {
			break
		}

		until, reason := s.getDisabledUntil()
		if s.now().Before(until) {
			// If the agent was disabled because it is too old, we do not want the synchronizer
			// to throw a DisableTooManyErrors because syncInfo() disabled the bleemeo connector.
			// This could alter the synchronizer would wait to sync again, and we do not desire it.
			// This would also show errors that could confuse the user like "Synchronization with
			// Bleemeo Cloud platform still have to wait 1m27s due to too many errors".
			if firstErr == nil && reason != bleemeoTypes.DisableAgentTooOld {
				firstErr = errConnectorTemporaryDisabled
			}

			break
		}

		if s.IsMaintenance() {
			if !step.enabledInMaintenance {
				// store the fact that we must sync this step when we will no longer be in maintenance:
				// we will try to sync it again at every iteration of runOnce(), until we get out of
				// maintenance.
				// This ensures that if the maintenance takes a long time, we will still update the
				// objects that should have been synced in that period.
				if full, ok := syncMethods[step.name]; ok {
					s.l.Lock()
					s.forceSync[step.name] = full || s.forceSync[step.name]
					s.l.Unlock()
				}

				continue
			}
		}

		if full, ok := syncMethods[step.name]; ok {
			err := step.method(ctx, full, onlyEssential && !step.skipOnlyEssential)
			if err != nil {
				logger.V(1).Printf("Synchronization for object %s failed: %v", step.name, err)

				if firstErr == nil {
					firstErr = err
				} else if !client.IsAuthError(firstErr) && client.IsAuthError(err) {
					// Prefere returning Authentication error that other errors
					firstErr = err
				}
			}

			if onlyEssential && !step.skipOnlyEssential {
				// We registered only essential object. Make sure all other
				// object are registered on second run
				s.l.Lock()
				s.forceSync[step.name] = false || s.forceSync[step.name]
				s.l.Unlock()
			}
		}
	}

	if s.callUpdateLabels {
		s.callUpdateLabels = false
		if s.option.NotifyLabelsUpdate != nil {
			s.option.NotifyLabelsUpdate(ctx)
		}
	}

	logger.V(2).Printf("Synchronization took %v for %v (and did %d requests)", s.now().Sub(startAt), syncMethods, s.realClient.RequestsCount()-previousCount)

	if wasCreation {
		s.UpdateUnitsAndThresholds(ctx, true)
	}

	if len(syncMethods) == len(syncStep) && firstErr == nil {
		s.option.Cache.Save()
		s.fullSyncCount++
		s.nextFullSync = s.now().Add(delay.JitterDelay(
			delay.Exponential(time.Hour, 1.75, s.fullSyncCount, 12*time.Hour),
			0.25,
		))

		if s.option.ReloadState != nil {
			s.option.ReloadState.SetFullSyncCount(s.fullSyncCount)
			s.option.ReloadState.SetNextFullSync(s.nextFullSync)
		}

		logger.V(1).Printf("New full synchronization scheduled for %s", s.nextFullSync.Format(time.RFC3339))
	}

	if firstErr == nil {
		s.lastSync = startAt
	}

	return firstErr
}

//nolint:cyclop
func (s *Synchronizer) syncToPerform(ctx context.Context) map[string]bool {
	s.l.Lock()
	defer s.l.Unlock()

	syncMethods := make(map[string]bool)

	fullSync := false
	if s.nextFullSync.Before(s.now()) {
		fullSync = true
	}

	nextConfigAt := s.option.Cache.Agent().NextConfigAt
	if !nextConfigAt.IsZero() && nextConfigAt.Before(s.now()) {
		fullSync = true
	}

	localFacts, _ := s.option.Facts.Facts(ctx, 24*time.Hour)

	if fullSync {
		syncMethods[syncMethodInfo] = fullSync
		syncMethods[syncMethodAgent] = fullSync
		syncMethods[syncMethodAccountConfig] = fullSync
		syncMethods[syncMethodMonitor] = fullSync
		syncMethods[syncMethodSNMP] = fullSync
	}

	if fullSync || s.lastFactUpdatedAt != localFacts["fact_updated_at"] {
		syncMethods[syncMethodFact] = fullSync
	}

	if s.lastSNMPcount != s.option.SNMPOnlineTarget() {
		syncMethods[syncMethodFact] = fullSync
		syncMethods[syncMethodSNMP] = fullSync
	}

	minDelayed := time.Time{}

	for _, delay := range s.delayedContainer {
		if minDelayed.IsZero() || delay.Before(minDelayed) {
			minDelayed = delay
		}
	}

	if fullSync || s.lastSync.Before(s.option.Discovery.LastUpdate()) || (!minDelayed.IsZero() && s.now().After(minDelayed)) {
		syncMethods[syncMethodService] = fullSync
		syncMethods[syncMethodContainer] = fullSync
	}

	if _, ok := syncMethods[syncMethodService]; ok {
		// Metrics registration may need services to be synced, trigger metrics synchronization
		syncMethods[syncMethodMetric] = false
	}

	if _, ok := syncMethods[syncMethodContainer]; ok {
		// Metrics registration may need containers to be synced, trigger metrics synchronization
		syncMethods[syncMethodMetric] = false
	}

	if _, ok := syncMethods[syncMethodMonitor]; ok {
		// Metrics registration may need monitors to be synced, trigger metrics synchronization
		syncMethods[syncMethodMetric] = false
	}

	if fullSync || s.now().After(s.metricRetryAt) || s.lastSync.Before(s.option.Discovery.LastUpdate()) || s.lastMetricCount != s.option.Store.MetricsCount() {
		syncMethods[syncMethodMetric] = fullSync
	}

	// when the mqtt connector is not connected, we cannot receive notifications to get out of maintenance
	// mode, so we poll more often.
	if s.maintenanceMode && !s.option.IsMqttConnected() && s.now().After(s.lastMaintenanceSync.Add(15*time.Minute)) {
		s.forceSync[syncMethodInfo] = false

		s.lastMaintenanceSync = s.now()
	}

	for k, full := range s.forceSync {
		syncMethods[k] = full || syncMethods[k]
		delete(s.forceSync, k)
	}

	return syncMethods
}

func (s *Synchronizer) checkDuplicated() error {
	oldFacts := s.option.Cache.FactsByKey()

	if err := s.factsUpdateList(); err != nil {
		return err
	}

	newFacts := s.option.Cache.FactsByKey()

	factNames := []string{"fqdn", "primary_address", "primary_mac_address"}
	for _, name := range factNames {
		old, ok := oldFacts[s.agentID][name]
		if !ok {
			continue
		}

		new, ok := newFacts[s.agentID][name] //nolint:predeclared
		if !ok {
			continue
		}

		if old.Value == new.Value {
			continue
		}

		until := s.now().Add(delay.JitterDelay(15*time.Minute, 0.05))
		s.Disable(until, bleemeoTypes.DisableDuplicatedAgent)

		if s.option.DisableCallback != nil {
			s.option.DisableCallback(bleemeoTypes.DisableDuplicatedAgent, until)
		}

		logger.Printf(
			"Detected duplicated state.json. Another agent changed %#v from %#v to %#v",
			name,
			old.Value,
			new.Value,
		)
		logger.Printf(
			"The following links may be relevant to solve the issue: https://docs.bleemeo.com/agent/upgrade " +
				"and https://docs.bleemeo.com/agent/installation#install-agent-with-cloud-image-creation ",
		)

		return errConnectorTemporaryDisabled
	}

	return nil
}

func (s *Synchronizer) register(ctx context.Context) error {
	facts, err := s.option.Facts.Facts(ctx, 15*time.Minute)
	if err != nil {
		return err
	}

	fqdn := facts["fqdn"]
	if fqdn == "" {
		return errFQDNNotSet
	}

	name := s.option.Config.String("bleemeo.initial_agent_name")
	if name == "" {
		name = fqdn
	}

	accountID := s.option.Config.String("bleemeo.account_id")
	registrationKey := s.option.Config.String("bleemeo.registration_key")

	for accountID == "" || registrationKey == "" {
		return errBleemeoUndefined
	}

	password, err := generatePassword(20)
	if err != nil {
		return err
	}

	var objectID struct {
		ID string
	}
	// We save an empty agent_uuid before doing the API POST to validate that
	// State can save value.
	if err := s.option.State.Set("agent_uuid", ""); err != nil {
		return err
	}

	statusCode, err := s.realClient.PostAuth(
		ctx,
		"v1/agent/",
		map[string]string{
			"account":          accountID,
			"initial_password": password,
			"display_name":     name,
			"fqdn":             fqdn,
		},
		fmt.Sprintf("%s@bleemeo.com", accountID),
		registrationKey,
		&objectID,
	)
	if err != nil {
		return err
	}

	if statusCode != 201 {
		return fmt.Errorf("%w: got %v, want 201", errIncorrectStatusCode, statusCode)
	}

	s.agentID = objectID.ID

	version := s.option.Config.String("bleemeo.glouton_version")

	sentry.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetContext("agent", map[string]interface{}{
			"agent_id":        s.agentID,
			"glouton_version": version,
		})
	})

	if err := s.option.State.Set("agent_uuid", objectID.ID); err != nil {
		return err
	}

	if err := s.option.State.Set("password", password); err != nil {
		return err
	}

	logger.V(1).Printf("registration successful with UUID %v", objectID.ID)

	_ = s.setClient()

	return nil
}

func generatePassword(length int) (string, error) {
	letters := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789#-_@%*:;$")
	b := make([]rune, length)

	for i := range b {
		bigN, err := cryptoRand.Int(cryptoRand.Reader, big.NewInt(int64(len(letters))))
		n := int(bigN.Int64())

		if err != nil {
			return "", err
		}

		b[i] = letters[n]
	}

	return string(b), nil
}
