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
	AccountID     string
	Facts         []types.AgentFact
	Containers    []types.Container
	Agent         types.Agent
	AccountConfig types.AccountConfig
	Services      []types.Service
}

// SetAccountID update the AccountID
func (c *Cache) SetAccountID(accountID string) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.AccountID = accountID
	c.dirty = true
}

// AccountID returns the AccountID
func (c *Cache) AccountID() string {
	c.l.Lock()
	defer c.l.Unlock()

	return c.data.AccountID
}

// SetFacts update the AgentFact list
func (c *Cache) SetFacts(facts []types.AgentFact) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Facts = facts
	c.dirty = true
}

// SetServices update the Services list
func (c *Cache) SetServices(services []types.Service) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Services = services
	c.dirty = true
}

// SetContainers update the Container list
func (c *Cache) SetContainers(containers []types.Container) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Containers = containers
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

// Services returns a (copy) of the Services
func (c *Cache) Services() []types.Service {
	c.l.Lock()
	defer c.l.Unlock()
	result := make([]types.Service, len(c.data.Services))
	copy(result, c.data.Services)
	return result
}

// ServicesByUUID returns a map service.id => service
func (c *Cache) ServicesByUUID() map[string]types.Service {
	c.l.Lock()
	defer c.l.Unlock()
	result := make(map[string]types.Service)
	for _, v := range c.data.Services {
		result[v.ID] = v
	}
	return result
}

// Containers returns a (copy) of the Containers
func (c *Cache) Containers() (containers []types.Container) {
	c.l.Lock()
	defer c.l.Unlock()

	result := make([]types.Container, len(c.data.Containers))
	copy(result, c.data.Containers)
	return result
}

// ContainersByContainerID returns a map container.ContainerId => container
func (c *Cache) ContainersByContainerID() map[string]types.Container {
	c.l.Lock()
	defer c.l.Unlock()
	result := make(map[string]types.Container)
	for _, v := range c.data.Containers {
		result[v.DockerID] = v
	}
	return result
}

// ContainersByUUID returns a map container.id => container
func (c *Cache) ContainersByUUID() map[string]types.Container {
	c.l.Lock()
	defer c.l.Unlock()
	result := make(map[string]types.Container)
	for _, v := range c.data.Containers {
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
