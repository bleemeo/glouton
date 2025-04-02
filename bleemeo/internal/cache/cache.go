// Copyright 2015-2025 Bleemeo
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
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/common"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
)

const (
	cacheVersion = 7
	cacheKey     = "CacheBleemeoConnector"
)

// Cache store information about object registered in Bleemeo API.
type Cache struct {
	data  data
	l     sync.Mutex
	dirty bool
	state bleemeoTypes.State

	cachedServiceLookup          map[common.ServiceNameInstance]bleemeoTypes.Service
	cachedMetricLookup           map[string]bleemeoTypes.Metric
	cachedFailRegistrationLookup map[string]bleemeoTypes.MetricRegistration
}

type data struct {
	Version                 int
	AccountID               string
	Facts                   []bleemeoTypes.AgentFact
	Containers              []bleemeoTypes.Container
	Agents                  []bleemeoTypes.Agent
	AgentTypes              []bleemeoTypes.AgentType
	Applications            []bleemeoTypes.Application
	Metrics                 []bleemeoTypes.Metric
	MetricRegistrationsFail []bleemeoTypes.MetricRegistration
	Agent                   bleemeoTypes.Agent
	AccountConfigs          []bleemeoTypes.AccountConfig
	AgentConfigs            []bleemeoTypes.AgentConfig
	Services                []bleemeoTypes.Service
	Monitors                []bleemeoTypes.Monitor
}

// dataVersion1 contains fields that have been deleted since the version 1 of the state file, but that we
// still need to access to generate a newer state file from it, while retaining all pertinent information
// It does *not* contain all the fields of the first version of state files, and should *not* be treated as
// an earlier version of `data`, and is exclusively manipulated in Load().
// See Load() for more details on the transformations we will apply to parse old versions.
type dataVersion1 struct {
	AccountConfig bleemeoTypes.AccountConfig
}

// dataVersion5, like dataVersion1, only contains fields from V5 that we need to read.
type dataVersion5 struct {
	CurrentAccountConfig bleemeoTypes.AccountConfig
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
	c.cachedServiceLookup = nil
	c.dirty = true
}

// SetContainers update the Container list.
func (c *Cache) SetContainers(containers []bleemeoTypes.Container) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Containers = containers
	c.dirty = true
}

// SetAgentList update agent list.
func (c *Cache) SetAgentList(agentList []bleemeoTypes.Agent) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Agents = agentList
	c.dirty = true
}

// SetAgentTypes update agent list.
func (c *Cache) SetAgentTypes(agentTypes []bleemeoTypes.AgentType) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.AgentTypes = agentTypes
	c.dirty = true
}

// SetApplications update the Applications list.
func (c *Cache) SetApplications(applications []bleemeoTypes.Application) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.Applications = applications
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

// CurrentAccountConfig returns our own AccountConfig. If the
// configuration isn't found and a default is used.
func (c *Cache) CurrentAccountConfig() (bleemeoTypes.GloutonAccountConfig, bool) {
	searchMap := c.AccountConfigsByUUID()

	c.l.Lock()
	defer c.l.Unlock()

	result, ok := searchMap[c.data.Agent.CurrentAccountConfigID]

	if _, ok := result.AgentConfigByName[bleemeo.AgentType_Agent]; !ok {
		return result, false
	}

	return result, ok
}

// SetAccountConfigs updates all the external accounts configurations we care about
// (in particular, this is necessary for the monitors).
func (c *Cache) SetAccountConfigs(configs []bleemeoTypes.AccountConfig) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.AccountConfigs = configs
}

// SetAgentConfigs updates all the external accounts configurations we care about
// (in particular, this is necessary for the monitors).
func (c *Cache) SetAgentConfigs(configs []bleemeoTypes.AgentConfig) {
	c.l.Lock()
	defer c.l.Unlock()

	c.data.AgentConfigs = configs
}

// AgentConfigs returns a copy of the AgentConfig.
func (c *Cache) AgentConfigs() []bleemeoTypes.AgentConfig {
	c.l.Lock()
	defer c.l.Unlock()

	result := make([]bleemeoTypes.AgentConfig, len(c.data.AgentConfigs))

	copy(result, c.data.AgentConfigs)

	return result
}

// AccountConfigs returns a (copy) of the AccountConfig.
func (c *Cache) AccountConfigs() []bleemeoTypes.AccountConfig {
	c.l.Lock()
	defer c.l.Unlock()

	result := make([]bleemeoTypes.AccountConfig, len(c.data.AccountConfigs))
	copy(result, c.data.AccountConfigs)

	return result
}

func (c *Cache) AccountConfigsByUUID() map[string]bleemeoTypes.GloutonAccountConfig {
	c.l.Lock()
	defer c.l.Unlock()

	result := make(map[string]bleemeoTypes.GloutonAccountConfig, len(c.data.AccountConfigs))

	for _, accountConfig := range c.data.AccountConfigs {
		config := bleemeoTypes.GloutonAccountConfig{
			ID:                    accountConfig.ID,
			Name:                  accountConfig.Name,
			LiveProcessResolution: time.Duration(accountConfig.LiveProcessResolution) * time.Second,
			LiveProcess:           accountConfig.LiveProcess,
			DockerIntegration:     accountConfig.DockerIntegration,
			SNMPIntegration:       accountConfig.SNMPIntegration,
			VSphereIntegration:    accountConfig.VSphereIntegration,
			Suspended:             accountConfig.Suspended,
			AgentConfigByName:     make(map[bleemeo.AgentType]bleemeoTypes.GloutonAgentConfig),
			AgentConfigByID:       make(map[string]bleemeoTypes.GloutonAgentConfig),
			MaxCustomMetrics:      accountConfig.MaxCustomMetrics,
		}

		for _, agentType := range c.data.AgentTypes {
			for _, agentConfig := range c.data.AgentConfigs {
				if agentConfig.AgentType == agentType.ID && agentConfig.AccountConfig == accountConfig.ID {
					config.AgentConfigByName[agentType.Name] = bleemeoTypes.GloutonAgentConfig{
						MetricResolution: time.Duration(agentConfig.MetricResolution) * time.Second,
						MetricsAllowlist: allowListToMap(agentConfig.MetricsAllowlist),
					}

					config.AgentConfigByID[agentType.ID] = config.AgentConfigByName[agentType.Name]

					break
				}
			}
		}

		if _, ok := config.AgentConfigByName[bleemeo.AgentType_SNMP]; !ok {
			config.SNMPIntegration = false
		}

		_, isVM := config.AgentConfigByName[bleemeo.AgentType_vSphereVM]
		_, isHost := config.AgentConfigByName[bleemeo.AgentType_vSphereHost]
		_, isCluster := config.AgentConfigByName[bleemeo.AgentType_vSphereCluster]

		if !isVM && !isHost && !isCluster {
			config.VSphereIntegration = false
		}

		result[accountConfig.ID] = config
	}

	return result
}

// Applications returns a (copy) of the Applications.
func (c *Cache) Applications() []bleemeoTypes.Application {
	c.l.Lock()
	defer c.l.Unlock()

	result := make([]bleemeoTypes.Application, len(c.data.Applications))

	copy(result, c.data.Applications)

	return result
}

// allowListToMap return a map with from an allow-list.
func allowListToMap(list string) map[string]bool {
	if len(list) == 0 {
		return nil
	}

	result := make(map[string]bool)

	separator := func(r rune) bool {
		if r == ',' {
			return true
		}

		return unicode.IsSpace(r)
	}

	for _, n := range strings.FieldsFunc(list, separator) {
		result[strings.TrimSpace(n)] = true
	}

	return result
}

// FactsByKey returns a map fact.agentid => fact.key => facts.
func (c *Cache) FactsByKey() map[string]map[string]bleemeoTypes.AgentFact {
	c.l.Lock()
	defer c.l.Unlock()

	result := make(map[string]map[string]bleemeoTypes.AgentFact)
	for _, v := range c.data.Facts {
		if _, ok := result[v.AgentID]; !ok {
			estimatedSize := len(c.data.Facts)
			if len(c.data.Agents) > 1 {
				estimatedSize /= len(c.data.Agents)
			}

			result[v.AgentID] = make(map[string]bleemeoTypes.AgentFact, estimatedSize)
		}

		// In case of duplicate facts, choose the key with the smallest ID.
		// Having a consistent selection of which duplicate facts to select is important for
		// the duplicate state.json detection:
		// If the glouton registered a new fact with a different FQDN but didn't yet deleted
		// the old one (due to crash for example), it could return a different facts in the
		// FactsByKey() call for oldFacts and newFacts, resulting in isDuplicatedUsingFacts thinking
		// the facts is changed by another agent.
		if existing, ok := result[v.AgentID][v.Key]; !ok || existing.ID > v.ID {
			result[v.AgentID][v.Key] = v
		}
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

// ServiceLookupFromList return a map[ServiceNameInstance]Service of all known Services
//
// This is an optimized version of common.ServiceLookupFromList(c.Services()).
// You should NOT mutate the result.
func (c *Cache) ServiceLookupFromList() map[common.ServiceNameInstance]bleemeoTypes.Service {
	c.l.Lock()
	defer c.l.Unlock()

	if c.cachedServiceLookup == nil {
		c.cachedServiceLookup = common.ServiceLookupFromList(c.data.Services)
	}

	return c.cachedServiceLookup
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

// Agents returns a (copy) of the list of agent.
func (c *Cache) Agents() []bleemeoTypes.Agent {
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

// AgentTypes returns a (copy) of the list of agent types.
func (c *Cache) AgentTypes() []bleemeoTypes.AgentType {
	c.l.Lock()
	defer c.l.Unlock()

	result := make([]bleemeoTypes.AgentType, len(c.data.AgentTypes))
	copy(result, c.data.AgentTypes)

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

	versionUpgrade := map[int]func(bleemeoTypes.State, data) data{
		1: upgradeV1,
		2: upgradeV2,
		3: upgradeV3,
		4: upgradeV4,
		5: upgradeV5,
		6: upgradeV6,
	}

	upgradeCount := 0
	for newData.Version != cacheVersion {
		if upgradeCount > cacheVersion {
			logger.V(2).Printf("Too many try to upgrade cache version. Discarding cache content")

			newData = data{
				Version: cacheVersion,
			}

			break
		}

		if newData.Version == 0 {
			logger.V(2).Printf("Bleemeo connector cache is too absent, starting with new empty cache")

			newData = data{
				Version: cacheVersion,
			}
		}

		upgradeFunc := versionUpgrade[newData.Version]
		if upgradeFunc == nil {
			logger.V(2).Printf("No upgrade path from version %d to %d. Discarding cache content", newData.Version, cacheVersion)

			newData = data{
				Version: cacheVersion,
			}
		} else {
			logger.V(1).Printf("Upgrading version %d of the cache", newData.Version)
			newData = upgradeFunc(state, newData)
		}

		upgradeCount++
	}

	cache.data = newData

	for i, m := range cache.data.Metrics {
		m.Labels = types.TextToLabels(m.LabelsText)

		cache.data.Metrics[i] = m
	}

	return cache
}

func upgradeV1(state bleemeoTypes.State, newData data) data {
	// the main change between V1 and V2 was the renaming of AccountConfig to CurrentAccountConfig, and
	// the addition of Monitors and AccountConfigs
	var oldCache dataVersion1

	if err := state.Get(cacheKey, &oldCache); err == nil {
		newData.AccountConfigs = []bleemeoTypes.AccountConfig{oldCache.AccountConfig}
	}

	newData.Version = 2

	return newData
}

func upgradeV2(_ bleemeoTypes.State, newData data) data {
	// well... containers had multiple fields renamed... lets drop it
	newData.Containers = nil
	newData.Version = 3

	return newData
}

func upgradeV3(_ bleemeoTypes.State, newData data) data {
	// Version 4 stopped using "_item" to store Bleemeo item and use "item"
	for i, m := range newData.Metrics {
		labels := types.TextToLabels(m.LabelsText)
		if v, ok := labels["_item"]; ok {
			labels[types.LabelItem] = v
			delete(labels, "_item")
			newData.Metrics[i].LabelsText = types.LabelsToText(labels)
		}
	}

	newData.Version = 4

	return newData
}

func upgradeV4(_ bleemeoTypes.State, newData data) data {
	// Version 5 added "AgentID" on Metric object
	// With version 4, only metric of monitor could belong to another agent
	for i, m := range newData.Metrics {
		for _, v := range newData.Monitors {
			if m.ServiceID == v.ID {
				m.AgentID = v.AgentID

				break
			}
		}

		if m.AgentID == "" {
			m.AgentID = newData.Agent.ID
		}

		newData.Metrics[i] = m
	}

	newData.Version = 5

	return newData
}

func upgradeV5(state bleemeoTypes.State, newData data) data {
	// Version 6 dropped the CurrentAccountConfig and store all config in
	// AccountConfigs.
	var oldCache dataVersion5

	if err := state.Get(cacheKey, &oldCache); err == nil {
		found := false

		for _, ac := range newData.AccountConfigs {
			if ac.ID == oldCache.CurrentAccountConfig.ID {
				found = true

				break
			}
		}

		if !found && oldCache.CurrentAccountConfig.ID != "" {
			newData.AccountConfigs = append(newData.AccountConfigs, oldCache.CurrentAccountConfig)
		}
	}

	newData.Version = 6

	return newData
}

func upgradeV6(_ bleemeoTypes.State, newData data) data {
	// Version 7 dropped stack on service. The forward migration
	// does nothing (it just drop it). But we bump the version
	// so that backward migration will discard and regenerate the cache
	// to re-fill the stack value.
	newData.Version = 7

	return newData
}
