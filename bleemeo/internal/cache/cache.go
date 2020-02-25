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
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/logger"
	"glouton/types"
	"sync"
)

const cacheVersion = 1
const cacheKey = "CacheBleemeoConnector"

// Cache store information about object registered in Bleemeo API
type Cache struct {
	data  data
	l     sync.Mutex
	dirty bool
	state bleemeoTypes.State
}

type data struct {
	Version       int
	AccountID     string
	Facts         []bleemeoTypes.AgentFact
	Containers    []bleemeoTypes.Container
	Metrics       []bleemeoTypes.Metric
	Agent         bleemeoTypes.Agent
	AccountConfig bleemeoTypes.AccountConfig
	Services      []bleemeoTypes.Service
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
func (c *Cache) SetFacts(facts []bleemeoTypes.AgentFact) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Facts = facts
	c.dirty = true
}

// SetServices update the Services list
func (c *Cache) SetServices(services []bleemeoTypes.Service) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Services = services
	c.dirty = true
}

// SetContainers update the Container list
func (c *Cache) SetContainers(containers []bleemeoTypes.Container) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Containers = containers
	c.dirty = true
}

// Agent returns the Agent object
func (c *Cache) Agent() (agent bleemeoTypes.Agent) {
	c.l.Lock()
	defer c.l.Unlock()

	return c.data.Agent
}

// SetAgent update the Agent object
func (c *Cache) SetAgent(agent bleemeoTypes.Agent) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Agent = agent
	c.dirty = true
}

// SetAccountConfig update the AccountConfig object
func (c *Cache) SetAccountConfig(accountConfig bleemeoTypes.AccountConfig) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.AccountConfig = accountConfig
	c.dirty = true
}

// AccountConfig returns AccountConfig
func (c *Cache) AccountConfig() bleemeoTypes.AccountConfig {
	c.l.Lock()
	defer c.l.Unlock()

	return c.data.AccountConfig
}

// FactsByKey returns a map fact.key => facts
func (c *Cache) FactsByKey() map[string]bleemeoTypes.AgentFact {
	c.l.Lock()
	defer c.l.Unlock()

	result := make(map[string]bleemeoTypes.AgentFact)
	for _, v := range c.data.Facts {
		result[v.Key] = v
	}
	return result
}

// FactsByUUID returns a map fact.id => facts
func (c *Cache) FactsByUUID() map[string]bleemeoTypes.AgentFact {
	c.l.Lock()
	defer c.l.Unlock()
	result := make(map[string]bleemeoTypes.AgentFact)
	for _, v := range c.data.Facts {
		result[v.ID] = v
	}
	return result
}

// Facts returns a (copy) of the Facts
func (c *Cache) Facts() []bleemeoTypes.AgentFact {
	c.l.Lock()
	defer c.l.Unlock()
	result := make([]bleemeoTypes.AgentFact, len(c.data.Facts))
	copy(result, c.data.Facts)
	return result
}

// Services returns a (copy) of the Services
func (c *Cache) Services() []bleemeoTypes.Service {
	c.l.Lock()
	defer c.l.Unlock()
	result := make([]bleemeoTypes.Service, len(c.data.Services))
	copy(result, c.data.Services)
	return result
}

// ServicesByUUID returns a map service.id => service
func (c *Cache) ServicesByUUID() map[string]bleemeoTypes.Service {
	c.l.Lock()
	defer c.l.Unlock()
	result := make(map[string]bleemeoTypes.Service)
	for _, v := range c.data.Services {
		result[v.ID] = v
	}
	return result
}

// Containers returns a (copy) of the Containers
func (c *Cache) Containers() (containers []bleemeoTypes.Container) {
	c.l.Lock()
	defer c.l.Unlock()

	result := make([]bleemeoTypes.Container, len(c.data.Containers))
	copy(result, c.data.Containers)
	return result
}

// ContainersByContainerID returns a map container.ContainerId => container
func (c *Cache) ContainersByContainerID() map[string]bleemeoTypes.Container {
	c.l.Lock()
	defer c.l.Unlock()
	result := make(map[string]bleemeoTypes.Container)
	for _, v := range c.data.Containers {
		result[v.DockerID] = v
	}
	return result
}

// ContainersByUUID returns a map container.id => container
func (c *Cache) ContainersByUUID() map[string]bleemeoTypes.Container {
	c.l.Lock()
	defer c.l.Unlock()
	result := make(map[string]bleemeoTypes.Container)
	for _, v := range c.data.Containers {
		result[v.ID] = v
	}
	return result
}

// SetMetrics update the Metric list
func (c *Cache) SetMetrics(metrics []bleemeoTypes.Metric) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Metrics = metrics
	c.dirty = true
}

// Metrics returns a (copy) of the Metrics
func (c *Cache) Metrics() (metrics []bleemeoTypes.Metric) {
	c.l.Lock()
	defer c.l.Unlock()

	result := make([]bleemeoTypes.Metric, len(c.data.Metrics))
	copy(result, c.data.Metrics)
	return result
}

// MetricsByUUID returns a map metric.id => metric
func (c *Cache) MetricsByUUID() map[string]bleemeoTypes.Metric {
	c.l.Lock()
	defer c.l.Unlock()
	result := make(map[string]bleemeoTypes.Metric)
	for _, v := range c.data.Metrics {
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

	if err := c.state.Set(cacheKey, c.data); err != nil {
		logger.V(1).Printf("Unable to save Bleemeo connector cache: %v", err)
		return
	}
	c.dirty = false
}

// Load loads the cache from State
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
	case cacheVersion:
		cache.data = newData
	default:
		logger.V(2).Printf("Bleemeo connector cache is too recent. Discarding content")
		cache.data.Version = cacheVersion
	}

	for i, m := range cache.data.Metrics {
		m.Labels = types.TextToLabels(m.LabelsText)
		if m.Item != "" {
			// This metric is using Bleemeo mode, labels must only contains name and item
			// it stored in the Item fields
			m.Labels = map[string]string{
				types.LabelName: m.Labels[types.LabelName],
			}
		}
		cache.data.Metrics[i] = m
	}

	return cache
}
