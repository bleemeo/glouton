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

// Synchronizer synchronize object with Bleemeo.
type Synchronizer struct {
	ctx    context.Context
	option Option

	client       *client.HTTPClient
	nextFullSync time.Time

	startedAt               time.Time
	lastSync                time.Time
	lastFactUpdatedAt       string
	successiveErrors        int
	warnAccountMismatchDone bool
	lastMetricCount         int
	agentID                 string

	l                     sync.Mutex
	disabledUntil         time.Time
	disableReason         bleemeoTypes.DisableReason
	forceSync             map[string]bool
	pendingMetricsUpdate  []string
	pendingMonitorsUpdate []MonitorUpdate
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
}

// New return a new Synchronizer.
func New(option Option) *Synchronizer {
	return &Synchronizer{
		option: option,

		forceSync:    make(map[string]bool),
		nextFullSync: time.Now(),
	}
}

// Run run the Connector.
func (s *Synchronizer) Run(ctx context.Context) error {
	s.ctx = ctx
	s.startedAt = time.Now()

	if err := s.option.State.Get("agent_uuid", &s.agentID); err != nil {
		return err
	}

	firstSync := true

	if s.agentID != "" {
		logger.V(1).Printf("This agent is registered on Bleemeo Cloud platform with UUID %v", s.agentID)

		firstSync = false
	}

	if err := s.setClient(); err != nil {
		return fmt.Errorf("unable to create Bleemeo HTTP client. Is the API base URL correct ? (error is %v)", err)
	}

	s.successiveErrors = 0
	successiveAuthErrors := 0

	var minimalDelay time.Duration

	if len(s.option.Cache.FactsByKey()) != 0 {
		logger.V(2).Printf("Waiting a few seconds before the first synchronization as this agent has a valid cache")

		minimalDelay = common.JitterDelay(20, 0.5, 20)
	}

	for s.ctx.Err() == nil {
		// TODO: allow WaitDeadline to be interrupted when new metrics arrive
		common.WaitDeadline(s.ctx, minimalDelay, s.getDisabledUntil, "Synchronize with Bleemeo Cloud platform")

		if s.ctx.Err() != nil {
			break
		}

		err := s.runOnce()
		if err != nil {
			s.successiveErrors++

			if client.IsAuthError(err) {
				successiveAuthErrors++
			}

			switch {
			case client.IsAuthError(err) && successiveAuthErrors >= 3:
				delay := common.JitterDelay(60*math.Pow(1.55, float64(successiveAuthErrors)), 0.1, 21600)
				s.option.DisableCallback(bleemeoTypes.DisableAuthenticationError, time.Now().Add(delay))
			default:
				delay := common.JitterDelay(15*math.Pow(1.55, float64(s.successiveErrors)), 0.1, 900)
				s.disable(time.Now().Add(delay), bleemeoTypes.DisableTooManyErrors)

				if client.IsAuthError(err) && successiveAuthErrors == 1 {
					// we disable only to trigger a reconnection on MQTT
					s.option.DisableCallback(bleemeoTypes.DisableAuthenticationError, time.Now().Add(10*time.Second))
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

			if firstSync {
				minimalDelay = common.JitterDelay(1, 0.05, 1)

				s.waitCPUMetric()

				firstSync = false
			}
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

	return builder.String()
}

// NotifyConfigUpdate notify that an Agent configuration change occurred.
// If immediate is true, the configuration change already happened, else it will happen.
func (s *Synchronizer) NotifyConfigUpdate(immediate bool) {
	s.l.Lock()
	defer s.l.Unlock()

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
// To re-enable, use the (not yet implmented) Enable().
func (s *Synchronizer) Disable(until time.Time, reason bleemeoTypes.DisableReason) {
	s.disable(until, reason)
}

func (s *Synchronizer) disable(until time.Time, reason bleemeoTypes.DisableReason) {
	s.l.Lock()
	defer s.l.Unlock()

	if s.disabledUntil.Before(until) {
		s.disabledUntil = until
		s.disableReason = reason
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

func (s *Synchronizer) runOnce() error {
	if s.agentID == "" {
		if err := s.register(); err != nil {
			return err
		}

		s.option.NotifyFirstRegistration(s.ctx)
	}

	syncMethods := s.syncToPerform()

	if len(syncMethods) == 0 {
		return nil
	}

	if err := s.checkDuplicated(); err != nil {
		return err
	}

	syncStep := []struct {
		name   string
		method func(bool) error
	}{
		{name: "agent", method: s.syncAgent},
		{name: "facts", method: s.syncFacts},
		{name: "services", method: s.syncServices},
		{name: "containers", method: s.syncContainers},
		{name: "monitors", method: s.syncMonitors},
		{name: "metrics", method: s.syncMetrics},
	}
	startAt := time.Now()

	var lastErr error

	for _, step := range syncStep {
		if s.ctx.Err() != nil {
			break
		}

		until, _ := s.getDisabledUntil()
		if time.Now().Before(until) {
			if lastErr == nil {
				lastErr = errors.New("bleemeo connector is temporary disabled")
			}

			break
		}

		if full, ok := syncMethods[step.name]; ok {
			err := step.method(full)
			if err != nil {
				logger.V(1).Printf("Synchronization for object %s failed: %v", step.name, err)
				lastErr = err
			}
		}
	}

	logger.V(2).Printf("Synchronization took %v for %v", time.Since(startAt), syncMethods)

	if len(syncMethods) == len(syncStep) && lastErr == nil {
		s.option.Cache.Save()
		s.nextFullSync = time.Now().Add(common.JitterDelay(3600, 0.1, 3600))
		logger.V(1).Printf("New full synchronization scheduled for %s", s.nextFullSync.Format(time.RFC3339))
	}

	if lastErr == nil {
		s.lastSync = startAt
	}

	return lastErr
}

func (s *Synchronizer) syncToPerform() map[string]bool {
	s.l.Lock()
	defer s.l.Unlock()

	syncMethods := make(map[string]bool)

	fullSync := false
	if s.nextFullSync.Before(time.Now()) {
		fullSync = true
	}

	nextConfigAt := s.option.Cache.Agent().NextConfigAt
	if !nextConfigAt.IsZero() && nextConfigAt.Before(time.Now()) {
		fullSync = true
	}

	localFacts, _ := s.option.Facts.Facts(s.ctx, 24*time.Hour)

	if fullSync {
		syncMethods["agent"] = fullSync
		syncMethods["monitors"] = fullSync
	}

	if fullSync || s.lastFactUpdatedAt != localFacts["fact_updated_at"] {
		syncMethods["facts"] = fullSync
	}

	if fullSync || s.lastSync.Before(s.option.Discovery.LastUpdate()) {
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

	if fullSync || s.lastSync.Before(s.option.Discovery.LastUpdate()) || s.lastMetricCount != s.option.Store.MetricsCount() {
		syncMethods["metrics"] = fullSync
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

		if old == new {
			continue
		}

		until := time.Now().Add(common.JitterDelay(900, 0.05, 900))
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
			"The following links may be relevant to solve the issue: https://docs.bleemeo.com/agent/migrate-agent-new-server/ " +
				"and https://docs.bleemeo.com/agent/install-cloudimage-creation/",
		)

		return errors.New("bleemeo connector temporary disabled")
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
		return errors.New("unable to register, fqdn is not set")
	}

	name := s.option.Config.String("bleemeo.initial_agent_name")
	if name == "" {
		name = fqdn
	}

	accountID := s.option.Config.String("bleemeo.account_id")
	registrationKey := s.option.Config.String("bleemeo.registration_key")

	for accountID == "" || registrationKey == "" {
		return errors.New("bleemeo.account_id and/or bleemeo.registration_key is undefined. Please see https://docs.bleemeo.com/how-to-configure-agent")
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
		return fmt.Errorf("registration status code is %v, want 201", statusCode)
	}

	s.agentID = objectID.ID

	if err := s.option.State.Set("agent_uuid", objectID.ID); err != nil {
		return err
	}

	if err := s.option.State.Set("password", password); err != nil {
		return err
	}

	logger.V(1).Printf("regisration successful with UUID %v", objectID.ID)

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
