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

package bleemeo

import (
	"context"
	"runtime"
	"sync"
	"time"

	"glouton/bleemeo/internal/cache"
	"glouton/bleemeo/internal/mqtt"
	"glouton/bleemeo/internal/synchronizer"
	"glouton/bleemeo/types"
	"glouton/logger"
	gloutonTypes "glouton/types"
)

// Connector manager the connection between the Agent and Bleemeo.
type Connector struct {
	option types.GlobalOption

	cache       *cache.Cache
	sync        *synchronizer.Synchronizer
	mqtt        *mqtt.Client
	mqttRestart chan interface{}

	l sync.Mutex
	// initDone      bool
	lastKnownReport time.Time
	lastMQTTRestart time.Time
	disabledUntil   time.Time
	disableReason   types.DisableReason
}

// New create a new Connector.
func New(option types.GlobalOption) *Connector {
	c := &Connector{
		option:      option,
		cache:       cache.Load(option.State),
		mqttRestart: make(chan interface{}, 1),
	}
	c.sync = synchronizer.New(synchronizer.Option{
		GlobalOption:         c.option,
		Cache:                c.cache,
		UpdateConfigCallback: c.uppdateConfig,
		DisableCallback:      c.disableCallback,
	})

	return c
}

// UpdateUnitsAndThresholds update metrics units & threshold (from cache).
func (c *Connector) UpdateUnitsAndThresholds(firstUpdate bool) {
	c.sync.UpdateUnitsAndThresholds(firstUpdate)
}

func (c *Connector) initMQTT(previousPoint map[gloutonTypes.AgentID][]gloutonTypes.MetricPoint, first bool) error {
	c.l.Lock()
	defer c.l.Unlock()

	var password string

	err := c.option.State.Get("password", &password)
	if err != nil {
		return err
	}

	c.mqtt = mqtt.New(
		mqtt.Option{
			GlobalOption:         c.option,
			Cache:                c.cache,
			DisableCallback:      c.disableCallback,
			AgentID:              gloutonTypes.AgentID(c.AgentID()),
			AgentPassword:        password,
			UpdateConfigCallback: c.sync.NotifyConfigUpdate,
			UpdateMetrics:        c.sync.UpdateMetrics,
			InitialPoints:        previousPoint,
		},
		first,
	)

	return nil
}
func (c *Connector) mqttRestarter(ctx context.Context) error {
	var (
		wg             sync.WaitGroup
		mqttErr        error
		l              sync.Mutex
		previousPoints map[gloutonTypes.AgentID][]gloutonTypes.MetricPoint
		alreadyInit    bool
	)

	subCtx, cancel := context.WithCancel(ctx)

	c.l.Lock()
	mqttRestart := c.mqttRestart
	c.l.Unlock()

	if mqttRestart == nil {
		return nil
	}

	select {
	case mqttRestart <- nil:
	default:
	}

	for range mqttRestart {
		cancel()

		subCtx, cancel = context.WithCancel(ctx)

		c.l.Lock()

		if c.mqtt != nil {
			// Try to retrieve pending points
			resultChan := make(chan map[gloutonTypes.AgentID][]gloutonTypes.MetricPoint, 1)

			go func() {
				resultChan <- c.mqtt.PopPendingPoints()
			}()

			select {
			case previousPoints = <-resultChan:
			case <-time.After(10 * time.Second):
			}
		}

		c.mqtt = nil

		c.l.Unlock()

		err := c.initMQTT(previousPoints, !alreadyInit)
		previousPoints = nil
		alreadyInit = true

		if err != nil {
			l.Lock()

			if mqttErr == nil {
				mqttErr = err
			}

			l.Unlock()

			break
		}

		wg.Add(1)

		go func() {
			defer wg.Done()

			err := c.mqtt.Run(subCtx)

			l.Lock()

			if mqttErr == nil {
				mqttErr = err
			}

			l.Unlock()
		}()
	}

	cancel()
	wg.Wait()

	return mqttErr
}

// Run run the Connector.
func (c *Connector) Run(ctx context.Context) error {
	defer c.cache.Save()

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg               sync.WaitGroup
		syncErr, mqttErr error
	)

	wg.Add(1)

	go func() {
		defer wg.Done()
		defer cancel()

		syncErr = c.sync.Run(subCtx)
	}()

	wg.Add(1)

	go func() {
		defer wg.Done()
		defer cancel()

		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for subCtx.Err() == nil {
			c.emitInternalMetric()

			select {
			case <-ticker.C:
			case <-subCtx.Done():
			}
		}

		c.l.Lock()
		close(c.mqttRestart)
		c.mqttRestart = nil
		c.l.Unlock()

		logger.V(2).Printf("Bleemeo connector stopping")
	}()

	for subCtx.Err() == nil {
		if c.AgentID() != "" {
			wg.Add(1)

			go func() {
				defer wg.Done()
				defer cancel()

				mqttErr = c.mqttRestarter(subCtx)
			}()

			break
		}

		select {
		case <-time.After(5 * time.Second):
		case <-subCtx.Done():
		}
	}

	wg.Wait()
	logger.V(2).Printf("Bleemeo connector stopped")

	if syncErr != nil {
		return syncErr
	}

	return mqttErr
}

// UpdateContainers request to update a containers.
func (c *Connector) UpdateContainers() {
	c.sync.UpdateContainers()
}

// Tags returns the Tags set on Bleemeo Cloud platform.
func (c *Connector) Tags() []string {
	agent := c.cache.Agent()
	tags := make([]string, len(agent.Tags))

	for i, t := range agent.Tags {
		tags[i] = t.Name
	}

	return tags
}

// AccountID returns the Account UUID of Bleemeo
// It return the empty string if the Account UUID is not available.
func (c *Connector) AccountID() string {
	c.l.Lock()
	defer c.l.Unlock()

	accountID := c.cache.AccountID()
	if accountID != "" {
		return accountID
	}

	return c.option.Config.String("bleemeo.account_id")
}

// AgentID returns the Agent UUID of Bleemeo
// It return the empty string if the Account UUID is not available.
func (c *Connector) AgentID() string {
	var agentID string

	err := c.option.State.Get("agent_uuid", &agentID)
	if err != nil {
		return ""
	}

	return agentID
}

// RegistrationAt returns the date of registration with Bleemeo API.
func (c *Connector) RegistrationAt() time.Time {
	c.l.Lock()
	defer c.l.Unlock()

	agent := c.cache.Agent()

	return agent.CreatedAt
}

// Connected returns the date of registration with Bleemeo API.
func (c *Connector) Connected() bool {
	c.l.Lock()
	defer c.l.Unlock()

	if c.mqtt == nil {
		return false
	}

	return c.mqtt.Connected()
}

// LastReport returns the date of last report with Bleemeo API over MQTT.
func (c *Connector) LastReport() time.Time {
	c.l.Lock()
	defer c.l.Unlock()

	if c.mqtt != nil {
		tmp := c.mqtt.LastReport()
		if tmp.After(c.lastKnownReport) {
			c.lastKnownReport = tmp
		}
	}

	return c.lastKnownReport
}

// HealthCheck perform some health check and logger any issue found.
func (c *Connector) HealthCheck() bool {
	ok := true

	if c.AgentID() == "" {
		logger.Printf("Agent not yet registered")

		ok = false
	}

	lastReport := c.LastReport()

	c.l.Lock()
	defer c.l.Unlock()

	if time.Now().Before(c.disabledUntil) {
		delay := time.Until(c.disabledUntil)

		logger.Printf("Bleemeo connector is still disabled for %v due to %v", delay.Truncate(time.Second), c.disableReason)

		return false
	}

	if c.mqtt != nil {
		ok = c.mqtt.HealthCheck() && ok

		if !lastReport.IsZero() && time.Since(lastReport) > time.Hour && (c.lastMQTTRestart.IsZero() || time.Since(c.lastMQTTRestart) > 4*time.Hour) {
			c.lastMQTTRestart = time.Now()

			logger.Printf("MQTT connection fail to re-establish since %s. This may be a long network issue or a Glouton bug", lastReport.Format(time.RFC3339))

			if time.Since(lastReport) > 36*time.Hour {
				logger.Printf("Restarting MQTT is not enough. Glouton seems unhealthy, killing mysel")

				// We don't know how big the buffer needs to be to collect
				// all the goroutines. Use 2MB buffer which hopefully is enough
				buffer := make([]byte, 1<<21)

				runtime.Stack(buffer, true)
				logger.Printf("%s", string(buffer))
				panic("Glouton seems unhealthy, killing myself")
			}

			logger.Printf("Trying to restart the MQTT connection from scratch")

			if c.mqttRestart != nil {
				c.mqttRestart <- nil
			}
		}
	}

	return ok
}

func (c *Connector) emitInternalMetric() {
	c.l.Lock()
	defer c.l.Unlock()

	if c.mqtt != nil && c.mqtt.Connected() {
		c.option.Acc.AddFields("", map[string]interface{}{"agent_status": 1.0}, nil)
	}
}

func (c *Connector) uppdateConfig() {
	currentConfig := c.cache.AccountConfig()

	logger.Printf("Changed to configuration %s", currentConfig.Name)

	if c.option.UpdateMetricResolution != nil {
		c.option.UpdateMetricResolution(time.Duration(currentConfig.MetricAgentResolution) * time.Second)
	}
}

func (c *Connector) disableCallback(reason types.DisableReason, until time.Time) {
	c.l.Lock()

	if c.disabledUntil.After(until) {
		return
	}

	c.disabledUntil = until
	c.disableReason = reason

	c.l.Unlock()

	delay := time.Until(until)

	logger.Printf("Disabling Bleemeo connector for %v due to %v", delay.Truncate(time.Second), reason)
	c.sync.Disable(until, reason)
	c.mqtt.Disable(until, reason)
}
