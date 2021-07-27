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

package cache

import (
	"glouton/bleemeo/internal/common"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/logger"
	"glouton/types"
	"sync"
)

const cacheVersion = 4
const cacheKey = "CacheBleemeoConnector"

// Cache store information about object registered in Bleemeo API.
type Cache struct {
	data  data
	l     sync.Mutex
	dirty bool
	state bleemeoTypes.State

	cachedMetricLookup           map[string]bleemeoTypes.Metric
	cachedFailRegistrationLookup map[string]bleemeoTypes.MetricRegistration
}

type data struct {
	Version                 int
	AccountID               string
	Facts                   []bleemeoTypes.AgentFact
	Containers              []bleemeoTypes.Container
	Agents                  []bleemeoTypes.Agent
	Metrics                 []bleemeoTypes.Metric
	MetricRegistrationsFail []bleemeoTypes.MetricRegistration
	Agent                   bleemeoTypes.Agent
	// AccountConfig groups the configuration of other accounts, something we may need for probes.
	// mapping config UUID -> Config
	AccountConfigs map[string]bleemeoTypes.AccountConfig
	// In contrast, CurrentAccountConfig stores the configuration of the account in which this
	// agent is registered.
	CurrentAccountConfig bleemeoTypes.AccountConfig
	Services             []bleemeoTypes.Service
	Monitors             []bleemeoTypes.Monitor
}

// dataVersion1 contains fields that have been deleted since the version 1 of the state file, but that we
// still need to access to generate a newer state file from it, while retaining all pertinent informations
// It does *not* contain all the fields of the first version of state files, and should *not* be treated as
// an earlier version of `data`, and is exclusively manipulated in Load().
// See Load() for more details on the transformations we will apply to parse old versions.
type dataVersion1 struct {
	AccountConfig bleemeoTypes.AccountConfig
}

// SetAccountID update the AccountID.
func (c *Cache) SetAccountID(accountID string) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.AccountID = accountID
	c.dirty = true
}

// AccountID returns the AccountID.
func (c *Cache) AccountID() string {
	c.l.Lock()
	defer c.l.Unlock()

	return c.data.AccountID
}

// SetFacts update the AgentFact list.
func (c *Cache) SetFacts(facts []bleemeoTypes.AgentFact) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Facts = facts
	c.dirty = true
}

// SetServices update the Services list.
func (c *Cache) SetServices(services []bleemeoTypes.Service) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Services = services
	c.dirty = true
}

// SetContainers update the Container list.
func (c *Cache) SetContainers(containers []bleemeoTypes.Container) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Containers = containers
	c.dirty = true
}

// SetSAgentList update agent list.
func (c *Cache) SetAgentList(agentList []bleemeoTypes.Agent) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Agents = agentList
	c.dirty = true
}

// SetMonitors updates the list of monitors.
func (c *Cache) SetMonitors(monitors []bleemeoTypes.Monitor) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Monitors = monitors
	c.dirty = true
}

// Agent returns the Agent object.
func (c *Cache) Agent() (agent bleemeoTypes.Agent) {
	c.l.Lock()
	defer c.l.Unlock()

	return c.data.Agent
}

// SetAgent update the Agent object.
func (c *Cache) SetAgent(agent bleemeoTypes.Agent) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Agent = agent
	c.dirty = true
}

// SetCurrentAccountConfig updates the AccountConfig of this agent.
func (c *Cache) SetCurrentAccountConfig(accountConfig bleemeoTypes.AccountConfig) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.CurrentAccountConfig = accountConfig
	c.dirty = true
}

// CurrentAccountConfig returns our own AccountConfig.
func (c *Cache) CurrentAccountConfig() bleemeoTypes.AccountConfig {
	c.l.Lock()
	defer c.l.Unlock()

	return c.data.CurrentAccountConfig
}

// SetAccountConfigs updates all the external accounts configurations we care about
// (in particular, this is necessary for the monitors).
func (c *Cache) SetAccountConfigs(configs map[string]bleemeoTypes.AccountConfig) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.AccountConfigs = configs
}

// AccountConfigs returns the mapping between the accoutn config UUID and  list of external account configurations.
func (c *Cache) AccountConfigs() map[string]bleemeoTypes.AccountConfig {
	c.l.Lock()
	defer c.l.Unlock()

	result := make(map[string]bleemeoTypes.AccountConfig, len(c.data.AccountConfigs))
	for k, v := range c.data.AccountConfigs {
		result[k] = v
	}

	return result
}

// FactsByKey returns a map fact.key => facts.
func (c *Cache) FactsByKey() map[string]bleemeoTypes.AgentFact {
	c.l.Lock()
	defer c.l.Unlock()

	result := make(map[string]bleemeoTypes.AgentFact)
	for _, v := range c.data.Facts {
		result[v.Key] = v
	}

	return result
}

// FactsByUUID returns a map fact.id => facts.
func (c *Cache) FactsByUUID() map[string]bleemeoTypes.AgentFact {
	c.l.Lock()
	defer c.l.Unlock()

	result := make(map[string]bleemeoTypes.AgentFact)

	for _, v := range c.data.Facts {
		result[v.ID] = v
	}

	return result
}

// Facts returns a (copy) of the Facts.
func (c *Cache) Facts() []bleemeoTypes.AgentFact {
	c.l.Lock()
	defer c.l.Unlock()

	result := make([]bleemeoTypes.AgentFact, len(c.data.Facts))

	copy(result, c.data.Facts)

	return result
}

// Services returns a (copy) of the Services.
func (c *Cache) Services() []bleemeoTypes.Service {
	c.l.Lock()
	defer c.l.Unlock()

	result := make([]bleemeoTypes.Service, len(c.data.Services))

	copy(result, c.data.Services)

	return result
}

// Monitors returns a (copy) of the Monitors.
func (c *Cache) Monitors() []bleemeoTypes.Monitor {
	c.l.Lock()
	defer c.l.Unlock()

	result := make([]bleemeoTypes.Monitor, len(c.data.Monitors))

	copy(result, c.data.Monitors)

	return result
}

// MonitorsByAgentUUID returns a mapping between their agent ID and the Monitors.
func (c *Cache) MonitorsByAgentUUID() map[bleemeoTypes.AgentID]bleemeoTypes.Monitor {
	c.l.Lock()
	defer c.l.Unlock()

	result := make(map[bleemeoTypes.AgentID]bleemeoTypes.Monitor, len(c.data.Monitors))
	for _, v := range c.data.Monitors {
		result[bleemeoTypes.AgentID(v.AgentID)] = v
	}

	return result
}

// ServicesByUUID returns a map service.id => service.
func (c *Cache) ServicesByUUID() map[string]bleemeoTypes.Service {
	c.l.Lock()
	defer c.l.Unlock()

	result := make(map[string]bleemeoTypes.Service)

	for _, v := range c.data.Services {
		result[v.ID] = v
	}

	return result
}

// Containers returns a (copy) of the Containers.
func (c *Cache) Containers() (containers []bleemeoTypes.Container) {
	c.l.Lock()
	defer c.l.Unlock()

	result := make([]bleemeoTypes.Container, len(c.data.Containers))
	copy(result, c.data.Containers)

	return result
}

// ContainersByContainerID returns a map container.ContainerId => container.
func (c *Cache) ContainersByContainerID() map[string]bleemeoTypes.Container {
	c.l.Lock()
	defer c.l.Unlock()

	result := make(map[string]bleemeoTypes.Container)

	for _, v := range c.data.Containers {
		result[v.ContainerID] = v
	}

	return result
}

// ContainersByUUID returns a map container.id => container.
func (c *Cache) ContainersByUUID() map[string]bleemeoTypes.Container {
	c.l.Lock()
	defer c.l.Unlock()

	result := make(map[string]bleemeoTypes.Container)

	for _, v := range c.data.Containers {
		result[v.ID] = v
	}

	return result
}

// AgentList returns a (copy) of the list of agent.
func (c *Cache) AgentList() []bleemeoTypes.Agent {
	c.l.Lock()
	defer c.l.Unlock()

	result := make([]bleemeoTypes.Agent, len(c.data.Agents))
	copy(result, c.data.Agents)

	return result
}

// AgentsByUUID returns a map agent.id => agent.
func (c *Cache) AgentsByUUID() map[string]bleemeoTypes.Agent {
	c.l.Lock()
	defer c.l.Unlock()

	result := make(map[string]bleemeoTypes.Agent)

	for _, v := range c.data.Agents {
		result[v.ID] = v
	}

	return result
}

// SetMetricRegistrationsFail update the Metric list.
func (c *Cache) SetMetricRegistrationsFail(registrations []bleemeoTypes.MetricRegistration) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.MetricRegistrationsFail = registrations
	c.cachedFailRegistrationLookup = nil
	c.dirty = true
}

// MetricRegistrationsFail returns the Metric registration list. You should not mutute it.
func (c *Cache) MetricRegistrationsFail() (registrations []bleemeoTypes.MetricRegistration) {
	c.l.Lock()
	defer c.l.Unlock()

	return c.data.MetricRegistrationsFail
}

// MetricRegistrationsFailByKey return a map with key being the labelsText.
func (c *Cache) MetricRegistrationsFailByKey() map[string]bleemeoTypes.MetricRegistration {
	c.l.Lock()
	defer c.l.Unlock()

	if c.cachedFailRegistrationLookup == nil {
		c.cachedFailRegistrationLookup = make(map[string]bleemeoTypes.MetricRegistration, len(c.data.MetricRegistrationsFail))

		for _, v := range c.data.MetricRegistrationsFail {
			c.cachedFailRegistrationLookup[v.LabelsText] = v
		}
	}

	return c.cachedFailRegistrationLookup
}

//

// SetMetrics update the Metric list.
func (c *Cache) SetMetrics(metrics []bleemeoTypes.Metric) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Metrics = metrics
	c.cachedMetricLookup = nil
	c.dirty = true
}

// Metrics returns a (copy) of the Metrics.
func (c *Cache) Metrics() (metrics []bleemeoTypes.Metric) {
	c.l.Lock()
	defer c.l.Unlock()

	result := make([]bleemeoTypes.Metric, len(c.data.Metrics))
	copy(result, c.data.Metrics)

	return result
}

// MetricLookupFromList return a map[MetricLabelItem]Metric of all known Metrics
//
// This is an optimized version of common.MetricLookupFromList(c.Metrics()).
// You should NOT mutate the result.
func (c *Cache) MetricLookupFromList() map[string]bleemeoTypes.Metric {
	c.l.Lock()
	defer c.l.Unlock()

	if c.cachedMetricLookup == nil {
		c.cachedMetricLookup = common.MetricLookupFromList(c.data.Metrics)
	}

	return c.cachedMetricLookup
}

// MetricsByUUID returns a map metric.id => metric.
func (c *Cache) MetricsByUUID() map[string]bleemeoTypes.Metric {
	c.l.Lock()
	defer c.l.Unlock()

	result := make(map[string]bleemeoTypes.Metric)

	for _, v := range c.data.Metrics {
		result[v.ID] = v
	}

	return result
}

// Save saves the cache into State.
func (c *Cache) Save() {
	c.l.Lock()
	defer c.l.Unlock()

	if c.state == nil {
		return
	}

	if !c.dirty {
		return
	}

	if err := c.state.Set(cacheKey, c.data); err != nil {
		logger.V(1).Printf("Unable to save Bleemeo connector cache: %v", err)
		return
	}

	c.dirty = false
}

// Load loads the cache from State.
func Load(state bleemeoTypes.State) *Cache {
	cache := &Cache{
		state: state,
	}

	var newData data

	if err := state.Get(cacheKey, &newData); err != nil {
		logger.V(1).Printf("Unable to load Bleemeo connector cache: %v", err)
	}

	switch newData.Version {
	case 0:
		logger.V(2).Printf("Bleemeo connector cache is too absent, starting with new empty cache")

		cache.data.Version = cacheVersion
	case 1:
		logger.V(1).Printf("Version 1 of the cache found, upgrading it.")

		// the main change between V1 and V2 was the renaming of AccoutConfig to CurrentAccountConfig, and
		// the addition of Monitors and AccountConfigs
		var oldCache dataVersion1

		if err := state.Get(cacheKey, &oldCache); err == nil {
			newData.CurrentAccountConfig = oldCache.AccountConfig
		}

		fallthrough
	case 2:
		logger.V(1).Printf("Version 2 of the cache found, upgrading it.")

		// well... containers had multiple fields renamed... lets drop it
		newData.Containers = nil

		fallthrough
	case 3:
		logger.V(1).Printf("Version 3 of cache found, upgrading it.")

		// Version 4 stopped using "_item" to store Bleemeo item and use "item"
		for i, m := range newData.Metrics {
			labels := types.TextToLabels(m.LabelsText)
			if v, ok := labels["_item"]; ok {
				labels[types.LabelItem] = v
				delete(labels, "_item")
				newData.Metrics[i].LabelsText = types.LabelsToText(labels)
			}
		}

		fallthrough
	case cacheVersion:
		newData.Version = cacheVersion
		cache.data = newData
	default:
		logger.V(2).Printf("Bleemeo connector cache is too recent. Discarding content")

		cache.data.Version = cacheVersion
	}

	for i, m := range cache.data.Metrics {
		m.Labels = types.TextToLabels(m.LabelsText)

		cache.data.Metrics[i] = m
	}

	return cache
}
