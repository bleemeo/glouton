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
	cryptoRand "crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/bleemeo/client"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/common"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/delay"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/threshold"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/version"

	"github.com/getsentry/sentry-go"
)

var (
	errFQDNNotSet                 = errors.New("unable to register, fqdn is not set")
	errConnectorTemporaryDisabled = errors.New("bleemeo connector temporary disabled")
	errBleemeoUndefined           = errors.New("bleemeo.account_id and/or bleemeo.registration_key is undefined. Please see  https://go.bleemeo.com/l/agent-configuration-bleemeo-account ")
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
	syncMethodVSphere       = "vsphere"
	syncMethodFact          = "facts"
	syncMethodService       = "service"
	syncMethodContainer     = "container"
	syncMethodMetric        = "metric"
	syncMethodConfig        = "config"
	syncMethodDiagnostics   = "diagnostics"
)

// Synchronizer synchronize object with Bleemeo.
type Synchronizer struct {
	ctx    context.Context //nolint:containedctx
	option Option
	now    func() time.Time
	inTest bool

	realClient       *client.HTTPClient
	client           *wrapperClient
	diagnosticClient *http.Client

	// These fields should always be set in the reload state after being modified.
	nextFullSync  time.Time
	fullSyncCount int

	startedAt                 time.Time
	syncHeartbeat             time.Time
	heartbeatConsecutiveError int
	lastSync                  time.Time
	lastFactUpdatedAt         string
	lastSNMPcount             int
	lastVSphereUpdate         time.Time
	lastVSphereAgentsPurge    time.Time
	lastMetricActivation      time.Time
	successiveErrors          int
	warnAccountMismatchDone   bool
	maintenanceMode           bool
	suspendedMode             bool
	callUpdateLabels          bool
	lastMetricCount           int
	currentConfigNotified     string
	agentID                   string

	onDemandDiagnostic     synchronizerOnDemandDiagnostic
	onDemandDiagnosticLock sync.Mutex

	// configSyncDone is true when the config items were successfully synced.
	configSyncDone bool

	// An edge case occurs when an agent is spawned while the maintenance mode is enabled on the backend:
	// the agent cannot register agent_status, thus the MQTT connector cannot start, and we cannot receive
	// notifications to tell us the backend is out of maintenance. So we resort to HTTP polling every 15
	// minutes to check whether we are still in maintenance of not.
	lastMaintenanceSync time.Time

	// logOnce is used to log that the limit of metrics has been reached.
	logOnce             sync.Once
	lastDenyReasonLogAt time.Time

	l                      sync.Mutex
	disabledUntil          time.Time
	disableReason          bleemeoTypes.DisableReason
	forceSync              map[string]bool
	pendingMetricsUpdate   []string
	pendingMonitorsUpdate  []MonitorUpdate
	thresholdOverrides     map[thresholdOverrideKey]threshold.Threshold
	delayedContainer       map[string]time.Time
	retryableMetricFailure map[bleemeoTypes.FailureKind]bool
	metricRetryAt          time.Time
	lastInfo               bleemeoTypes.GlobalInfo
	// Whether the agent MQTT status should be synced. This is used to avoid syncing
	// the MQTT status too soon before the agent has tried to connect to MQTT.
	shouldUpdateMQTTStatus bool
	// Whether the agent is connected to MQTT. We use a pointer to know if the field is set.
	isMQTTConnected *bool
}

type thresholdOverrideKey struct {
	MetricName string
	AgentID    string
}

// Option are parameters for the synchronizer.
type Option struct {
	bleemeoTypes.GlobalOption
	Cache        *cache.Cache
	PushAppender *model.BufferAppender

	// DisableCallback is a function called when Synchronizer request Bleemeo connector to be disabled
	// reason state why it's disabled and until set for how long it should be disabled.
	DisableCallback func(reason bleemeoTypes.DisableReason, until time.Time)

	// UpdateConfigCallback is a function called when Synchronizer detected a AccountConfiguration change
	UpdateConfigCallback func(ctx context.Context, nameChanged bool)

	// SetInitialized tells the bleemeo connector that the MQTT module can be started
	SetInitialized func()

	// IsMqttConnected returns whether the MQTT connector is operating nominally, and specifically
	// that it can receive mqtt notifications. It is useful for the fallback on http polling
	// described above Synchronizer.lastMaintenanceSync definition.
	// Note: returns false when the mqtt connector is not enabled.
	IsMqttConnected func() bool

	// SetBleemeoInMaintenanceMode makes the bleemeo connector wait a day before checking again for maintenance.
	SetBleemeoInMaintenanceMode func(maintenance bool)

	// SetBleemeoInSuspendedMode sets the suspended mode. While Bleemeo is suspended the agent doesn't
	// create or update objects on the API and stops sending points on MQTT. The suspended mode differs
	// from the maintenance mode because we stop buffering points to send on MQTT and just drop them.
	SetBleemeoInSuspendedMode func(suspended bool)
}

// New return a new Synchronizer.
func New(option Option) (*Synchronizer, error) {
	return newWithNow(option, time.Now)
}

func newForTest(option Option, now func() time.Time) (*Synchronizer, error) {
	s, err := newWithNow(option, now)
	if err != nil {
		return s, err
	}

	s.inTest = true

	return s, nil
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

	s.agentID, _ = s.option.State.BleemeoCredentials()

	return s, nil
}

func (s *Synchronizer) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("bleemeo-sync-state.json")
	if err != nil {
		return err
	}

	s.l.Lock()
	defer s.l.Unlock()

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
		ThresholdOverrides         string
	}{
		NextFullSync:               s.nextFullSync,
		HeartBeat:                  s.syncHeartbeat,
		HeartbeatConsecutiveError:  s.heartbeatConsecutiveError,
		FullSyncCount:              s.fullSyncCount,
		StartedAt:                  s.startedAt,
		LastSync:                   s.lastSync,
		LastFactUpdatedAt:          s.lastFactUpdatedAt,
		LastMetricActivation:       s.lastMetricActivation,
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
		ThresholdOverrides:         fmt.Sprintf("%v", s.thresholdOverrides),
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

// Run runs the Synchronizer.
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

	cfg, ok := s.option.Cache.CurrentAccountConfig()
	if ok {
		s.currentConfigNotified = cfg.ID
	}

	// syncInfo early because MQTT connection will establish or not depending on it (maintenance & outdated agent).
	// syncInfo also disable if time drift is too big. We don't do this disable now for a new agent, because
	// we want it to perform registration and creation of agent_status in order to mark this agent as "bad time" on Bleemeo.
	_, err := s.syncInfoReal(ctx, !firstSync)
	if err != nil {
		logger.V(1).Printf("bleemeo: pre-run checks: couldn't sync the global config: %v", err)
	}

	s.option.SetInitialized()

	s.l.Lock()
	s.successiveErrors = 0
	s.l.Unlock()

	successiveAuthErrors := 0

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
			s.l.Lock()
			s.successiveErrors++
			s.l.Unlock()

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
					fqdnMessage = " with fqdn " + fqdn
				}

				logger.Printf(
					"Unable to synchronize with Bleemeo: Unable to login with credentials from state.json. Using agent ID %s%s. Was this server deleted on Bleemeo Cloud platform ?",
					s.agentID,
					fqdnMessage,
				)
			case client.IsAuthError(err):
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
			s.l.Unlock()

			successiveAuthErrors = 0
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
			_, err := s.syncMetrics(ctx, false, false)
			if err != nil {
				logger.V(1).Printf("Delayed metrics sync failed: %v", err)
			}
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

	if s.diagnosticClient == nil {
		s.diagnosticClient = &http.Client{
			Transport: types.NewHTTPTransport(tlsConfig),
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
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
	s.forceSync[syncMethodFact] = true
	s.forceSync[syncMethodAccountConfig] = true

	if !immediate {
		return
	}

	s.forceSync[syncMethodMetric] = true
	s.forceSync[syncMethodContainer] = true
	s.forceSync[syncMethodMonitor] = true
}

// LastMetricActivation return the date at which last metric was activated/registrered.
func (s *Synchronizer) LastMetricActivation() time.Time {
	s.l.Lock()
	defer s.l.Unlock()

	return s.lastMetricActivation
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
	// In test, we skip this waiting time.
	if s.inTest {
		return
	}

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

// GetToken to talk to the Bleemeo API.
func (s *Synchronizer) GetToken(ctx context.Context) (string, error) {
	return s.realClient.VerifyAndGetToken(ctx, s.agentID)
}

func (s *Synchronizer) setClient() error {
	username := s.agentID + "@bleemeo.com"
	_, password := s.option.State.BleemeoCredentials()

	client, err := client.NewClient(
		s.option.Config.Bleemeo.APIBase,
		username,
		password,
		s.option.Config.Bleemeo.APISSLInsecure,
		s.option.ReloadState,
	)
	if err != nil {
		return err
	}

	s.realClient = client

	return nil
}

// HealthCheck perform some health check and log any issue found.
// This method could panic when health condition are bad for too long in order to cause a Glouton restart.
func (s *Synchronizer) HealthCheck() bool {
	s.l.Lock()
	defer s.l.Unlock()

	syncHearthbeat := s.syncHeartbeat
	if syncHearthbeat.IsZero() {
		syncHearthbeat = s.startedAt
	}

	if s.now().Before(s.disabledUntil) {
		// synchronization is still disabled, don't check syncHearthbeat
		return true
	}

	if s.now().Sub(syncHearthbeat) > time.Hour {
		logger.Printf("Bleemeo API communication didn't run since %s. This should be a Glouton bug", syncHearthbeat.Format(time.RFC3339))

		if s.now().Sub(syncHearthbeat) > 6*time.Hour {
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

func (s *Synchronizer) runOnce(ctx context.Context, onlyEssential bool) (map[string]bool, error) {
	var wasCreation, updateThresholds bool

	startAt := s.now()

	s.l.Lock()
	s.syncHeartbeat = time.Now()
	s.l.Unlock()

	if s.agentID == "" {
		if err := s.register(ctx); err != nil {
			return nil, err
		}

		wasCreation = true

		s.option.NotifyFirstRegistration()
		// Do one pass of metric registration to register agent_status.
		// MQTT connection require this metric to exists before connecting
		_, _ = s.syncMetrics(ctx, false, true)

		// Also do syncAgent, because agent configuration is also required for MQTT connection
		_, _ = s.syncAgent(ctx, false, true)

		// Then wait CPU (which should arrive the all other system metrics)
		// before continuing to process.
		s.waitCPUMetric(ctx)
	}

	syncMethods, fullsync := s.syncToPerform(ctx)

	if len(syncMethods) == 0 {
		return syncMethods, nil
	}

	s.client = &wrapperClient{
		s:      s,
		client: s.realClient,
	}
	previousCount := s.realClient.RequestsCount()

	syncStep := []struct {
		name                   string
		method                 func(ctx context.Context, fullSync, onlyEssential bool) (updateThresholds bool, err error)
		enabledInMaintenance   bool
		enabledInSuspendedMode bool
		skipOnlyEssential      bool // should be true for method that ignore onlyEssential
	}{
		{name: syncMethodInfo, method: s.syncInfo, enabledInMaintenance: true, skipOnlyEssential: true},
		{name: syncMethodAgent, method: s.syncAgent, enabledInSuspendedMode: true, skipOnlyEssential: true},
		{name: syncMethodAccountConfig, method: s.syncAccountConfig, enabledInSuspendedMode: true, skipOnlyEssential: true},
		{name: syncMethodFact, method: s.syncFacts},
		{name: syncMethodContainer, method: s.syncContainers},
		{name: syncMethodSNMP, method: s.syncSNMP},
		{name: syncMethodVSphere, method: s.syncVSphere},
		{name: syncMethodService, method: s.syncServices},
		{name: syncMethodMonitor, method: s.syncMonitors, skipOnlyEssential: true},
		{name: syncMethodMetric, method: s.syncMetrics},
		{name: syncMethodConfig, method: s.syncConfig},
		{name: syncMethodDiagnostics, method: s.syncDiagnostics},
	}

	var firstErr error

	for _, step := range syncStep {
		if ctx.Err() != nil {
			return syncMethods, ctx.Err()
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

		if (s.IsMaintenance() && !step.enabledInMaintenance) || (s.suspendedMode && !step.enabledInSuspendedMode) {
			// Store the fact that we must sync this step when we will no longer be in maintenance:
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

		if full, ok := syncMethods[step.name]; ok {
			updateThresholdsTmp, err := step.method(ctx, full, onlyEssential && !step.skipOnlyEssential)
			if err != nil {
				logger.V(1).Printf("Synchronization for object %s failed: %v", step.name, err)

				if firstErr == nil {
					firstErr = err
				} else if !client.IsAuthError(firstErr) && client.IsAuthError(err) {
					// Prefer returning Authentication error than other errors.
					firstErr = err
				}
			}

			updateThresholds = updateThresholds || updateThresholdsTmp

			if onlyEssential && !step.skipOnlyEssential {
				// We registered only essential object. Make sure all other
				// objects are registered on the second run.
				s.l.Lock()
				s.forceSync[step.name] = false || s.forceSync[step.name]
				s.l.Unlock()
			}
		}
	}

	if s.callUpdateLabels {
		s.callUpdateLabels = false
		if s.option.NotifyLabelsUpdate != nil {
			s.option.NotifyLabelsUpdate()
		}
	}

	logger.V(2).Printf("Synchronization took %v for %v (and did %d requests)", s.now().Sub(startAt), syncMethods, s.realClient.RequestsCount()-previousCount)

	if wasCreation {
		s.UpdateUnitsAndThresholds(ctx, true)
	} else if updateThresholds {
		s.UpdateUnitsAndThresholds(ctx, false)
	}

	if fullsync && firstErr == nil {
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

	return syncMethods, firstErr
}

// syncToPerform returns the methods that should be synced in a map. For each method
// in the map, the value indicates whether a fullsync should be performed.
// It also returns true if the current sync is a full sync.
func (s *Synchronizer) syncToPerform(ctx context.Context) (map[string]bool, bool) {
	// Take values that will be used later before taking the lock. This reduce dead-lock risk
	localFacts, _ := s.option.Facts.Facts(ctx, 24*time.Hour)
	currentSNMPCount := s.option.SNMPOnlineTarget()
	lastVSphereChange := s.option.LastVSphereChange(ctx)
	lastDiscovery := s.option.Discovery.LastUpdate()
	currentMetricCount := s.option.Store.MetricsCount()
	mqttIsConnected := s.option.IsMqttConnected()
	lastAnnotationChange := s.option.LastMetricAnnotationChange()

	s.l.Lock()
	defer s.l.Unlock()

	syncMethods := make(map[string]bool)

	fullSync := false
	if s.nextFullSync.Before(s.now()) {
		fullSync = true
	}

	agent := s.option.Cache.Agent()

	nextConfigAt := agent.NextConfigAt
	if !nextConfigAt.IsZero() && nextConfigAt.Before(s.now()) {
		fullSync = true
	}

	if s.currentConfigNotified != agent.CurrentConfigID {
		syncMethods[syncMethodAccountConfig] = true
	}

	if fullSync {
		syncMethods[syncMethodInfo] = true
		syncMethods[syncMethodAgent] = true
		syncMethods[syncMethodAccountConfig] = true
		syncMethods[syncMethodMonitor] = true
		syncMethods[syncMethodSNMP] = true
		syncMethods[syncMethodVSphere] = true
		syncMethods[syncMethodDiagnostics] = true
	}

	if fullSync || s.lastFactUpdatedAt != localFacts[facts.FactUpdatedAt] {
		syncMethods[syncMethodFact] = fullSync
	}

	if s.lastSNMPcount != currentSNMPCount {
		syncMethods[syncMethodFact] = fullSync
		syncMethods[syncMethodSNMP] = fullSync
		// TODO: this isn't idea. If the synchronization fail, it won't be retried.
		// I think the ideal fix would be to always retry all syncMethods that was to synchronize but failed.
		s.lastSNMPcount = currentSNMPCount
	}

	if lastVSphereChange.After(s.lastVSphereUpdate) {
		syncMethods[syncMethodVSphere] = true
		s.lastVSphereUpdate = lastVSphereChange
	}

	// After a reload, the config has been changed, so we want to do a fullsync
	// without waiting the nextFullSync that is kept between reload.
	if fullSync || !s.configSyncDone {
		syncMethods[syncMethodConfig] = true
	}

	minDelayed := time.Time{}

	for _, delay := range s.delayedContainer {
		if minDelayed.IsZero() || delay.Before(minDelayed) {
			minDelayed = delay
		}
	}

	if fullSync || s.lastSync.Before(lastDiscovery) || (!minDelayed.IsZero() && s.now().After(minDelayed)) {
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

	if fullSync || s.now().After(s.metricRetryAt) || s.lastSync.Before(lastDiscovery) || s.lastSync.Before(lastAnnotationChange) || s.lastMetricCount != currentMetricCount {
		syncMethods[syncMethodMetric] = fullSync
	}

	// when the mqtt connector is not connected, we cannot receive notifications to get out of maintenance
	// mode, so we poll more often.
	if s.maintenanceMode && !mqttIsConnected && s.now().After(s.lastMaintenanceSync.Add(15*time.Minute)) {
		s.forceSync[syncMethodInfo] = false

		s.lastMaintenanceSync = s.now()
	}

	for k, full := range s.forceSync {
		syncMethods[k] = full || syncMethods[k]
		delete(s.forceSync, k)
	}

	if len(syncMethods) > 0 && s.now().Sub(s.lastInfo.FetchedAt) > 30*time.Minute {
		// Ensure lastInfo is enough up-to-date.
		// This will help detection quickly a change on /v1/info/ and will ensure the
		// metric time_drift is updated recently to avoid unwanted deactivation.
		syncMethods[syncMethodInfo] = false || syncMethods[syncMethodInfo]
	}

	return syncMethods, fullSync
}

// checkDuplicated checks if another glouton is running with the same ID.
func (s *Synchronizer) checkDuplicated(ctx context.Context) error {
	oldFacts := s.option.Cache.FactsByKey()

	if err := s.factsUpdateList(ctx); err != nil {
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
	params := map[string]string{
		"fields": "last_duplication_date",
	}

	data := map[string]time.Time{
		"last_duplication_date": time.Now(),
	}

	_, err := s.client.Do(s.ctx, "PATCH", fmt.Sprintf("v1/agent/%s/", s.agentID), params, data, nil)
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

	var objectID struct {
		ID string
	}
	// We save an empty agent_uuid before doing the API POST to validate that
	// State can save value.
	if err := s.option.State.SetBleemeoCredentials("", ""); err != nil {
		return err
	}

	statusCode, err := s.realClient.PostAuth(
		ctx,
		"v1/agent/",
		map[string]string{
			"account":                   accountID,
			"initial_password":          password,
			"initial_server_group_name": s.option.Config.Bleemeo.InitialServerGroupName,
			"display_name":              name,
			"fqdn":                      fqdn,
		},
		accountID+"@bleemeo.com",
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

	sentry.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetContext("agent", map[string]interface{}{
			"agent_id":        s.agentID,
			"glouton_version": version.Version,
		})
	})

	if err := s.option.State.SetBleemeoCredentials(objectID.ID, password); err != nil {
		logger.Printf("Failed to persist Bleemeo credentials. The agent may register itself multiple-time: %v", err)
	}

	logger.V(1).Printf("Registration successful with UUID %v", objectID.ID)

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
		s.forceSync[syncMethodInfo] = false || s.forceSync[syncMethodInfo]
		s.shouldUpdateMQTTStatus = true
	}
}
