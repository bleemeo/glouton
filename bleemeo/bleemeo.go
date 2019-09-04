package bleemeo

import (
	"context"
	"sync"
	"time"

	"agentgo/bleemeo/internal/cache"
	"agentgo/bleemeo/internal/mqtt"
	"agentgo/bleemeo/internal/synchronizer"
	"agentgo/bleemeo/types"
	"agentgo/logger"
)

// Connector manager the connection between the Agent and Bleemeo
type Connector struct {
	option types.GlobalOption

	cache *cache.Cache
	sync  *synchronizer.Synchronizer
	mqtt  *mqtt.Client

	l sync.Mutex
	// initDone      bool
	disabledUntil time.Time
	disableReason types.DisableReason
}

// New create a new Connector
func New(option types.GlobalOption) *Connector {
	c := &Connector{
		option: option,
		cache:  cache.Load(option.State),
	}
	c.sync = synchronizer.New(synchronizer.Option{
		GlobalOption:         c.option,
		Cache:                c.cache,
		UpdateConfigCallback: c.uppdateConfig,
		DisableCallback:      c.disableCallback,
	})
	return c
}

func (c *Connector) initMQTT() error {
	c.l.Lock()
	defer c.l.Unlock()
	var password string
	err := c.option.State.Get("password", &password)
	if err != nil {
		return err
	}
	c.mqtt = mqtt.New(mqtt.Option{
		GlobalOption:         c.option,
		Cache:                c.cache,
		DisableCallback:      c.disableCallback,
		AgentID:              c.AgentID(),
		AgentPassword:        password,
		UpdateConfigCallback: c.sync.NotifyConfigUpdate,
		UpdateMetrics:        c.sync.UpdateMetrics,
	})
	return nil
}

// Run run the Connector
func (c *Connector) Run(ctx context.Context) error {
	defer c.cache.Save()
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var syncErr, mqttErr error
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
		logger.V(2).Printf("Bleemeo connector stopping")
	}()

	for {
		if c.AgentID() != "" {
			if err := c.initMQTT(); err != nil {
				cancel()
				mqttErr = err
				break
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer cancel()
				mqttErr = c.mqtt.Run(subCtx)
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

// Tags returns the Tags set on Bleemeo Cloud platform
func (c *Connector) Tags() []string {
	agent := c.cache.Agent()
	tags := make([]string, len(agent.Tags))
	for i, t := range agent.Tags {
		tags[i] = t.Name
	}
	return tags
}

// AccountID returns the Account UUID of Bleemeo
// It return the empty string if the Account UUID is not available
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
// It return the empty string if the Account UUID is not available
func (c *Connector) AgentID() string {
	var agentID string
	err := c.option.State.Get("agent_uuid", &agentID)
	if err != nil {
		return ""
	}
	return agentID
}

// RegistrationAt returns the date of registration with Bleemeo API
func (c *Connector) RegistrationAt() time.Time {
	c.l.Lock()
	defer c.l.Unlock()
	agent := c.cache.Agent()
	return agent.CreatedAt
}

// Connected returns the date of registration with Bleemeo API
func (c *Connector) Connected() bool {
	c.l.Lock()
	defer c.l.Unlock()
	if c.mqtt == nil {
		return false
	}
	return c.mqtt.Connected()
}

// LastReport returns the date of last report with Bleemeo API over MQTT
func (c *Connector) LastReport() time.Time {
	c.l.Lock()
	defer c.l.Unlock()
	if c.mqtt == nil {
		return time.Time{}
	}
	return c.mqtt.LastReport()
}

// HealthCheck perform some health check and logger any issue found
func (c *Connector) HealthCheck() bool {
	ok := true
	if c.AgentID() == "" {
		logger.Printf("Agent not yet registered")
		ok = false
	}
	c.l.Lock()
	defer c.l.Unlock()
	if time.Now().Before(c.disabledUntil) {
		delay := time.Until(c.disabledUntil)
		logger.Printf("Bleemeo connector is still disabled for %v due to %v", delay.Truncate(time.Second), c.disableReason)
		return false
	}

	if c.mqtt != nil {
		ok = c.mqtt.HealthCheck() && ok
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
	delay := time.Until(until)
	logger.Printf("Disabling Bleemeo connector for %v due to %v", delay.Truncate(time.Second), reason)
	c.sync.Disable(until, reason)
	c.mqtt.Disable(until, reason)

	c.l.Lock()
	defer c.l.Unlock()
	c.disabledUntil = until
	c.disableReason = reason
}
