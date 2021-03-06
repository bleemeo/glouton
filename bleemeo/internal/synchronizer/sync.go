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
	"errors"
	"fmt"
	"glouton/bleemeo/client"
	"glouton/bleemeo/internal/cache"
	"glouton/bleemeo/internal/common"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/logger"
	"glouton/types"
	"math"
	"math/big"
	"math/rand"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var errFQDNNotSet = errors.New("unable to register, fqdn is not set")
var errConnectorTemporaryDisabled = errors.New("bleemeo connector temporary disabled")
var errBleemeoUndefined = errors.New("bleemeo.account_id and/or bleemeo.registration_key is undefined. Please see  https://docs.bleemeo.com/agent/configuration#bleemeoaccount_id ")
var errIncorrectStatusCode = errors.New("registration status code is")

// Synchronizer synchronize object with Bleemeo.
type Synchronizer struct {
	ctx    context.Context
	option Option
	now    func() time.Time

	client       *client.HTTPClient
	nextFullSync time.Time

	startedAt               time.Time
	lastSync                time.Time
	lastFactUpdatedAt       string
	successiveErrors        int
	warnAccountMismatchDone bool
	maintenanceMode         bool
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
	UpdateConfigCallback func()

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
func New(option Option) *Synchronizer {
	return &Synchronizer{
		option: option,
		now:    time.Now,

		forceSync:              make(map[string]bool),
		nextFullSync:           time.Now(),
		retryableMetricFailure: make(map[bleemeoTypes.FailureKind]bool),
	}
}

// Run run the Connector.
//nolint: gocyclo
func (s *Synchronizer) Run(ctx context.Context) error {
	s.ctx = ctx
	s.startedAt = s.now()

	if err := s.option.State.Get("agent_uuid", &s.agentID); err != nil {
		return err
	}

	firstSync := true

	if s.agentID != "" {
		logger.V(1).Printf("This agent is registered on Bleemeo Cloud platform with UUID %v", s.agentID)

		firstSync = false
	}

	if err := s.setClient(); err != nil {
		//TODO: an error occurs with the linter as of v1.27. This is fixed in the latest updates.
		return fmt.Errorf("unable to create Bleemeo HTTP client. Is the API base URL correct ? (error is %w)", err) //nolint: goerr113
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

		minimalDelay = common.JitterDelay(20, 0.5, 20)
	}

	for s.ctx.Err() == nil {
		// TODO: allow WaitDeadline to be interrupted when new metrics arrive
		common.WaitDeadline(s.ctx, minimalDelay, s.getDisabledUntil, "Synchronization with Bleemeo Cloud platform")

		if s.ctx.Err() != nil {
			break
		}

		err := s.runOnce(firstSync)
		if err != nil {
			s.successiveErrors++

			if client.IsAuthError(err) {
				successiveAuthErrors++
			}

			switch {
			case client.IsAuthError(err) && successiveAuthErrors >= 3:
				delay := common.JitterDelay(60*math.Pow(1.55, float64(successiveAuthErrors)), 0.1, 21600)
				s.option.DisableCallback(bleemeoTypes.DisableAuthenticationError, s.now().Add(delay))
			case client.IsThrottleError(err):
				deadline := s.client.ThrottleDeadline().Add(common.JitterDelay(15, 0.3, 15))
				s.Disable(deadline, bleemeoTypes.DisableTooManyRequests)
			default:
				delay := common.JitterDelay(15*math.Pow(1.55, float64(s.successiveErrors)), 0.1, 900)
				s.Disable(s.now().Add(delay), bleemeoTypes.DisableTooManyErrors)

				if client.IsAuthError(err) && successiveAuthErrors == 1 {
					// we disable only to trigger a reconnection on MQTT
					s.option.DisableCallback(bleemeoTypes.DisableAuthenticationError, s.now().Add(10*time.Second))
				}
			}

			switch {
			case client.IsAuthError(err) && s.agentID != "":
				fqdnMessage := ""

				fqdn := s.option.Cache.FactsByKey()["fqdn"].Value
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
			minimalDelay = common.JitterDelay(15, 0.05, 15)
		}

		if firstSync {
			if err == nil {
				minimalDelay = common.JitterDelay(1, 0.05, 1)
			}

			firstSync = false
		}
	}

	return nil
}

// DiagnosticPage return useful information to troubleshoot issue.
func (s *Synchronizer) DiagnosticPage() string {
	builder := &strings.Builder{}

	var tlsConfig *tls.Config

	u, err := url.Parse(s.option.Config.String("bleemeo.api_base"))
	if err != nil {
		fmt.Fprintf(builder, "Bad URL %#v: %v\n", s.option.Config.String("bleemeo.api_base"), err)
		return builder.String()
	}

	port := 80

	if u.Scheme == "https" {
		tlsConfig = &tls.Config{
			InsecureSkipVerify: s.option.Config.Bool("bleemeo.api_ssl_insecure"), // nolint: gosec
		}
		port = 443
	}

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
		httpMessage <- common.DiagnosticHTTP(u.String(), tlsConfig)
	}()

	builder.WriteString(<-tcpMessage)
	builder.WriteString(<-httpMessage)

	s.l.Lock()
	if !s.lastInfo.FetchedAt.IsZero() {
		bleemeoTime := s.lastInfo.BleemeoTime()
		delta := s.lastInfo.TimeDrift()
		builder.WriteString(fmt.Sprintf(
			"Bleemeo /v1/info/ fetched at %v. At this moment, time_dift was %v (time expected was %v)\n",
			s.lastInfo.FetchedAt.Format(time.RFC3339),
			delta.Truncate(time.Second),
			bleemeoTime.Format(time.RFC3339),
		))
	}
	s.l.Unlock()

	return builder.String()
}

// NotifyConfigUpdate notify that an Agent configuration change occurred.
// If immediate is true, the configuration change already happened, else it will happen.
func (s *Synchronizer) NotifyConfigUpdate(immediate bool) {
	s.l.Lock()
	defer s.l.Unlock()

	s.forceSync["info"] = true
	s.forceSync["agent"] = true

	if !immediate {
		return
	}

	s.forceSync["metrics"] = true
	s.forceSync["containers"] = true
	s.forceSync["monitors"] = true
}

// UpdateMetrics request to update a specific metrics.
func (s *Synchronizer) UpdateMetrics(metricUUID ...string) {
	s.l.Lock()
	defer s.l.Unlock()

	if len(metricUUID) == 1 && metricUUID[0] == "" {
		// We don't known the metric to update. Update all
		s.forceSync["metrics"] = true
		s.pendingMetricsUpdate = nil

		return
	}

	s.pendingMetricsUpdate = append(s.pendingMetricsUpdate, metricUUID...)

	s.forceSync["metrics"] = false
}

// UpdateContainers request to update a containers.
func (s *Synchronizer) UpdateContainers() {
	s.l.Lock()
	defer s.l.Unlock()

	s.forceSync["containers"] = false
}

// UpdateInfo request to update a info, which include the time_drift.
func (s *Synchronizer) UpdateInfo() {
	s.l.Lock()
	defer s.l.Unlock()

	s.forceSync["info"] = false
}

// UpdateMonitors requests to update all the monitors.
func (s *Synchronizer) UpdateMonitors() {
	s.l.Lock()
	defer s.l.Unlock()

	s.forceSync["monitors"] = true
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

func (s *Synchronizer) waitCPUMetric() {
	metrics := s.option.Cache.Metrics()
	for _, m := range metrics {
		if m.Labels[types.LabelName] == "cpu_used" || m.Labels[types.LabelName] == "node_cpu_seconds_total" {
			return
		}
	}

	filter := map[string]string{types.LabelName: "cpu_used"}
	filter2 := map[string]string{types.LabelName: "node_cpu_seconds_total"}
	count := 0

	for s.ctx.Err() == nil && count < 20 {
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
		case <-s.ctx.Done():
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

func (s *Synchronizer) setClient() error {
	username := fmt.Sprintf("%s@bleemeo.com", s.agentID)

	var password string

	if err := s.option.State.Get("password", &password); err != nil {
		return err
	}

	client, err := client.NewClient(s.ctx, s.option.Config.String("bleemeo.api_base"), username, password, s.option.Config.Bool("bleemeo.api_ssl_insecure"))
	if err != nil {
		return err
	}

	s.client = client

	return nil
}

//nolint: gocyclo
func (s *Synchronizer) runOnce(onlyEssential bool) error {
	if s.agentID == "" {
		if err := s.register(); err != nil {
			return err
		}

		s.option.NotifyFirstRegistration(s.ctx)
		// Do one pass of metric registration to register agent_status.
		// MQTT connection require this metric to exists before connecting
		_ = s.syncMetrics(false, true)

		// Also do syncAgent, because agent configuration is also required for MQTT connection
		_ = s.syncAgent(false, true)

		// Then wait CPU (which should arrive the all other system metrics)
		// before continuing to process.
		s.waitCPUMetric()
	}

	syncMethods := s.syncToPerform()

	if len(syncMethods) == 0 {
		return nil
	}

	// We do not perform this check in maintennace mode (otherwise we could end up checking it every 15 econds),
	// we will only do it once when getting out of the maintenance mode
	if !s.IsMaintenance() {
		if err := s.checkDuplicated(); err != nil {
			return err
		}
	}

	syncStep := []struct {
		name                 string
		method               func(bool, bool) error
		enabledInMaintenance bool
		skipOnlyEssential    bool // should be true for method that ignore onlyEssential
	}{
		{name: "info", method: s.syncInfo, enabledInMaintenance: true, skipOnlyEssential: true},
		{name: "agent", method: s.syncAgent, skipOnlyEssential: true},
		{name: "facts", method: s.syncFacts},
		{name: "containers", method: s.syncContainers},
		{name: "services", method: s.syncServices},
		{name: "monitors", method: s.syncMonitors, skipOnlyEssential: true},
		{name: "metrics", method: s.syncMetrics},
	}
	startAt := s.now()

	var firstErr error

	for _, step := range syncStep {
		if s.ctx.Err() != nil {
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
			err := step.method(full, onlyEssential && !step.skipOnlyEssential)
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

	logger.V(2).Printf("Synchronization took %v for %v", s.now().Sub(startAt), syncMethods)

	if len(syncMethods) == len(syncStep) && firstErr == nil {
		s.option.Cache.Save()
		s.nextFullSync = s.now().Add(common.JitterDelay(3600, 0.1, 3600))
		logger.V(1).Printf("New full synchronization scheduled for %s", s.nextFullSync.Format(time.RFC3339))
	}

	if firstErr == nil {
		s.lastSync = startAt
	}

	return firstErr
}

//nolint: gocyclo
func (s *Synchronizer) syncToPerform() map[string]bool {
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

	localFacts, _ := s.option.Facts.Facts(s.ctx, 24*time.Hour)

	if fullSync {
		syncMethods["info"] = fullSync
		syncMethods["agent"] = fullSync
		syncMethods["monitors"] = fullSync
	}

	if fullSync || s.lastFactUpdatedAt != localFacts["fact_updated_at"] {
		syncMethods["facts"] = fullSync
	}

	minDelayed := time.Time{}

	for _, delay := range s.delayedContainer {
		if minDelayed.IsZero() || delay.Before(minDelayed) {
			minDelayed = delay
		}
	}

	if fullSync || s.lastSync.Before(s.option.Discovery.LastUpdate()) || (!minDelayed.IsZero() && s.now().After(minDelayed)) {
		syncMethods["services"] = fullSync
		syncMethods["containers"] = fullSync
	}

	if _, ok := syncMethods["services"]; ok {
		// Metrics registration may need services to be synced, trigger metrics synchronization
		syncMethods["metrics"] = false
	}

	if _, ok := syncMethods["containers"]; ok {
		// Metrics registration may need containers to be synced, trigger metrics synchronization
		syncMethods["metrics"] = false
	}

	if _, ok := syncMethods["monitors"]; ok {
		// Metrics registration may need monitors to be synced, trigger metrics synchronization
		syncMethods["metrics"] = false
	}

	if fullSync || s.now().After(s.metricRetryAt) || s.lastSync.Before(s.option.Discovery.LastUpdate()) || s.lastMetricCount != s.option.Store.MetricsCount() {
		syncMethods["metrics"] = fullSync
	}

	// when the mqtt connector is not connected, we cannot receive notifications to get out of maintenance
	// mode, so we poll more often.
	if s.maintenanceMode && !s.option.IsMqttConnected() && s.now().After(s.lastMaintenanceSync.Add(15*time.Minute)) {
		s.forceSync["info"] = false

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
		old, ok := oldFacts[name]
		if !ok {
			continue
		}

		new, ok := newFacts[name]
		if !ok {
			continue
		}

		if old.Value == new.Value {
			continue
		}

		until := s.now().Add(common.JitterDelay(900, 0.05, 900))
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

func (s *Synchronizer) register() error {
	facts, err := s.option.Facts.Facts(s.ctx, 15*time.Minute)
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

	password := generatePassword(20)

	var objectID struct {
		ID string
	}
	// We save an empty agent_uuid before doing the API POST to validate that
	// State can save value.
	if err := s.option.State.Set("agent_uuid", ""); err != nil {
		return err
	}

	statusCode, err := s.client.PostAuth(
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

func generatePassword(length int) string {
	letters := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789#-_@%*:;$")
	b := make([]rune, length)

	for i := range b {
		bigN, err := cryptoRand.Int(cryptoRand.Reader, big.NewInt(int64(len(letters))))
		n := int(bigN.Int64())

		if err != nil {
			n = rand.Intn(len(letters))
		}

		b[i] = letters[n]
	}

	return string(b)
}
