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

package synchronizer

import (
	"context"
	cryptoRand "crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/common"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncagent"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncapplications"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncconfig"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/synccontainers"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncdiagnostics"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncfacts"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncgloutonconfig"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncinfo"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncmetrics"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncmonitors"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncregistrationstatus"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncservices"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncsnmp"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncvsphere"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/delay"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/inputs/vsphere"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/exporter/snmp"
	gloutonTypes "github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/version"

	"github.com/getsentry/sentry-go"
	"golang.org/x/oauth2"
)

const (
	agentAuthBrokenCacheKey = "AgentAuthBroken"
	// agentDysfunctionalCacheKey is the flag older versions set on an agent failing for 36 hours,
	// to disable its connector up to an hour. Each entity now waits for its own retry delay, see
	// Execution.recordFailureLocked: Run deletes the flag.
	agentDysfunctionalCacheKey = "AgentDysfunctional"
	// entityFirstFailureCacheKey keeps entityFirstFailureAt across restarts.
	entityFirstFailureCacheKey = "EntityFirstFailure"
	refreshTokenCacheKey       = "RefreshToken"

	// duplicateCheckInterval is the delay between two duplicated agent checks, jittered.
	// The check lists the agent facts registered on the API, which used to happen on every
	// synchronization execution doing any API call. It only needs to detect a state file
	// shared between two Glouton, which isn't urgent, and it's forced anyway before the
	// agent facts get updated.
	duplicateCheckInterval = 20 * time.Minute
)

var (
	ErrBleemeoDisabled            = errors.New("bleemeo connector is disabled")
	errFQDNNotSet                 = errors.New("unable to register, fqdn is not set")
	errConnectorTemporaryDisabled = errors.New("bleemeo connector temporary disabled")
	errBleemeoUndefined           = errors.New("bleemeo.account_id and/or bleemeo.registration_key is undefined. Please see  https://go.bleemeo.com/l/agent-configuration-bleemeo-account ")
	errRegistrationFailed         = errors.New("registration failed")
	errUninitialized              = errors.New("uninitialized")
	errUploadFailed               = errors.New("upload failed")
)

// metricCPUUsed is the CPU metric, the first system metric the agent waits for after its
// registration.
const metricCPUUsed = syncmetrics.MetricCPUUsed

// Synchronizer synchronize object with Bleemeo.
type Synchronizer struct {
	option        types.Option
	now           func() time.Time
	inTest        bool
	synchronizers []types.EntitySynchronizer
	hasFeature    map[types.APIFeature]bool
	// info, diagnostics, snmp, vsphere, monitors and metrics are also in synchronizers, kept for
	// the methods forwarded to them.
	info        *syncinfo.SyncInfo
	diagnostics *syncdiagnostics.SyncDiagnostics
	snmp        *syncsnmp.SyncSNMP
	vsphere     *syncvsphere.SyncVSphere
	monitors    *syncmonitors.SyncMonitors
	metrics     *syncmetrics.SyncMetrics

	requestCounter atomic.Uint32
	// authenticatedSuccessCounter counts the authenticated requests the API answered without
	// error: see Execution.calledAPISuccessfully.
	authenticatedSuccessCounter atomic.Uint32
	// realClient and agentID are only written by the synchronization loop
	// goroutine, while holding l. The synchronization loop may read them
	// without the lock, any other goroutine must hold l to read them.
	realClient *bleemeo.Client

	// These fields should always be set in the reload state after being modified.
	nextFullSync  time.Time
	fullSyncCount int

	startedAt                 time.Time
	syncHeartbeat             time.Time
	heartbeatConsecutiveError int
	lastSuccessfulSync        time.Time
	lastSync                  time.Time
	successiveErrors          int
	// successiveAuthErrors, firstAuthErrorAt and firstRegistrationFailureAt are only used by the
	// synchronization loop.
	successiveAuthErrors       int
	firstAuthErrorAt           time.Time
	firstRegistrationFailureAt time.Time
	maintenanceMode            bool
	suspendedMode              bool
	// See the comment on realClient about the locking of agentID.
	agentID          string
	delayedContainer map[string]time.Time

	l                  sync.Mutex
	disabledUntil      time.Time
	disableReason      bleemeoTypes.DisableReason
	lastDuplicateCheck time.Time
	nextDuplicateCheck time.Time
	forceSync          map[types.EntityName]types.SyncRequest
	// entityLastSync is, per entity, when the execution which last synchronized it successfully
	// started, and entityFailures the number of executions in a row which failed to synchronize
	// it. See Execution.LastSuccessfulSync and Execution.ConsecutiveFailures.
	entityLastSync map[types.EntityName]time.Time
	entityFailures map[types.EntityName]int
	// entityRetryAt is, per failed entity, when it can be synchronized again, and
	// entityFirstFailureAt when the failures in a row started, kept in the state: see
	// Execution.recordResults.
	entityRetryAt        map[types.EntityName]time.Time
	entityFirstFailureAt map[types.EntityName]time.Time
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

	if option.ReloadState != nil {
		if option.ReloadState.NextFullSync().After(nextFullSync) {
			nextFullSync = option.ReloadState.NextFullSync()
		}

		fullSyncCount = option.ReloadState.FullSyncCount()
	}

	s := &Synchronizer{
		option: option,
		now:    now,

		forceSync:      make(map[types.EntityName]types.SyncRequest),
		entityLastSync: make(map[types.EntityName]time.Time),
		entityFailures: make(map[types.EntityName]int),
		entityRetryAt:  make(map[types.EntityName]time.Time),
		nextFullSync:   nextFullSync,
		fullSyncCount:  fullSyncCount,

		entityFirstFailureAt: make(map[types.EntityName]time.Time),
	}

	s.loadEntityFirstFailures()

	s.info = syncinfo.New(&s.requestCounter, s.now)
	s.diagnostics = syncdiagnostics.New(option.State, option.Config.Agent.StateDirectory, s.now)
	s.snmp = syncsnmp.New(option.State, option.SNMP)
	s.vsphere = syncvsphere.New(option.State, option.VSphereDevices, s.now)
	s.monitors = syncmonitors.New(option.Cache, option.State, option.MonitorManager)
	// startedAt is set by Run, and read by the synchronization loop.
	s.metrics = syncmetrics.New(option, s.now, func() time.Time { return s.startedAt })

	s.synchronizers = []types.EntitySynchronizer{
		s.info,
		syncagent.New(),
		syncconfig.New(option.Cache),
		syncfacts.New(s.snmp, s.vsphere, func(ctx context.Context, client types.Client, executionStartedAt time.Time) error {
			return s.checkDuplicatedIfNeeded(ctx, client, executionStartedAt, true)
		}),
		synccontainers.New(s.now),
		s.snmp,
		s.vsphere,
		syncapplications.New(),
		syncservices.New(),
		s.monitors,
		s.metrics,
		syncregistrationstatus.New(option, s.metrics.MetricsState, s.now),
		syncgloutonconfig.New(option.ConfigItems),
		s.diagnostics,
	}

	if s.option.State != nil {
		s.agentID, _ = s.option.State.BleemeoCredentials()
	}

	return s
}

// getClient returns the client used to talk to the Bleemeo API.
// Only the wrapper is new: the underlying API client is shared, with its OAuth
// token and its throttle deadline.
// See the comment on realClient about the locking.
func (s *Synchronizer) getClient() types.Client {
	if s.option.ProvideClient != nil {
		// Allows tests to inject mock clients
		return s.option.ProvideClient()
	}

	return &wrapperClient{client: s.realClient}
}

func (s *Synchronizer) DiagnosticArchive(ctx context.Context, archive gloutonTypes.ArchiveWriter) error {
	// The sync state first: an entity must not keep it out of the archive.
	if err := s.diagnosticSyncState(archive); err != nil {
		return err
	}

	// All the entities, so that one failing does not keep the others out either.
	var errs []error

	for _, entity := range s.synchronizers {
		if archiver, ok := entity.(types.DiagnosticArchiver); ok {
			errs = append(errs, archiver.DiagnosticArchive(ctx, archive))
		}
	}

	return errors.Join(errs...)
}

func (s *Synchronizer) diagnosticSyncState(archive gloutonTypes.ArchiveWriter) error {
	file, err := archive.Create("bleemeo-sync-state.json")
	if err != nil {
		return err
	}

	delayedContainer, _ := s.DelayedContainers()

	s.l.Lock()
	defer s.l.Unlock()

	obj := struct {
		NextFullSync              time.Time
		HeartBeat                 time.Time
		HeartbeatConsecutiveError int
		FullSyncCount             int
		StartedAt                 time.Time
		LastSuccessfulSync        time.Time
		LastSync                  time.Time
		SuccessiveErrors          int
		EntityLastSync            map[types.EntityName]time.Time
		EntityFailures            map[types.EntityName]int
		EntityRetryAt             map[types.EntityName]time.Time
		EntityFirstFailureAt      map[types.EntityName]time.Time
		AuthenticatedSuccesses    uint32
		MaintenanceMode           bool
		AgentID                   string
		DisabledUntil             time.Time
		DisableReason             string
		LastDuplicateCheck        time.Time
		NextDuplicateCheck        time.Time
		ForceSync                 map[types.EntityName]types.SyncRequest
		DelayedContainer          map[string]time.Time
		APIFeatures               map[string]bool
		StateMarkerAuthBroken     time.Time
	}{
		NextFullSync:              s.nextFullSync,
		HeartBeat:                 s.syncHeartbeat,
		HeartbeatConsecutiveError: s.heartbeatConsecutiveError,
		FullSyncCount:             s.fullSyncCount,
		StartedAt:                 s.startedAt,
		LastSuccessfulSync:        s.lastSuccessfulSync,
		LastSync:                  s.lastSync,
		SuccessiveErrors:          s.successiveErrors,
		EntityLastSync:            maps.Clone(s.entityLastSync),
		EntityFailures:            maps.Clone(s.entityFailures),
		EntityRetryAt:             maps.Clone(s.entityRetryAt),
		EntityFirstFailureAt:      maps.Clone(s.entityFirstFailureAt),
		AuthenticatedSuccesses:    s.authenticatedSuccessCounter.Load(),
		MaintenanceMode:           s.maintenanceMode,
		AgentID:                   s.agentID,
		DisabledUntil:             s.disabledUntil,
		DisableReason:             s.disableReason.String(),
		LastDuplicateCheck:        s.lastDuplicateCheck,
		NextDuplicateCheck:        s.nextDuplicateCheck,
		ForceSync:                 s.forceSync,
		DelayedContainer:          delayedContainer,
		APIFeatures:               make(map[string]bool, len(s.hasFeature)),
		StateMarkerAuthBroken:     stateGetTime(agentAuthBrokenCacheKey, s.option.State),
	}

	for k, v := range s.hasFeature {
		obj.APIFeatures[k.String()] = v
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

// syncInfoBeforeFirstExecution syncs info before the synchronization loop, because the MQTT
// connection depends on it (maintenance and agent too old).
// The execution is scoped to this function: it must not be reused by the synchronization loop,
// which has its own execution per run.
func (s *Synchronizer) syncInfoBeforeFirstExecution(ctx context.Context) {
	exec := s.newLimitedExecution(false, map[types.EntityName]types.SyncType{types.EntityInfo: types.SyncTypeNormal})

	// run logs the failure of info.
	_ = exec.run(ctx)
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

	err = s.option.State.Delete(agentDysfunctionalCacheKey)
	if err != nil {
		logger.V(1).Printf("Failed to delete agent broken flag from state cache: %v", err)
	}

	s.syncInfoBeforeFirstExecution(ctx)

	s.option.SetInitialized()

	s.l.Lock()
	s.successiveErrors = 0
	s.l.Unlock()

	s.successiveAuthErrors = 0
	s.firstAuthErrorAt = time.Time{}
	s.firstRegistrationFailureAt = time.Time{}

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

		execution, errSync := s.runOnce(ctx, firstSync)

		s.handleExecutionResult(execution, errSync)

		minimalDelay = delay.JitterDelay(15*time.Second, 0.05)

		if firstSync {
			firstSync = false
		}
	}

	return nil //nolint:nilerr
}

// handleExecutionResult tracks the errors of the synchronization loop's executions, and disables
// the connector for the errors that concern the whole agent.
func (s *Synchronizer) handleExecutionResult(execution *Execution, errSync error) {
	// An authenticated request the API answered shows the agent works with it, and its
	// credentials too: the failing entities only wait for their own retry delay, see
	// Execution.recordResults. An execution that doesn't call the API, like SNMP without targets
	// or nothing to synchronize, shows nothing.
	if execution != nil && execution.calledAPISuccessfully() {
		s.l.Lock()
		s.successiveErrors = 0
		s.l.Unlock()

		s.successiveAuthErrors = 0
		s.firstAuthErrorAt = time.Time{}

		err := s.option.State.Delete(agentAuthBrokenCacheKey)
		if err != nil {
			logger.V(1).Printf("Failed to delete agent broken flag from state cache: %v", err)
		}
	}

	// runOnce returns no execution only when the agent failed to register.
	registrationFailed := execution == nil && errSync != nil

	switch {
	case !registrationFailed:
		s.firstRegistrationFailureAt = time.Time{}
	case s.firstRegistrationFailureAt.IsZero():
		s.firstRegistrationFailureAt = s.now()
	}

	if errSync != nil {
		s.l.Lock()
		s.successiveErrors++
		s.l.Unlock()

		if IsAuthenticationError(errSync) {
			s.successiveAuthErrors++

			if s.firstAuthErrorAt.IsZero() {
				s.firstAuthErrorAt = s.now()
			} else if s.now().Sub(s.firstAuthErrorAt) > 2*time.Hour {
				if !stateHasValue(agentAuthBrokenCacheKey, s.option.State) {
					err := s.option.State.Set(agentAuthBrokenCacheKey, s.firstAuthErrorAt.Format(time.RFC3339))
					if err != nil {
						logger.V(1).Printf("Failed to write agent broken flag to state cache: %v", err)
					}
				}
			}
		} else {
			s.firstAuthErrorAt = time.Time{}

			err := s.option.State.Delete(agentAuthBrokenCacheKey)
			if err != nil {
				logger.V(1).Printf("Failed to delete agent broken flag from state cache: %v", err)
			}
		}

		if IsThrottleError(errSync) {
			deadline := s.throttleDeadline(execution).Add(delay.JitterDelay(15*time.Second, 0.3))
			s.Disable(deadline, bleemeoTypes.DisableTooManyRequests)
		} else {
			s.l.Lock()
			successiveErrors := s.successiveErrors
			s.l.Unlock()

			// Other errors don't disable the whole connector: each failed entity waits for its
			// own retry delay, see Execution.recordResults.
			switch {
			case stateHasValue(agentAuthBrokenCacheKey, s.option.State):
				disableDelay := delay.Exponential(10*time.Minute, 3, successiveErrors, 5*24*time.Hour)
				s.Disable(s.now().Add(disableDelay), bleemeoTypes.DisableAuthenticationError)
				// Ask MQTT to disconnect, but don't block reconnection.
				// They will be blocked anyway because it's a auth error (so we don't have oauth token required to connect to MQTT)
				s.option.DisableCallback(bleemeoTypes.DisableRequestReconnection, s.now().Add(10*time.Second))
			case IsAuthenticationError(errSync):
				disableDelay := delay.JitterDelay(
					delay.Exponential(15*time.Second, 1.55, successiveErrors, 15*time.Minute),
					0.1,
				)

				s.Disable(s.now().Add(disableDelay), bleemeoTypes.DisableTooManyErrors)
			case registrationFailed:
				// No entity ran, so none waits for its retry delay: the registration waits like
				// a failed entity.
				disableDelay := failureRetryDelay(successiveErrors, s.now().Sub(s.firstRegistrationFailureAt))

				s.Disable(s.now().Add(disableDelay), bleemeoTypes.DisableTooManyErrors)
			}

			if IsAuthenticationError(errSync) && s.successiveAuthErrors == 1 {
				// we disable only to trigger a reconnection on MQTT
				s.option.DisableCallback(bleemeoTypes.DisableRequestReconnection, s.now().Add(10*time.Second))
			}
		}

		switch {
		case IsAuthenticationError(errSync) && s.agentID != "":
			fqdnMessage := ""

			fqdn := s.option.Cache.FactsByKey()[s.agentID][syncfacts.FactFQDN].Value
			if fqdn != "" {
				fqdnMessage = " with fqdn " + fqdn
			}

			logger.Printf(
				"Unable to synchronize with Bleemeo: Unable to login with credentials from state.json. Using agent ID %s%s. Was this server deleted on Bleemeo Cloud platform ?",
				s.agentID,
				fqdnMessage,
			)
		case IsAuthenticationError(errSync):
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
				logger.Printf("Unable to synchronize with Bleemeo: %v", errSync)
			} else {
				logger.V(1).Printf("Unable to synchronize with Bleemeo: %v", errSync)
			}
		}
	}
}

// scheduleMetricSync requests a synchronization of the metrics after the given delay, in a new goroutine.
func (s *Synchronizer) scheduleMetricSync(ctx context.Context, delay time.Duration) {
	go func() {
		defer crashreport.ProcessPanic()

		timer := time.NewTimer(delay)
		defer timer.Stop()

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

	s.l.Lock()

	apiClient := s.realClient

	if apiClient == nil {
		// The client is only built for this diagnostic and not stored on the
		// Synchronizer: DiagnosticPage runs on another goroutine and s.realClient
		// is only written by the synchronization loop.
		apiClient, err = s.buildClient()
		if err != nil {
			s.l.Unlock()

			fmt.Fprintf(builder, "Can't initialize the API client: %v\n", err)

			return builder.String()
		}
	}

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

	if lastInfo := s.info.LastInfo(); !lastInfo.FetchedAt.IsZero() {
		bleemeoTime := lastInfo.BleemeoTime()
		delta := lastInfo.TimeDrift()
		fmt.Fprintf(
			builder,
			"Bleemeo /v1/info/ fetched at %v. At this moment, time_drift was %v (time expected was %v)\n",
			lastInfo.FetchedAt.Format(time.RFC3339),
			delta.Truncate(time.Second),
			bleemeoTime.Format(time.RFC3339),
		)
	}

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
	s.requestSynchronizationLocked(types.EntityConfig, true)

	if !immediate {
		return
	}

	s.requestSynchronizationLocked(types.EntityMetric, true)
	s.requestSynchronizationLocked(types.EntityContainer, true)
	s.requestSynchronizationLocked(types.EntityMonitor, true)
}

// LastMetricActivation return the date at which last metric was activated/registrered.
func (s *Synchronizer) LastMetricActivation() time.Time {
	return s.metrics.LastMetricActivation()
}

// UpdateMetrics request to update a specific metrics.
// A single empty UUID requests to update all the metrics.
func (s *Synchronizer) UpdateMetrics(metricUUID ...string) {
	all := s.metrics.UpdateMetrics(metricUUID...)

	s.l.Lock()
	defer s.l.Unlock()

	s.requestSynchronizationLocked(types.EntityMetric, all)
}

// UpdateUnitsAndThresholds updates the units and thresholds of the metrics, from the cache.
func (s *Synchronizer) UpdateUnitsAndThresholds(firstUpdate bool) {
	s.metrics.UpdateUnitsAndThresholds(firstUpdate)
}

// UpdateContainers request to update a containers.
func (s *Synchronizer) UpdateContainers() {
	s.l.Lock()
	defer s.l.Unlock()

	s.requestSynchronizationLocked(types.EntityContainer, false)
}

// UpdateFacts requests to update the agent facts, within delay. The facts synchronization
// also checks whether another Glouton is using the same agent ID.
// A delay of zero requests the update on the next synchronization execution.
func (s *Synchronizer) UpdateFacts(delay time.Duration) {
	s.l.Lock()
	defer s.l.Unlock()

	s.requestLaterSynchronizationLocked(types.EntityFact, false, delay)
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

	s.requestSynchronizationLocked(types.EntityConfig, true)
	s.requestSynchronizationLocked(types.EntityMonitor, true)
}

// UpdateMaintenance requests to check for the maintenance mode again, within delay.
// A delay of zero requests the check on the next synchronization execution.
func (s *Synchronizer) UpdateMaintenance(delay time.Duration) {
	s.l.Lock()
	defer s.l.Unlock()

	s.requestLaterSynchronizationLocked(types.EntityInfo, false, delay)
}

// UpdateAgent requests to check for the agent synchronization, within delay.
// A delay of zero requests the check on the next synchronization execution.
func (s *Synchronizer) UpdateAgent(delay time.Duration) {
	s.l.Lock()
	defer s.l.Unlock()

	s.requestLaterSynchronizationLocked(types.EntityAgent, false, delay)
}

// SetMaintenance allows to trigger the maintenance mode for the synchronize.
// When running in maintenance mode, only the general infos, the agent and its configuration are synced.
func (s *Synchronizer) SetMaintenance(ctx context.Context, maintenance bool) {
	if s.IsMaintenance() && !maintenance {
		// getting out of maintenance, let's check for a duplicated state.json file
		err := s.checkDuplicatedIfNeeded(ctx, s.getClient(), s.now(), true)
		if err != nil {
			// it's not a critical error at all, we will perform this check again on the next synchronization pass
			logger.V(2).Printf("Couldn't check for duplicated agent: %v", err)
		}
	}

	s.l.Lock()
	defer s.l.Unlock()

	s.maintenanceMode = maintenance
}

// SetSuspended sets the suspended mode on the synchronizer.
func (s *Synchronizer) SetSuspended(suspended bool) {
	s.l.Lock()
	defer s.l.Unlock()

	s.suspendedMode = suspended
}

// UpdateMonitor requests to update a monitor, identified by its Monitor ID. It allows for adding, updating and removing a monitor.
func (s *Synchronizer) UpdateMonitor(op string, uuid string) {
	s.monitors.UpdateMonitor(op, uuid)

	s.l.Lock()
	defer s.l.Unlock()

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

// ScheduleDiagnosticUpload stores the given diagnostic until the next synchronization,
// where it will be uploaded to the API.
// If another call to this method is made before the next synchronization,
// only the latest diagnostic will be uploaded.
func (s *Synchronizer) ScheduleDiagnosticUpload(filename, requestToken string, contents []byte) {
	s.diagnostics.ScheduleOnDemand(filename, requestToken, contents)

	s.l.Lock()
	defer s.l.Unlock()

	s.requestSynchronizationLocked(types.EntityDiagnostics, false)
}

func (s *Synchronizer) waitCPUMetric(ctx context.Context) {
	// In test, we skip this waiting time.
	if s.inTest {
		return
	}

	metrics := s.option.Cache.Metrics()
	for _, m := range metrics {
		metricName := gloutonTypes.TextToLabels(m.LabelsText)[gloutonTypes.LabelName]
		if metricName == metricCPUUsed || metricName == "node_cpu_seconds_total" {
			return
		}
	}

	filter := map[string]string{gloutonTypes.LabelName: metricCPUUsed}
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

	if s.now().After(s.disabledUntil) {
		s.disabledUntil = time.Time{}
		s.disableReason = bleemeoTypes.NotDisabled
	}

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

// GetToken returns a valid OAuth token, used as the MQTT password.
// It does no API call: the Bleemeo client renews the token by itself once the expiration
// date sent by the API is reached.
// Should only be called after the synchronized had called SetInitialized and AgentID is filled in State.
func (s *Synchronizer) GetToken(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	disabledUntil, disableReason := s.getDisabledUntil()
	if disableReason == bleemeoTypes.DisableAuthenticationError && s.now().Before(disabledUntil) {
		return "", fmt.Errorf("%w: %s", ErrBleemeoDisabled, disableReason.String())
	}

	// This method is called by the MQTT client, so it doesn't run on the
	// synchronization loop goroutine which owns realClient.
	s.l.Lock()
	hadSyncOnce := !s.lastSync.IsZero()
	apiClient := s.realClient
	s.l.Unlock()

	if !hadSyncOnce && stateHasValue(agentAuthBrokenCacheKey, s.option.State) {
		return "", fmt.Errorf("%w: not yet started", ErrBleemeoDisabled)
	}

	if apiClient == nil {
		return "", errClientUninitialized
	}

	token, err := apiClient.GetToken(ctx)
	if err != nil {
		return "", err
	}

	return token.AccessToken, nil
}

// CheckToken validates our credentials against the Bleemeo API, using a low-cost endpoint.
// It's meant to be called *after* MQTT refused our token, and not before every connection:
// when many agents reconnect at the same time, one API request per agent per connection is
// a significant load on the API for a check that nearly always succeeds.
// The Bleemeo client fetches a new token when the API answers 401, so the next connection
// attempt will use fresh credentials.
func (s *Synchronizer) CheckToken(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	disabledUntil, disableReason := s.getDisabledUntil()
	if disableReason == bleemeoTypes.DisableAuthenticationError && s.now().Before(disabledUntil) {
		return
	}

	// This method is called by the MQTT client, so it doesn't run on the
	// synchronization loop goroutine which owns realClient and agentID.
	s.l.Lock()
	apiClient := s.realClient
	agentID := s.agentID
	s.l.Unlock()

	if apiClient == nil {
		logger.V(1).Printf("Checking Bleemeo API credentials failed: %v", errClientUninitialized)

		return
	}

	result, err := apiClient.Get(ctx, bleemeo.ResourceAgent, agentID, "id")
	if err != nil {
		logger.V(1).Printf("Checking Bleemeo API credentials failed: %v", err)

		return
	}

	var res struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal(result, &res); err != nil {
		logger.V(1).Printf("Checking Bleemeo API credentials failed: %v", err)

		return
	}

	if res.ID != agentID {
		logger.V(1).Printf("Checking Bleemeo API credentials failed: %v", errInvalidAgentID)
	}
}

// setClient creates the API client and stores it on the Synchronizer.
// The caller must hold s.l.
func (s *Synchronizer) setClient() error {
	client, err := s.buildClient()
	if err != nil {
		return err
	}

	s.realClient = client

	return nil
}

// buildClient creates a new API client without storing it.
// The caller must hold s.l.
func (s *Synchronizer) buildClient() (*bleemeo.Client, error) {
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

		AuthenticatedSuccessCounter: &s.authenticatedSuccessCounter,
	}
	cl := &http.Client{
		Transport: gloutonTypes.NewHTTPTransport(tlsConfig, transportOpts),
	}

	return bleemeo.NewClient(
		bleemeo.WithCredentials(username, password),
		bleemeo.WithOAuthClient(gloutonOAuthClientID, ""),
		bleemeo.WithEndpoint(s.option.Config.Bleemeo.APIBase),
		bleemeo.WithNewOAuthTokenCallback(newOAuthTokenCallback),
		bleemeo.WithHTTPClient(cl),
		initialRefreshTokenOpt,
	)
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

// throttleDeadline returns the retry deadline advertised by the API to the
// client used by the given execution.
// execution may be nil when the synchronization failed before it was created,
// which happens when the registration fails; the deadline is then taken from
// the current client, which is the one that did the throttled request.
func (s *Synchronizer) throttleDeadline(execution *Execution) time.Time {
	if execution != nil {
		return execution.client.ThrottleDeadline()
	}

	s.l.Lock()
	defer s.l.Unlock()

	return s.getClient().ThrottleDeadline()
}

// runOnce registers the agent if needed and runs an execution. It returns no execution only when
// the registration failed.
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
				types.EntityConfig: types.SyncTypeForceCacheRefresh,
				types.EntityMetric: types.SyncTypeNormal,
				types.EntityAgent:  types.SyncTypeForceCacheRefresh,
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
	if execution.forceCacheRefreshForAll() && ctx.Err() == nil {
		// The full synchronization is done even when some entities failed: only they are
		// refreshed again, after their retry delay (see Execution.recordResults). Redoing the full
		// synchronization would refresh every entity again, with all their API calls, until the
		// failing ones succeed.
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

	s.l.Lock()

	if err == nil {
		s.lastSuccessfulSync = startAt
	}

	s.lastSync = startAt

	s.l.Unlock()

	return execution, err
}

// checkDuplicatedIfNeeded runs checkDuplicated, unless it already ran during the execution
// started at executionStartedAt or, when force is false, ran recently enough.
//
// It must run *before* any agent fact is updated on the API: the check compares the facts
// registered on the API with the ones this Glouton registered, so updating them first would
// hide a duplication. Callers about to update facts must therefore force the check.
func (s *Synchronizer) checkDuplicatedIfNeeded(
	ctx context.Context, client types.Client, executionStartedAt time.Time, force bool,
) error {
	s.l.Lock()
	shouldCheck := s.shouldCheckDuplicatedLocked(executionStartedAt, force)
	s.l.Unlock()

	if !shouldCheck {
		return nil
	}

	if err := s.checkDuplicated(ctx, client); err != nil {
		return err
	}

	s.l.Lock()
	s.lastDuplicateCheck = s.now()
	s.nextDuplicateCheck = s.lastDuplicateCheck.Add(delay.JitterDelay(duplicateCheckInterval, 0.25))
	s.l.Unlock()

	return nil
}

// shouldCheckDuplicatedLocked tells whether the duplicated agent check should run now.
// It never runs twice during the same synchronization execution. Outside of that, force makes
// it run regardless of when it last ran.
// Caller must hold the lock s.l.
func (s *Synchronizer) shouldCheckDuplicatedLocked(executionStartedAt time.Time, force bool) bool {
	if !s.lastDuplicateCheck.Before(executionStartedAt) {
		return false
	}

	return force || !s.now().Before(s.nextDuplicateCheck)
}

// checkDuplicated checks if another glouton is running with the same ID.
func (s *Synchronizer) checkDuplicated(ctx context.Context, client types.Client) error {
	oldFacts := s.option.Cache.FactsByKey()

	if err := syncfacts.UpdateList(ctx, client, s.option.Cache); err != nil {
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
	factNames := []string{syncfacts.FactFQDN, "primary_address", "primary_mac_address", syncfacts.FactGloutonPID}
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

		if name == syncfacts.FactGloutonPID {
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

	fqdn := facts[syncfacts.FactFQDN]
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

	agentID, err := s.getClient().RegisterSelf(ctx, accountID, password, s.option.Config.Bleemeo.InitialServerGroupName, name, fqdn, registrationKey)
	if err != nil {
		return err
	}

	s.l.Lock()
	s.agentID = agentID
	s.l.Unlock()

	sentry.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetContext("agent", map[string]any{
			"agent_id":                   agentID,
			syncfacts.FactGloutonVersion: version.Version,
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

func (s *Synchronizer) SetMQTTConnected(isConnected bool) {
	s.l.Lock()
	defer s.l.Unlock()

	// Don't set the MQTT status too soon, let some time to the agent to connect.
	if s.now().Sub(s.startedAt) < time.Minute {
		return
	}

	// Update the MQTT status when MQTT just became inaccessible.
	if s.info.SetMQTTConnected(isConnected) {
		s.requestSynchronizationLocked(types.EntityInfo, false)
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
	s.requestLaterSynchronizationLocked(entityName, forceCacheRefresh, 0)
}

// requestLaterSynchronizationLocked request specified entity to be synchronized within delay,
// that is at the latest at now+delay. The synchronization might happen earlier, in which case
// the request is fulfilled: see types.SyncRequest.Deadline.
// A delay of zero requests the synchronization on the next synchronization execution.
//
// When a request already exists for that entity, the strongest sync type and the earliest
// deadline win: asking again for a later synchronization never postpones a pending request.
// Caller must hold the lock s.l.
func (s *Synchronizer) requestLaterSynchronizationLocked(entityName types.EntityName, forceCacheRefresh bool, delay time.Duration) {
	deadline := s.now().Add(delay)

	request, ok := s.forceSync[entityName]

	if forceCacheRefresh {
		request.Type = types.SyncTypeForceCacheRefresh
	} else if request.Type == types.SyncTypeNone {
		request.Type = types.SyncTypeNormal
	}

	if !ok || deadline.Before(request.Deadline) {
		request.Deadline = deadline
	}

	s.forceSync[entityName] = request
}

// loadEntityFirstFailures reads from the state when the failing entities started failing, so
// that an entity failing for longStandingFailure before a restart still waits up to
// longStandingMaxRetryDelay.
func (s *Synchronizer) loadEntityFirstFailures() {
	if s.option.State == nil {
		return
	}

	var firstFailures map[types.EntityName]time.Time

	err := s.option.State.Get(entityFirstFailureCacheKey, &firstFailures)
	if err != nil {
		logger.V(1).Printf("Failed to read the entities' first failures from state cache: %v", err)

		return
	}

	maps.Copy(s.entityFirstFailureAt, firstFailures)
}

// saveEntityFirstFailuresLocked writes entityFirstFailureAt to the state, see loadEntityFirstFailures.
// Caller must hold the lock s.l.
func (s *Synchronizer) saveEntityFirstFailuresLocked() {
	if s.option.State == nil {
		return
	}

	var err error

	if len(s.entityFirstFailureAt) == 0 {
		err = s.option.State.Delete(entityFirstFailureCacheKey)
	} else {
		err = s.option.State.Set(entityFirstFailureCacheKey, maps.Clone(s.entityFirstFailureAt))
	}

	if err != nil {
		logger.V(1).Printf("Failed to write the entities' first failures to state cache: %v", err)
	}
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

// stateGetTime returns whether the given state has a value for the given key.
// If an error occurs, it returns false.
func stateGetTime(key string, state bleemeoTypes.State) time.Time {
	var value time.Time

	err := state.Get(key, &value)
	if err != nil {
		return time.Time{}
	}

	return value
}

// ApplyMonitorUpdate gives the monitors of the cache to the blackbox prober.
func (s *Synchronizer) ApplyMonitorUpdate() error {
	return s.monitors.ApplyMonitorUpdate()
}

// FindSNMPAgent returns the agent of the SNMP target, among agentsByID. snmpType is the ID of
// the SNMP agent type.
func (s *Synchronizer) FindSNMPAgent(ctx context.Context, target *snmp.Target, snmpType string, agentsByID map[string]bleemeoTypes.Agent) (bleemeoTypes.Agent, error) {
	return s.snmp.FindAgent(ctx, target, snmpType, agentsByID)
}

// FindVSphereAgent returns the agent of the vSphere device, among agentsByID. agentTypeID is the
// ID of the agent type of the device.
func (s *Synchronizer) FindVSphereAgent(ctx context.Context, device bleemeoTypes.VSphereDevice, agentTypeID string, agentsByID map[string]bleemeoTypes.Agent) (bleemeoTypes.Agent, error) {
	return s.vsphere.FindAgent(ctx, device, agentTypeID, agentsByID)
}

// GetVSphereAgentTypes returns a map[vSphereKind]=>AgentTypeID of all vSphere resource kinds.
func (s *Synchronizer) GetVSphereAgentTypes() (map[vsphere.ResourceKind]string, bool) {
	return syncvsphere.AgentTypes(s.option.Cache)
}

func (s *Synchronizer) GetVSphereAgentType(kind vsphere.ResourceKind) (agentTypeID string, found bool) {
	return syncvsphere.AgentType(s.option.Cache, kind)
}
