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

	l        sync.Mutex
	initDone bool
}

// New create a new Connector
func New(option types.GlobalOption) *Connector {
	return &Connector{
		option: option,
		cache:  cache.Load(option.State),
	}
}

func (c *Connector) init(ctx context.Context) {
	c.l.Lock()
	defer c.l.Unlock()
	c.sync = synchronizer.New(synchronizer.Option{
		GlobalOption:         c.option,
		Cache:                c.cache,
		UpdateConfigCallback: c.uppdateConfig,
		DisableCallback:      c.disableCallback,
	})
	c.mqtt = mqtt.New(mqtt.Option{
		GlobalOption:    c.option,
		Cache:           c.cache,
		DisableCallback: c.disableCallback,
	})
	c.initDone = true
}

// Run run the Connector
func (c *Connector) Run(ctx context.Context) error {
	c.init(ctx)
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
		mqttErr = c.mqtt.Run(subCtx)
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

	wg.Wait()
	logger.V(2).Printf("Bleemeo connector stopped")
	if syncErr != nil {
		return syncErr
	}
	return mqttErr
}

// AccountID returns the Account UUID of Bleemeo
// It return the empty string if the Account UUID is not available
func (c *Connector) AccountID() string {
	c.l.Lock()
	defer c.l.Unlock()
	if !c.initDone {
		return ""
	}
	accountID := c.cache.AccountID()
	if accountID != "" {
		return accountID
	}
	return c.option.Config.String("bleemeo.account_id")
}

// AgentID returns the Agent UUID of Bleemeo
// It return the empty string if the Account UUID is not available
func (c *Connector) AgentID() string {
	return c.option.State.AgentID()
}

// RegistrationAt returns the date of registration with Bleemeo API
func (c *Connector) RegistrationAt() time.Time {
	c.l.Lock()
	defer c.l.Unlock()
	if !c.initDone {
		return time.Time{}
	}
	agent := c.cache.Agent()
	return agent.CreatedAt
}

// Connected returns the date of registration with Bleemeo API
func (c *Connector) Connected() bool {
	c.l.Lock()
	defer c.l.Unlock()
	if !c.initDone {
		return false
	}
	return c.mqtt.Connected()
}

// LastReport returns the date of last report with Bleemeo API
func (c *Connector) LastReport() time.Time {
	return time.Time{} // TODO
}

func (c *Connector) emitInternalMetric() {
	if c.mqtt.Connected() {
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
	logger.Printf("Disabling Bleemeo connector until %v due to %v", until.Truncate(time.Minute), reason)
	c.sync.Disable(until)
}
