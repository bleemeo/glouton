package synchronizer

import (
	"agentgo/bleemeo/client"
	"agentgo/bleemeo/internal/cache"
	"agentgo/bleemeo/types"
	"agentgo/logger"
	"context"
	cryptoRand "crypto/rand"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"time"
)

// Synchronizer synchronize object with Bleemeo
type Synchronizer struct {
	ctx    context.Context
	option Option

	client        *client.HTTPClient
	nextFullSync  time.Time
	disabledUntil time.Time

	startedAt               time.Time
	lastFactUpdatedAt       string
	successiveErrors        int
	warnAccountMismatchDone bool
	apiSupportLabels        bool
}

// Option are parameter for the syncrhonizer
type Option struct {
	types.GlobalOption
	Cache *cache.Cache

	// DisableCallback is a function called when Synchronizer request Bleemeo connector to be disabled
	// reason state why it's disabled and until set for how long it should be disabled.
	DisableCallback func(reason types.DisableReason, until time.Time)

	// UpdateConfigCallback is a function called when Synchronizer detected a AccountConfiguration change
	UpdateConfigCallback func()
}

// New return a new Synchronizer
func New(ctx context.Context, option Option) *Synchronizer {
	return &Synchronizer{
		ctx:    ctx,
		option: option,

		nextFullSync: time.Now(),
	}
}

// Run run the Connector
func (s Synchronizer) Run() error {
	s.startedAt = time.Now()
	accountID := s.option.Config.String("bleemeo.account_id")
	registrationKey := s.option.Config.String("bleemeo.registration_key")
	if accountID == "" || registrationKey == "" {
		return errors.New("bleemeo.account_id and/or bleemeo.registration_key is undefined. Please see https://docs.bleemeo.com/how-to-configure-agent)")
	}

	if err := s.setClient(); err != nil {
		return fmt.Errorf("unable to create Bleemeo HTTP client. Is the API base URL correct ? (error is %v)", err)
	}

	agentID := s.option.State.AgentID()
	if agentID != "" {
		logger.V(1).Printf("This agent is registered on Bleemeo Cloud platform with UUID %v", agentID)
	}

	s.successiveErrors = 0
	delay := time.Duration(0)
	deadline := time.Now()

	if len(s.option.Cache.FactsByKey()) != 0 {
		logger.V(2).Printf("Waiting few second before first synchroization as this agent has a valid cache")
		deadline = time.Now().Add(JitterDelay(20, 0.5, 20))
	}
	for s.ctx.Err() == nil {
		s.waitDeadline(deadline)
		err := s.runOnce()
		if err != nil {
			s.successiveErrors++
			delay = JitterDelay(15+math.Pow(1.55, float64(s.successiveErrors)), 0.1, 900)
			if client.IsAuthError(err) {
				agentID := s.option.State.AgentID()
				fqdn := " with FQDN TODO from agent fact cache"
				logger.Printf(
					"Unable to synchronize with Bleemeo: Unable to login with credentials from state.json. Using agent ID %s%s. Was this server deleted on Bleemeo Cloud platform ?",
					agentID,
					fqdn,
				)
			} else {
				if s.successiveErrors%5 == 0 {
					logger.Printf("Unable to synchronize with Bleemeo: %v", err)
				} else {
					logger.V(1).Printf("Unable to synchronize with Bleemeo: %v", err)
				}
			}
		} else {
			s.successiveErrors = 0
			delay = JitterDelay(15, 0.05, 15)
		}
		deadline = time.Now().Add(delay)
		if deadline.Before(s.disabledUntil) {
			deadline = s.disabledUntil
		}
	}

	return nil
}

func (s Synchronizer) waitDeadline(deadline time.Time) {
	for time.Now().Before(deadline) && s.ctx.Err() == nil {
		delay := time.Until(deadline)
		if delay < 0 {
			break
		}
		if delay > 60*time.Second {
			logger.V(1).Printf(
				"Synchronize with Bleemeo Cloud platform still have to wait %v due to last error",
				delay.Truncate(time.Second),
			)
			delay = 60 * time.Second
		}
		select {
		// TODO: allow this loop to be interrupted when new metrics arrive
		case <-time.After(delay):
		case <-s.ctx.Done():
		}
	}
}

// JitterDelay return a number between value * [1-factor; 1+factor[
// If the valueSecond exceed max, max is used instead of valueSecond.
// factor should be less than 1
func JitterDelay(valueSecond float64, factor float64, maxSecond float64) time.Duration {
	scale := rand.Float64() * 2 * factor
	scale += 1 - factor
	if valueSecond > maxSecond {
		valueSecond = maxSecond
	}
	result := int(valueSecond * scale)
	return time.Duration(result) * time.Second
}

func (s *Synchronizer) setClient() error {
	username := fmt.Sprintf("%s@bleemeo.com", s.option.State.AgentID())
	password := s.option.State.AgentPassword()
	client, err := client.NewClient(s.ctx, s.option.Config.String("bleemeo.api_base"), username, password, s.option.Config.Bool("bleemeo.api_ssl_insecure"))
	if err != nil {
		return err
	}
	s.client = client
	return nil
}

func (s *Synchronizer) runOnce() error {
	agentID := s.option.State.AgentID()
	if agentID == "" {
		if err := s.register(); err != nil {
			return err
		}
	}

	syncMethods := make(map[string]bool)
	now := time.Now()
	localFacts, _ := s.option.Facts.Facts(s.ctx, 24*time.Hour)

	fullSync := false
	if s.nextFullSync.Before(now) {
		fullSync = true
	}
	// TODO: add other condition to trigger update
	if fullSync {
		syncMethods["agent"] = true
	}
	if fullSync || s.lastFactUpdatedAt != localFacts["fact_updated_at"] {
		syncMethods["facts"] = true
	}
	if fullSync {
		syncMethods["services"] = true

		// Metrics registration may need services to be synced, trigger metrics synchronization
		syncMethods["metrics"] = true
	}
	if fullSync {
		syncMethods["containers"] = true

		// Metrics registration may need containers to be synced, trigger metrics synchronization
		syncMethods["metrics"] = true
	}
	if fullSync {
		syncMethods["metrics"] = true
	}

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
		{name: "metrics", method: s.syncMetrics},
	}
	startAt := time.Now()
	var lastErr error
	for _, step := range syncStep {
		if _, ok := syncMethods[step.name]; ok {
			err := step.method(fullSync)
			if err != nil {
				logger.V(1).Printf("Synchronization for object %s failed: %v", step.name, err)
				lastErr = err
			}
		}
	}
	logger.V(2).Printf("Synchronization took %v for %v", time.Since(startAt), syncMethods)
	if fullSync && lastErr == nil {
		s.option.Cache.Save()
		s.nextFullSync = time.Now().Add(JitterDelay(3600, 0.1, 3600))
		logger.V(1).Printf("New full synchronization scheduled for %s", s.nextFullSync.Format(time.RFC3339))
	}
	return lastErr
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
		until := time.Now().Add(JitterDelay(900, 0.05, 900))
		if s.option.DisableCallback != nil {
			s.option.DisableCallback(types.DisableDuplicatedAgent, until)
		}
		logger.Printf(
			"Detected duplicated state.json. Another agent changed %#v from %#v to %#v",
			name,
			old,
			new,
		)
		logger.Printf(
			"The following links may be relevant to solve the issue: https://docs.bleemeo.com/agent/migrate-agent-new-server/ " +
				"and https://docs.bleemeo.com/agent/install-cloudimage-creation/",
		)
	}
	return nil
}

func (s *Synchronizer) register() error {
	facts, err := s.option.Facts.Facts(s.ctx, 15*time.Minute)
	if err != nil {
		return err
	}
	fqdn := facts["fqdn"]
	name := s.option.Config.String("bleemeo.initial_agent_name")
	if fqdn == "" {
		return errors.New("unable to register, fqdn is not set")
	}
	if name == "" {
		name = fqdn
	}

	accountID := s.option.Config.String("bleemeo.account_id")

	password := generatePassword(10)
	var objectID struct {
		ID string
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
		s.option.Config.String("bleemeo.registration_key"),
		&objectID,
	)
	if err != nil {
		return err
	}
	if statusCode != 201 {
		return fmt.Errorf("registration status code is %v, want 201", statusCode)
	}
	s.option.State.SetAgentIDPassword(objectID.ID, password)
	logger.V(1).Printf("regisration successful with UUID %v", objectID.ID)
	_ = s.setClient()
	return nil
}

func generatePassword(length int) string {
	letters := []rune("abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789")
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
