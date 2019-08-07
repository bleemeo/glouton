package cache

import (
	"agentgo/bleemeo/types"
	"sync"
)

// Cache store information about object registered in Bleemeo API
type Cache struct {
	facts         []types.AgentFact
	agent         types.Agent
	accountConfig types.AccountConfig

	l sync.Mutex
}

// SetFacts update the AgentFact list
func (c *Cache) SetFacts(facts []types.AgentFact) {
	c.l.Lock()
	defer c.l.Unlock()

	c.facts = facts
}

// SetAgent update the Agent object
func (c *Cache) SetAgent(agent types.Agent) {
	c.l.Lock()
	defer c.l.Unlock()

	c.agent = agent
}

// SetAccountConfig update the AccountConfig object
func (c *Cache) SetAccountConfig(accountConfig types.AccountConfig) {
	c.l.Lock()
	defer c.l.Unlock()

	c.accountConfig = accountConfig
}

// AccountConfig returns AccountConfig
func (c *Cache) AccountConfig() types.AccountConfig {
	c.l.Lock()
	defer c.l.Unlock()

	return c.accountConfig
}

// FactsByKey returns a map fact.key => facts
func (c *Cache) FactsByKey() map[string]types.AgentFact {
	c.l.Lock()
	defer c.l.Unlock()

	result := make(map[string]types.AgentFact)
	for _, v := range c.facts {
		result[v.Key] = v
	}
	return result
}

// FactsByUUID returns a map fact.id => facts
func (c *Cache) FactsByUUID() map[string]types.AgentFact {
	c.l.Lock()
	defer c.l.Unlock()
	result := make(map[string]types.AgentFact)
	for _, v := range c.facts {
		result[v.ID] = v
	}
	return result
}
