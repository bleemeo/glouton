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
	cryptoRand "crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/common"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncapplications"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncservices"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/delay"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/threshold"
	gloutonTypes "github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/version"

	"github.com/getsentry/sentry-go"
	"golang.org/x/oauth2"
)

const (
	agentBrokenCacheKey  = "AgentBroken"
	refreshTokenCacheKey = "RefreshToken"
)

var (
	errFQDNNotSet                 = errors.New("unable to register, fqdn is not set")
	errConnectorTemporaryDisabled = errors.New("bleemeo connector temporary disabled")
	errBleemeoUndefined           = errors.New("bleemeo.account_id and/or bleemeo.registration_key is undefined. Please see  https://go.bleemeo.com/l/agent-configuration-bleemeo-account ")
	errRegistrationFailed         = errors.New("registration failed")
	errUninitialized              = errors.New("uninitialized")
	errNotExist                   = errors.New("does not exist")
)

// Synchronizer synchronize object with Bleemeo.
type Synchronizer struct {
	option        types.Option
	now           func() time.Time
	inTest        bool
	synchronizers []types.EntitySynchronizer
	state         *synchronizerState
	hasFeature    map[types.APIFeature]bool

	requestCounter atomic.Uint32
	realClient     *bleemeo.Client

	// These fields should always be set in the reload state after being modified.
	nextFullSync  time.Time
	fullSyncCount int

	startedAt                 time.Time
	syncHeartbeat             time.Time
	heartbeatConsecutiveError int
	lastSync                  time.Time
	lastVSphereAgentsPurge    time.Time
	successiveErrors          int
	firstErrorAt              time.Time
	warnAccountMismatchDone   bool
	maintenanceMode           bool
	suspendedMode             bool
	agentID                   string
	delayedContainer          map[string]time.Time

	// logOnce is used to log that the limit of metrics has been reached.
	logOnce             sync.Once
	lastDenyReasonLogAt time.Time

	l                             sync.Mutex
	disabledUntil                 time.Time
	disableReason                 bleemeoTypes.DisableReason
	forceSync                     map[types.EntityName]types.SyncType
	pendingMetricsUpdate          []string
	pendingMonitorsUpdate         []MonitorUpdate
	thresholdOverrides            map[thresholdOverrideKey]threshold.Threshold
	retryableMetricFailure        map[bleemeoTypes.FailureKind]bool
	disableCrashReportUploadUntil time.Time
	lastInfo                      bleemeoTypes.GlobalInfo
	// Whether the agent MQTT status should be synced. This is used to avoid syncing
	// the MQTT status too soon before the agent has tried to connect to MQTT.
	shouldUpdateMQTTStatus bool
	// Whether the agent is connected to MQTT. We use a pointer to know if the field is set.
	isMQTTConnected *bool

	dialer net.Dialer
}

type thresholdOverrideKey struct {
	MetricName string
	AgentID    string
}

// New return a new Synchronizer.
func New(option types.Option) *Synchronizer {
	return newWithNow(option, time.Now)
}

func newForTest(option types.Option, now func() time.Time) *Synchronizer {
	s := newWithNow(option, now)

	s.inTest = true

	return s
}

func newWithNow(option types.Option, now func() time.Time) *Synchronizer {
	nextFullSync := now()
	fullSyncCount := 0

	state := &synchronizerState{}

	if option.ReloadState != nil {
		if option.ReloadState.NextFullSync().After(nextFullSync) {
			nextFullSync = option.ReloadState.NextFullSync()
		}

		fullSyncCount = option.ReloadState.FullSyncCount()
	}

	s := &Synchronizer{
		option: option,
		now:    now,

		forceSync:              make(map[types.EntityName]types.SyncType),
		nextFullSync:           nextFullSync,
		fullSyncCount:          fullSyncCount,
		retryableMetricFailure: make(map[bleemeoTypes.FailureKind]bool),
		state:                  state,
	}

	s.synchronizers = []types.EntitySynchronizer{
		&CompatibilityWrapper{state: state, name: types.EntityInfo, method: s.syncInfo, enabledInMaintenance: true, skipOnlyEssential: true},
		&CompatibilityWrapper{state: state, name: types.EntityAgent, method: s.syncAgent, enabledInSuspendedMode: true, skipOnlyEssential: true},
		&CompatibilityWrapper{state: state, name: types.EntityAccountConfig, method: s.syncAccountConfig, enabledInSuspendedMode: true, skipOnlyEssential: true},
		&CompatibilityWrapper{state: state, name: types.EntityFact, method: s.syncFacts},
		&CompatibilityWrapper{state: state, name: types.EntityContainer, method: s.syncContainers},
		&CompatibilityWrapper{state: state, name: types.EntitySNMP, method: s.syncSNMP},
		&CompatibilityWrapper{state: state, name: types.EntityVSphere, method: s.syncVSphere},
		syncapplications.New(),
		syncservices.New(),
		&CompatibilityWrapper{state: state, name: types.EntityMonitor, method: s.syncMonitors, skipOnlyEssential: true},
		&CompatibilityWrapper{state: state, name: types.EntityMetric, method: s.syncMetrics},
		&CompatibilityWrapper{state: state, name: types.EntityConfig, method: s.syncConfig},
		&CompatibilityWrapper{state: state, name: types.EntityDiagnostics, method: s.syncDiagnostics},
	}

	if s.option.State != nil {
		s.agentID, _ = s.option.State.BleemeoCredentials()
	}

	return s
}

func (s *Synchronizer) newClient() types.Client {
	if s.option.ProvideClient != nil {
		// Allows tests to inject mock clients
		return s.option.ProvideClient()
	}

	return &wrapperClient{
		client:           s.realClient,
		checkDuplicateFn: s.checkDuplicated,
	}
}

func (s *Synchronizer) DiagnosticArchive(_ context.Context, archive gloutonTypes.ArchiveWriter) error {
	file, err := archive.Create("bleemeo-sync-state.json")
	if err != nil {
		return err
	}

	delayedContainer, _ := s.DelayedContainers()

	s.l.Lock()
	defer s.l.Unlock()

	s.state.l.Lock()
	defer s.state.l.Unlock()

	obj := struct {
		NextFullSync               time.Time
		HeartBeat                  time.Time
		HeartbeatConsecutiveError  int
		FullSyncCount              int
		StartedAt                  time.Time
		LastSync                   time.Time
		LastMetricActivation       time.Time
		LastFactUpdatedAt          string
		SuccessiveErrors           int
		FirstErrorAt               time.Time
		WarnAccountMismatchDone    bool
		MaintenanceMode            bool
		LastMetricCount            int
		AgentID                    string
		LastMaintenanceSync        time.Time
		DisabledUntil              time.Time
		DisableReason              bleemeoTypes.DisableReason
		ForceSync                  map[types.EntityName]types.SyncType
		PendingMetricsUpdateCount  int
		PendingMonitorsUpdateCount int
		DelayedContainer           map[string]time.Time
		RetryableMetricFailure     map[bleemeoTypes.FailureKind]bool
		MetricRetryAt              time.Time
		LastInfo                   bleemeoTypes.GlobalInfo
		ThresholdOverrides         string
		APIFeatures                map[string]bool
	}{
		NextFullSync:               s.nextFullSync,
		HeartBeat:                  s.syncHeartbeat,
		HeartbeatConsecutiveError:  s.heartbeatConsecutiveError,
		FullSyncCount:              s.fullSyncCount,
		StartedAt:                  s.startedAt,
		LastSync:                   s.lastSync,
		LastFactUpdatedAt:          s.state.lastFactUpdatedAt,
		LastMetricActivation:       s.state.lastMetricActivation,
		SuccessiveErrors:           s.successiveErrors,
		FirstErrorAt:               s.firstErrorAt,
		WarnAccountMismatchDone:    s.warnAccountMismatchDone,
		MaintenanceMode:            s.maintenanceMode,
		LastMetricCount:            s.state.lastMetricCount,
		AgentID:                    s.agentID,
		LastMaintenanceSync:        s.state.lastMaintenanceSync,
		DisabledUntil:              s.disabledUntil,
		DisableReason:              s.disableReason,
		ForceSync:                  s.forceSync,
		PendingMetricsUpdateCount:  len(s.pendingMetricsUpdate),
		PendingMonitorsUpdateCount: len(s.pendingMonitorsUpdate),
		DelayedContainer:           delayedContainer,
		RetryableMetricFailure:     s.retryableMetricFailure,
		MetricRetryAt:              s.state.metricRetryAt,
		LastInfo:                   s.lastInfo,
		ThresholdOverrides:         fmt.Sprintf("%v", s.thresholdOverrides),
		APIFeatures:                make(map[string]bool, len(s.hasFeature)),
	}

	for k, v := range s.hasFeature {
		obj.APIFeatures[k.String()] = v
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

// Run runs the Synchronizer.
func (s *Synchronizer) Run(ctx context.Context) error {
	s.startedAt = s.now()

	firstSync := true

	if s.agentID != "" {
		logger.V(1).Printf("This agent is registered on Bleemeo Cloud platform with UUID %v", s.agentID)

		firstSync = false
	}

	s.l.Lock()
	err := s.setClient()
	s.l.Unlock()

	if err != nil {
		return fmt.Errorf("unable to create Bleemeo HTTP client. Is the API base URL correct ? (error is %w)", err)
	}

	cfg, ok := s.option.Cache.CurrentAccountConfig()
	if ok {
		s.state.currentConfigNotified = cfg.ID
	}

	// syncInfo early because MQTT connection will establish or not depending on it (maintenance & outdated agent).
	// syncInfo also disable if time drift is too big. We don't do this disable now for a new agent, because
	// we want it to perform registration and creation of agent_status in order to mark this agent as "bad time" on Bleemeo.
	exec := s.newLimitedExecution(false, nil)

	_, err = s.syncInfoReal(ctx, exec, !firstSync)
	if err != nil {
		logger.V(1).Printf("bleemeo: pre-run checks: couldn't sync the global config: %v", err)
	}

	exec.executePostRunCalls()

	s.option.SetInitialized()

	s.l.Lock()
	s.successiveErrors = 0
	s.firstErrorAt = time.Time{}
	s.l.Unlock()

	successiveAuthErrors := 0
	firstAuthErrorAt := time.Time{}

	// We schedule a metric synchronization for in a few minutes to permit
	// the deactivation of metrics whose item has disappeared between
	// the startup of Glouton and the metrics gracePeriod.
	// 11min is the time needed to run two metric collections.
	s.scheduleMetricSync(ctx, 11*time.Minute)

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

		_, err := s.runOnce(ctx, firstSync)
		if err != nil {
			var shouldSetAgentBrokenFlagTo time.Time

			s.l.Lock()
			s.successiveErrors++

			if s.firstErrorAt.IsZero() {
				s.firstErrorAt = s.now()
			} else if s.now().Sub(s.firstErrorAt) > 36*time.Hour {
				shouldSetAgentBrokenFlagTo = s.firstErrorAt
			}

			s.l.Unlock()

			if IsAuthenticationError(err) {
				successiveAuthErrors++

				if firstAuthErrorAt.IsZero() {
					firstAuthErrorAt = s.now()
				} else if s.now().Sub(firstAuthErrorAt) > 2*time.Hour {
					shouldSetAgentBrokenFlagTo = firstAuthErrorAt
				}
			} else {
				firstAuthErrorAt = time.Time{}
			}

			if !shouldSetAgentBrokenFlagTo.IsZero() && !stateHasValue(agentBrokenCacheKey, s.option.State) {
				err := s.option.State.Set(agentBrokenCacheKey, shouldSetAgentBrokenFlagTo.Format(time.RFC3339))
				if err != nil {
					logger.V(1).Printf("Failed to write agent broken flag to state cache: %v", err)
				}
			}

			if IsThrottleError(err) {
				deadline := exec.client.ThrottleDeadline().Add(delay.JitterDelay(15*time.Second, 0.3))
				s.Disable(deadline, bleemeoTypes.DisableTooManyRequests)
			} else {
				s.l.Lock()
				successiveErrors := s.successiveErrors
				s.l.Unlock()

				if stateHasValue(agentBrokenCacheKey, s.option.State) {
					disableDelay := delay.Exponential(10*time.Minute, 3, successiveErrors, 5*24*time.Hour)
					s.option.DisableCallback(bleemeoTypes.DisableLongStandingError, s.now().Add(disableDelay))
				} else {
					disableDelay := delay.JitterDelay(
						delay.Exponential(15*time.Second, 1.55, successiveErrors, 15*time.Minute),
						0.1,
					)

					s.Disable(s.now().Add(disableDelay), bleemeoTypes.DisableTooManyErrors)
				}

				if IsAuthenticationError(err) && successiveAuthErrors == 1 {
					// we disable only to trigger a reconnection on MQTT
					s.option.DisableCallback(bleemeoTypes.DisableAuthenticationError, s.now().Add(10*time.Second))
				}
			}

			switch {
			case IsAuthenticationError(err) && s.agentID != "":
				fqdnMessage := ""

				fqdn := s.option.Cache.FactsByKey()[s.agentID]["fqdn"].Value
				if fqdn != "" {
					fqdnMessage = " with fqdn " + fqdn
				}

				logger.Printf(
					"Unable to synchronize with Bleemeo: Unable to login with credentials from state.json. Using agent ID %s%s. Was this server deleted on Bleemeo Cloud platform ?",
					s.agentID,
					fqdnMessage,
				)
			case IsAuthenticationError(err):
				registrationKey := []rune(s.option.Config.Bleemeo.RegistrationKey)
				for i := range registrationKey {
					if i >= 6 && i < len(registrationKey)-4 {
						registrationKey[i] = '*'
					}
				}

				logger.Printf(
					"Wrong credential for registration. Configuration contains account_id %s and registration_key %s",
					s.option.Config.Bleemeo.AccountID,
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
			s.l.Lock()
			s.successiveErrors = 0
			s.firstErrorAt = time.Time{}
			s.l.Unlock()

			successiveAuthErrors = 0
			firstAuthErrorAt = time.Time{}

			err = s.option.State.Delete(agentBrokenCacheKey)
			if err != nil {
				logger.V(1).Printf("Failed to delete agent broken flag from state cache: %v", err)
			}
		}

		minimalDelay = delay.JitterDelay(15*time.Second, 0.05)

		if firstSync {
			firstSync = false
		}
	}

	return nil //nolint:nilerr
}

// scheduleMetricSync will run s.syncMetrics after the given delay, in a new goroutine.
func (s *Synchronizer) scheduleMetricSync(ctx context.Context, delay time.Duration) {
	go func() {
		defer crashreport.ProcessPanic()

		timer := time.NewTimer(delay)

		select {
		case <-timer.C:
			s.l.Lock()
			defer s.l.Unlock()

			s.requestSynchronizationLocked(types.EntityMetric, false)
		case <-ctx.Done(): // return
		}
	}()
}

// DiagnosticPage return useful information to troubleshoot issue.
func (s *Synchronizer) DiagnosticPage() string {
	var tlsConfig *tls.Config

	builder := &strings.Builder{}

	port := 80

	u, err := url.Parse(s.option.Config.Bleemeo.APIBase)
	if err != nil {
		fmt.Fprintf(builder, "Bad URL %#v: %v\n", s.option.Config.Bleemeo.APIBase, err)

		return builder.String()
	}

	if u.Scheme == "https" {
		tlsConfig = &tls.Config{
			InsecureSkipVerify: s.option.Config.Bleemeo.APISSLInsecure, //nolint:gosec
		}
		port = 443
	}

	var apiClient *bleemeo.Client

	s.l.Lock()

	if s.realClient == nil {
		err = s.setClient()
		if err != nil {
			s.l.Unlock()

			fmt.Fprintf(builder, "Can't initialize the API client: %v\n", err)

			return builder.String()
		}
	}

	apiClient = s.realClient

	s.l.Unlock()

	if u.Port() != "" {
		tmp, err := strconv.ParseInt(u.Port(), 10, 0)
		if err != nil {
			fmt.Fprintf(builder, "Bad URL %#v, invalid port: %v\n", s.option.Config.Bleemeo.APIBase, err)

			return builder.String()
		}

		port = int(tmp)
	}

	tcpMessage := make(chan string)
	httpMessage := make(chan string)

	go func() {
		defer crashreport.ProcessPanic()

		tcpMessage <- common.DiagnosticTCP(u.Hostname(), port, tlsConfig)
	}()

	go func() {
		defer crashreport.ProcessPanic()

		httpMessage <- common.DiagnosticHTTP(apiClient)
	}()

	builder.WriteString(<-tcpMessage)
	builder.WriteString(<-httpMessage)

	s.l.Lock()

	if !s.lastInfo.FetchedAt.IsZero() {
		bleemeoTime := s.lastInfo.BleemeoTime()
		delta := s.lastInfo.TimeDrift()
		fmt.Fprintf(
			builder,
			"Bleemeo /v1/info/ fetched at %v. At this moment, time_drift was %v (time expected was %v)\n",
			s.lastInfo.FetchedAt.Format(time.RFC3339),
			delta.Truncate(time.Second),
			bleemeoTime.Format(time.RFC3339),
		)
	}

	s.l.Unlock()

	count := s.requestCounter.Load()
	fmt.Fprintf(
		builder,
		"Did %d requests since start time at %s (%v ago). Avg of %.2f request/minute\n",
		count,
		s.startedAt.Format(time.RFC3339),
		time.Since(s.startedAt).Round(time.Second),
		float64(count)/time.Since(s.startedAt).Minutes(),
	)

	return builder.String()
}

// NotifyConfigUpdate notify that an Agent configuration change occurred.
// If immediate is true, the configuration change already happened, else it will happen.
func (s *Synchronizer) NotifyConfigUpdate(immediate bool) {
	s.l.Lock()
	defer s.l.Unlock()

	s.requestSynchronizationLocked(types.EntityInfo, true)
	s.requestSynchronizationLocked(types.EntityAgent, true)
	s.requestSynchronizationLocked(types.EntityFact, true)
	s.requestSynchronizationLocked(types.EntityAccountConfig, true)

	if !immediate {
		return
	}

	s.requestSynchronizationLocked(types.EntityMetric, true)
	s.requestSynchronizationLocked(types.EntityContainer, true)
	s.requestSynchronizationLocked(types.EntityMonitor, true)
}

// LastMetricActivation return the date at which last metric was activated/registrered.
func (s *Synchronizer) LastMetricActivation() time.Time {
	s.state.l.Lock()
	defer s.state.l.Unlock()

	return s.state.lastMetricActivation
}

// UpdateMetrics request to update a specific metrics.
func (s *Synchronizer) UpdateMetrics(metricUUID ...string) {
	s.l.Lock()
	defer s.l.Unlock()

	if len(metricUUID) == 1 && metricUUID[0] == "" {
		// We don't known the metric to update. Update all
		s.requestSynchronizationLocked(types.EntityMetric, true)
		s.pendingMetricsUpdate = nil

		return
	}

	s.pendingMetricsUpdate = append(s.pendingMetricsUpdate, metricUUID...)

	s.requestSynchronizationLocked(types.EntityMetric, false)
}

// UpdateContainers request to update a containers.
func (s *Synchronizer) UpdateContainers() {
	s.l.Lock()
	defer s.l.Unlock()

	s.requestSynchronizationLocked(types.EntityContainer, false)
}

// UpdateInfo request to update a info, which include the time_drift.
func (s *Synchronizer) UpdateInfo() {
	s.l.Lock()
	defer s.l.Unlock()

	s.requestSynchronizationLocked(types.EntityInfo, false)
}

// UpdateMonitors requests to update all the monitors.
func (s *Synchronizer) UpdateMonitors() {
	s.l.Lock()
	defer s.l.Unlock()

	s.requestSynchronizationLocked(types.EntityAccountConfig, true)
	s.requestSynchronizationLocked(types.EntityMonitor, true)
}

// UpdateMaintenance requests to check for the maintenance mode again.
func (s *Synchronizer) UpdateMaintenance() {
	s.l.Lock()
	defer s.l.Unlock()

	s.requestSynchronizationLocked(types.EntityInfo, false)
}

// SetMaintenance allows to trigger the maintenance mode for the synchronize.
// When running in maintenance mode, only the general infos, the agent and its configuration are synced.
func (s *Synchronizer) SetMaintenance(ctx context.Context, maintenance bool) {
	if s.IsMaintenance() && !maintenance {
		// getting out of maintenance, let's check for a duplicated state.json file
		err := s.checkDuplicated(ctx, s.newClient())
		if err != nil {
			// it's not a critical error at all, we will perform this check again on the next synchronization pass
			logger.V(2).Printf("Couldn't check for duplicated agent: %v", err)
		}
	}

	s.l.Lock()
	defer s.l.Unlock()

	s.maintenanceMode = maintenance
}

// UpdateMonitor requests to update a monitor, identified by its UUID. It allows for adding, updating and removing a monitor.
func (s *Synchronizer) UpdateMonitor(op string, uuid string) {
	s.l.Lock()
	defer s.l.Unlock()

	mu := MonitorUpdate{uuid: uuid}

	switch op {
	case "change":
		mu.op = Change
	case "delete":
		mu.op = Delete
	}

	s.pendingMonitorsUpdate = append(s.pendingMonitorsUpdate, mu)
	// ideally we only want to (re)fetch the agent associated with the monitor, but
	// 1) at this point, we don't know which agent
	// 2) currently the agent synchronized don't support updating only one agent
	s.requestSynchronizationLocked(types.EntityAgent, true)
	s.requestSynchronizationLocked(types.EntityMonitor, false)
}

// IsMaintenance returns whether the synchronizer is currently in maintenance mode (not making any request except info/agent).
func (s *Synchronizer) IsMaintenance() bool {
	s.l.Lock()
	defer s.l.Unlock()

	return s.maintenanceMode
}

func (s *Synchronizer) UpdateDelayedContainers(localContainers []facts.Container) {
	s.l.Lock()
	newDelayedContainer := make(map[string]time.Time, len(s.delayedContainer))
	s.l.Unlock()

	delay := time.Duration(s.option.Config.Bleemeo.ContainerRegistrationDelaySeconds) * time.Second

	for _, container := range localContainers {
		// if the container (name) was recently seen, don't delay it.
		if s.option.IsContainerNameRecentlyDeleted != nil && s.option.IsContainerNameRecentlyDeleted(container.ContainerName()) {
			continue
		}

		if s.now().Sub(container.CreatedAt()) < delay {
			enable, explicit := s.option.IsContainerEnabled(container)
			if !enable || !explicit {
				newDelayedContainer[container.ID()] = container.CreatedAt().Add(delay)
			}
		}
	}

	s.l.Lock()
	defer s.l.Unlock()

	s.delayedContainer = newDelayedContainer
}

func (s *Synchronizer) DelayedContainers() (delayedByID map[string]time.Time, minDelayed time.Time) {
	s.l.Lock()
	defer s.l.Unlock()

	for _, delay := range s.delayedContainer {
		if minDelayed.IsZero() || delay.Before(minDelayed) {
			minDelayed = delay
		}
	}

	return s.delayedContainer, minDelayed
}

func (s *Synchronizer) APIHasFeature(feature types.APIFeature) bool {
	s.l.Lock()
	defer s.l.Unlock()

	return s.hasFeature[feature]
}

func (s *Synchronizer) SetAPIHasFeature(feature types.APIFeature, has bool) {
	s.l.Lock()
	defer s.l.Unlock()

	if s.hasFeature == nil {
		s.hasFeature = make(map[types.APIFeature]bool)
	}

	s.hasFeature[feature] = has
}

func (s *Synchronizer) SuccessiveErrors() int {
	s.l.Lock()
	defer s.l.Unlock()

	return s.successiveErrors
}

// ScheduleDiagnosticUpload stores the given diagnostic until the next synchronization,
// where it will be uploaded to the API.
// If another call to this method is made before the next synchronization,
// only the latest diagnostic will be uploaded.
func (s *Synchronizer) ScheduleDiagnosticUpload(filename, requestToken string, contents []byte) {
	s.l.Lock()
	defer s.l.Unlock()

	s.state.l.Lock()
	defer s.state.l.Unlock()

	s.requestSynchronizationLocked(types.EntityDiagnostics, false)
	s.state.onDemandDiagnostic = synchronizerOnDemandDiagnostic{
		filename:     filepath.Base(filename),
		archive:      contents,
		s:            s,
		requestToken: requestToken,
	}
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
	// In test, we skip this waiting time.
	if s.inTest {
		return
	}

	metrics := s.option.Cache.Metrics()
	for _, m := range metrics {
		if m.Labels[gloutonTypes.LabelName] == "cpu_used" || m.Labels[gloutonTypes.LabelName] == "node_cpu_seconds_total" {
			return
		}
	}

	filter := map[string]string{gloutonTypes.LabelName: "cpu_used"}
	filter2 := map[string]string{gloutonTypes.LabelName: "node_cpu_seconds_total"}
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

// VerifyAndGetToken is used to get a valid token.
// Should only be called after the synchronized had called SetInitialized and AgentID is filled in State.
func (s *Synchronizer) VerifyAndGetToken(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Low-cost API endpoint, used to test the validity of our token.
	// We rely on the client to renew the token if it has expired.
	result, err := s.realClient.Get(ctx, bleemeo.ResourceAgent, s.agentID, "id")
	if err != nil {
		return "", err
	}

	var res struct {
		ID string `json:"id"`
	}

	err = json.Unmarshal(result, &res)
	if err != nil {
		return "", err
	}

	if res.ID != s.agentID {
		return "", errInvalidAgentID
	}

	token, err := s.realClient.GetToken(ctx)
	if err != nil {
		return "", err
	}

	return token.AccessToken, nil
}

func (s *Synchronizer) setClient() error {
	username := s.agentID + "@bleemeo.com"
	_, password := s.option.State.BleemeoCredentials()

	var initialRefreshTokenOpt bleemeo.ClientOption

	if s.option.ReloadState != nil {
		if tk := s.option.ReloadState.Token(); tk.Valid() && tk.RefreshToken != "" {
			initialRefreshTokenOpt = bleemeo.WithInitialOAuthRefreshToken(tk.RefreshToken)
		}
	}

	if initialRefreshTokenOpt == nil {
		var refreshToken string

		err := s.option.State.Get(refreshTokenCacheKey, &refreshToken)
		if err != nil {
			logger.V(1).Printf("Failed to retrieve refresh token from state cache: %v", err)
		} else if refreshToken != "" {
			initialRefreshTokenOpt = bleemeo.WithInitialOAuthRefreshToken(refreshToken)
		}
	}

	newOAuthTokenCallback := func(token *oauth2.Token) {
		err := s.option.State.Set(refreshTokenCacheKey, token.RefreshToken)
		if err != nil {
			logger.V(1).Printf("Failed to set refresh token in state cache: %v", err)
		}

		if s.option.ReloadState != nil {
			s.option.ReloadState.SetToken(token)
		}
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: s.option.Config.Bleemeo.APISSLInsecure, //nolint:gosec
	}
	transportOpts := &gloutonTypes.CustomTransportOptions{
		UserAgentHeader: version.UserAgent(),
		RequestCounter:  &s.requestCounter,
		EnableLogger:    true,
	}
	cl := &http.Client{
		Transport: gloutonTypes.NewHTTPTransport(tlsConfig, transportOpts),
	}

	client, err := bleemeo.NewClient(
		bleemeo.WithCredentials(username, password),
		bleemeo.WithOAuthClient(gloutonOAuthClientID, ""),
		bleemeo.WithEndpoint(s.option.Config.Bleemeo.APIBase),
		bleemeo.WithNewOAuthTokenCallback(newOAuthTokenCallback),
		bleemeo.WithHTTPClient(cl),
		initialRefreshTokenOpt,
	)
	if err != nil {
		return err
	}

	s.realClient = client

	return nil
}

// HealthCheck perform some health check and log any issue found.
// This method could panic when health conditions are bad for too long, in order to cause a Glouton restart.
func (s *Synchronizer) HealthCheck() bool {
	s.l.Lock()
	defer s.l.Unlock()

	syncHeartbeat := s.syncHeartbeat
	if syncHeartbeat.IsZero() {
		syncHeartbeat = s.startedAt
	}

	if s.now().Before(s.disabledUntil) {
		// synchronization is still disabled, don't check syncHeartbeat
		return true
	}

	if s.now().Sub(syncHeartbeat) > time.Hour {
		logger.Printf("Bleemeo API communication didn't run since %s. This should be a Glouton bug", syncHeartbeat.Format(time.RFC3339))

		if s.now().Sub(syncHeartbeat) > 6*time.Hour {
			s.heartbeatConsecutiveError++

			if s.heartbeatConsecutiveError >= 3 {
				logger.Printf("Bleemeo API communication is broken. Glouton seems unhealthy, killing myself")

				// We don't know how big the buffer needs to be to collect
				// all the goroutines. Use 2MB buffer which hopefully is enough
				buffer := make([]byte, 1<<21)

				n := runtime.Stack(buffer, true)
				logger.Printf("%s", string(buffer[:n]))
				panic("Glouton seems unhealthy (last API request too old), killing myself")
			}
		}
	} else {
		s.heartbeatConsecutiveError = 0
	}

	return true
}

func (s *Synchronizer) runOnce(ctx context.Context, onlyEssential bool) (*Execution, error) {
	var wasCreation bool

	startAt := s.now()

	s.l.Lock()
	s.syncHeartbeat = time.Now()
	s.l.Unlock()

	if s.agentID == "" {
		if err := s.register(ctx); err != nil {
			return nil, err
		}

		wasCreation = true

		execution := s.newLimitedExecution(
			false,
			map[types.EntityName]types.SyncType{
				types.EntityAccountConfig: types.SyncTypeForceCacheRefresh,
				types.EntityMetric:        types.SyncTypeNormal,
				types.EntityAgent:         types.SyncTypeForceCacheRefresh,
			},
		)
		_ = execution.run(ctx)

		s.option.NotifyFirstRegistration()

		// Then wait CPU (which should arrive the all other system metrics)
		// before continuing to process.
		s.waitCPUMetric(ctx)
	}

	execution := s.newExecution(onlyEssential, wasCreation)

	err := execution.run(ctx)
	if execution.forceCacheRefreshForAll() && err == nil {
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

	if err == nil {
		s.lastSync = startAt
	}

	return execution, err
}

// checkDuplicated checks if another glouton is running with the same ID.
func (s *Synchronizer) checkDuplicated(ctx context.Context, client types.Client) error {
	oldFacts := s.option.Cache.FactsByKey()

	if err := s.factsUpdateList(ctx, client); err != nil {
		return fmt.Errorf("update facts list: %w", err)
	}

	newFacts := s.option.Cache.FactsByKey()

	isDuplicated, message := isDuplicatedUsingFacts(s.startedAt, oldFacts[s.agentID], newFacts[s.agentID])

	if !isDuplicated {
		return nil
	}

	logger.Printf(message)
	logger.Printf(
		"The following links may be relevant to solve the issue: https://go.bleemeo.com/l/doc-duplicated-agent",
	)

	until := s.now().Add(delay.JitterDelay(15*time.Minute, 0.05))
	s.Disable(until, bleemeoTypes.DisableDuplicatedAgent)

	if s.option.DisableCallback != nil {
		s.option.DisableCallback(bleemeoTypes.DisableDuplicatedAgent, until)
	}

	// The agent is duplicated, update the last duplication date on the API.
	err := client.UpdateAgentLastDuplicationDate(ctx, s.agentID, s.now())
	if err != nil {
		logger.V(1).Printf("Failed to update duplication date: %s", err)
	}

	return errConnectorTemporaryDisabled
}

// isDuplicatedUsingFacts checks if another agent with the same ID using AgentFact registered
// on Bleemeo API. This happen only if the state file is shared by multiple running agents.
func isDuplicatedUsingFacts(agentStartedAt time.Time, oldFacts map[string]bleemeoTypes.AgentFact, newFacts map[string]bleemeoTypes.AgentFact) (bool, string) {
	// We use the fact that the agents will send different facts to the API.
	// If the facts we fetch from the API are not the same as the last facts
	// we registered, another agent with the same ID must have modified them.
	// Note that this won't work if the two agents are on the same host
	// because both agents will send mostly the same facts.
	factNames := []string{"fqdn", "primary_address", "primary_mac_address", "glouton_pid"}
	for _, name := range factNames {
		old, ok := oldFacts[name]
		if !ok {
			continue
		}

		newFact, ok := newFacts[name]
		if !ok {
			continue
		}

		if old.Value == newFact.Value {
			continue
		}

		if name == "glouton_pid" {
			// For the fact glouton_pid we are going to be smarter. We don't want to announce a duplicate state.json
			// when glouton crash or when its state.cache.json is restored to an older version.
			// To solve this problem, we check that facts (old & new) where updated *after* Glouton started (this assume fact glouton_pid
			// is updated at the same time as fact_updated_at, which is true unless a Glouton crashed during facts update).
			oldStartedAt, _ := time.Parse(time.RFC3339, oldFacts[facts.FactUpdatedAt].Value)
			newStartedAt, _ := time.Parse(time.RFC3339, newFacts[facts.FactUpdatedAt].Value)

			if !oldStartedAt.IsZero() && !newStartedAt.IsZero() && oldStartedAt.Before(agentStartedAt) && newStartedAt.Before(agentStartedAt) {
				continue
			}
		}

		message := fmt.Sprintf(
			"Detected duplicated state.json. Another agent changed %#v from %#v to %#v",
			name,
			old.Value,
			newFact.Value,
		)

		return true, message
	}

	return false, ""
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

	name := s.option.Config.Bleemeo.InitialAgentName
	if name == "" {
		name = fqdn
	}

	accountID := s.option.Config.Bleemeo.AccountID
	registrationKey := s.option.Config.Bleemeo.RegistrationKey

	for accountID == "" || registrationKey == "" {
		return errBleemeoUndefined
	}

	password, err := generatePassword(20)
	if err != nil {
		return err
	}

	// We save an empty agent_uuid before doing the API POST to validate that
	// State can save value.
	if err := s.option.State.SetBleemeoCredentials("", ""); err != nil {
		return err
	}

	agentID, err := s.newClient().RegisterSelf(ctx, accountID, password, s.option.Config.Bleemeo.InitialServerGroupName, name, fqdn, registrationKey)
	if err != nil {
		return err
	}

	s.agentID = agentID

	sentry.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetContext("agent", map[string]any{
			"agent_id":        s.agentID,
			"glouton_version": version.Version,
		})
	})

	if err := s.option.State.SetBleemeoCredentials(agentID, password); err != nil {
		logger.Printf("Failed to persist Bleemeo credentials. The agent may register itself multiple-time: %v", err)
	}

	logger.V(1).Printf("Registration successful with UUID %v", agentID)

	s.l.Lock()
	defer s.l.Unlock()

	return s.setClient()
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

// logThrottle logs a message at most once per hour, all other logs are dropped to prevent spam.
func (s *Synchronizer) logThrottle(msg string) {
	if time.Since(s.lastDenyReasonLogAt) > time.Hour {
		logger.V(1).Println(msg)

		s.lastDenyReasonLogAt = time.Now()
	}
}

func (s *Synchronizer) SetMQTTConnected(isConnected bool) {
	s.l.Lock()
	defer s.l.Unlock()

	// Don't set the MQTT status too soon, let some time to the agent to connect.
	if time.Since(s.startedAt) < time.Minute {
		return
	}

	// Update the MQTT status when MQTT just became inaccessible.
	shouldUpdateStatus := (s.isMQTTConnected == nil || *s.isMQTTConnected) && !isConnected
	s.isMQTTConnected = &isConnected

	if shouldUpdateStatus {
		s.requestSynchronizationLocked(types.EntityInfo, false)
		s.shouldUpdateMQTTStatus = true
	}
}

// UpdateK8SAgentList requests to update list of Kubernetes agents.
func (s *Synchronizer) UpdateK8SAgentList() {
	s.l.Lock()
	defer s.l.Unlock()

	// Today we don't support updating only some agents, so update all.
	s.requestSynchronizationLocked(types.EntityAgent, true)
}

// requestSynchronizationLocked request specified entity to be synchronized on next synchronization execution.
// Caller must hold the lock s.l.
func (s *Synchronizer) requestSynchronizationLocked(entityName types.EntityName, forceCacheRefresh bool) {
	if forceCacheRefresh {
		s.forceSync[entityName] = types.SyncTypeForceCacheRefresh
	} else if s.forceSync[entityName] == types.SyncTypeNone {
		s.forceSync[entityName] = types.SyncTypeNormal
	}
}

func (s *Synchronizer) canUploadCrashReports() bool {
	s.l.Lock()
	defer s.l.Unlock()

	return s.disableCrashReportUploadUntil.Before(s.now())
}

func (s *Synchronizer) disableCrashReportUpload(duration time.Duration) {
	s.l.Lock()
	defer s.l.Unlock()

	s.disableCrashReportUploadUntil = s.now().Add(duration)
}

// stateHasValue returns whether the given state has a value for the given key.
// If an error occurs, it returns false.
func stateHasValue(key string, state bleemeoTypes.State) bool {
	var value json.RawMessage

	err := state.Get(key, &value)
	if err != nil {
		return false
	}

	return value != nil
}
