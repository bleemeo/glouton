package bleemeo

import (
	"context"
	"time"

	"agentgo/bleemeo/internal/cache"
	"agentgo/bleemeo/internal/synchronizer"
	"agentgo/bleemeo/types"
	"agentgo/logger"
)

// Connector manager the connection between the Agent and Bleemeo
type Connector struct {
	option types.GlobalOption

	cache *cache.Cache
	sync  *synchronizer.Synchronizer
}

// New create a new Connector
func New(option types.GlobalOption) *Connector {
	return &Connector{
		option: option,
		cache:  cache.Load(option.State),
	}
}

// Run run the Connector
func (c Connector) Run(ctx context.Context) error {
	c.sync = synchronizer.New(ctx, synchronizer.Option{
		GlobalOption:         c.option,
		Cache:                c.cache,
		UpdateConfigCallback: c.uppdateConfig,
		DisableCallback:      c.disableCallback,
	})
	defer c.cache.Save()
	return c.sync.Run()
}

func (c Connector) uppdateConfig() {
	currentConfig := c.cache.AccountConfig()
	logger.Printf("Changed to configuration %s", currentConfig.Name)

	if c.option.UpdateMetricResolution != nil {
		c.option.UpdateMetricResolution(time.Duration(currentConfig.MetricAgentResolution) * time.Second)
	}
}

func (c Connector) disableCallback(reason types.DisableReason, until time.Time) {
	logger.Printf("Disabling Bleemeo connector until %v due to %v", until.Truncate(time.Minute), reason)
	c.sync.Disable(until)
}
