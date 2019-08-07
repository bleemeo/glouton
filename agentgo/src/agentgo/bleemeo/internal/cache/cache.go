package cache

import (
	"agentgo/bleemeo/types"
	"agentgo/logger"
	"sync"
)

const cacheVersion = 1

// Cache store information about object registered in Bleemeo API
type Cache struct {
	data  data
	l     sync.Mutex
	dirty bool
	state types.State
}

type data struct {
	Version       int
	Facts         []types.AgentFact
	Agent         types.Agent
	AccountConfig types.AccountConfig
}

// SetFacts update the AgentFact list
func (c *Cache) SetFacts(facts []types.AgentFact) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Facts = facts
	c.dirty = true
}

// SetAgent update the Agent object
func (c *Cache) SetAgent(agent types.Agent) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Agent = agent
	c.dirty = true
}

// SetAccountConfig update the AccountConfig object
func (c *Cache) SetAccountConfig(accountConfig types.AccountConfig) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.AccountConfig = accountConfig
	c.dirty = true
}

// AccountConfig returns AccountConfig
func (c *Cache) AccountConfig() types.AccountConfig {
	c.l.Lock()
	defer c.l.Unlock()

	return c.data.AccountConfig
}

// FactsByKey returns a map fact.key => facts
func (c *Cache) FactsByKey() map[string]types.AgentFact {
	c.l.Lock()
	defer c.l.Unlock()

	result := make(map[string]types.AgentFact)
	for _, v := range c.data.Facts {
		result[v.Key] = v
	}
	return result
}

// FactsByUUID returns a map fact.id => facts
func (c *Cache) FactsByUUID() map[string]types.AgentFact {
	c.l.Lock()
	defer c.l.Unlock()
	result := make(map[string]types.AgentFact)
	for _, v := range c.data.Facts {
		result[v.ID] = v
	}
	return result
}

// Save saves the cache into State
func (c *Cache) Save() {
	c.l.Lock()
	defer c.l.Unlock()

	if c.state == nil {
		return
	}

	if !c.dirty {
		return
	}

	if err := c.state.SetCache("bleemeoConnector", c.data); err != nil {
		logger.V(1).Printf("Unable to save Bleemeo connector cache: %v", err)
		return
	}
	c.dirty = false
}

// Load loads the cache from State
func Load(state types.State) *Cache {
	cache := &Cache{
		state: state,
	}
	var newData data
	if err := state.Cache("bleemeoConnector", &newData); err != nil {
		logger.V(1).Printf("Unable to load Bleemeo connector cache: %v", err)
	}
	switch newData.Version {
	case 0:
		logger.V(2).Printf("Bleemeo connector cache is too absent, starting with new empty cache")
		cache.data.Version = cacheVersion
	case cacheVersion:
		cache.data = newData
	default:
		logger.V(2).Printf("Bleemeo connector cache is too recent. Discarding content")
		cache.data.Version = cacheVersion
	}
	return cache
}
