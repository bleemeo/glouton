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
	option Option

	cache *cache.Cache
}

// Option for bleemeo.Connector
type Option struct {
	Config types.Config
	State  types.State
	Facts  types.FactProvider

	UpdateMetricResolution func(resolution time.Duration)
}

// New create a new Connector
func New(option Option) *Connector {
	return &Connector{
		option: option,
		cache:  cache.Load(option.State),
	}
}

// Run run the Connector
func (c Connector) Run(ctx context.Context) error {
	sync := synchronizer.New(ctx, synchronizer.Option{
		Config: c.option.Config,
		State:  c.option.State,
		Facts:  c.option.Facts,
		Cache:  c.cache,

		UpdateConfigCallback: c.uppdateConfig,
	})
	defer c.cache.Save()
	return sync.Run()
}

func (c Connector) uppdateConfig() {
	currentConfig := c.cache.AccountConfig()
	logger.Printf("Changed to configuration %s", currentConfig.Name)

	if c.option.UpdateMetricResolution != nil {
		c.option.UpdateMetricResolution(time.Duration(currentConfig.MetricAgentResolution) * time.Second)
	}
}
