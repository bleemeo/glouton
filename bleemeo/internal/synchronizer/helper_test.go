// Copyright 2015-2022 Bleemeo
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

package synchronizer

import (
	"context"
	"errors"
	"fmt"
	"glouton/bleemeo/internal/cache"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/config"
	"glouton/discovery"
	"glouton/facts"
	"glouton/prometheus/exporter/snmp"
	"glouton/prometheus/model"
	"glouton/store"
	"glouton/types"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/prometheus/model/labels"
)

const (
	idAny            = "this constant in test when the object ID doesn't matter. An helper function will replace it by actual ID"
	idObjectNotFound = "idAny was used but object isn't found"
)

var (
	errMethodCalled            = errors.New("syncMethod is called unexpectedly")
	errMethodNotCalled         = errors.New("syncMethod is NOT called")
	errMethodCalledWithoutFull = errors.New("syncMethod is called without full=true")
	errMethodCalledWithFull    = errors.New("syncMethod is called without full=false")
)

type syncTestHelper struct {
	api        *mockAPI
	s          *Synchronizer
	cfg        config.Config
	facts      *facts.FactProviderMock
	containers []facts.Container
	cache      *cache.Cache
	state      *stateMock
	discovery  *discovery.MockDiscoverer
	store      *store.Store
	httpServer *httptest.Server

	// Following fields are options used by some method
	SNMP               []*snmp.Target
	MetricFormat       types.MetricFormat
	NotifyLabelsUpdate func()
}

// newHelper create an helper with all permanent resource: API, cache, state.
// It does not create the Synchronizer (use initSynchronizer).
func newHelper(t *testing.T) *syncTestHelper {
	t.Helper()

	api := newAPI()

	helper := &syncTestHelper{
		api:   api,
		facts: facts.NewMockFacter(nil),
		cache: &cache.Cache{},
		state: newStateMock(),
		discovery: &discovery.MockDiscoverer{
			UpdatedAt: api.now.Now(),
		},

		MetricFormat: types.MetricFormatBleemeo,
	}

	helper.httpServer = helper.api.Server()

	helper.cfg = config.Config{
		Logging: config.Logging{
			Level: "debug",
		},
		Bleemeo: config.Bleemeo{
			APIBase:         helper.httpServer.URL,
			AccountID:       accountID,
			RegistrationKey: registrationKey,
		},
		Blackbox: config.Blackbox{
			Enable:      true,
			ScraperName: "paris",
		},
	}

	helper.facts.SetFact("fqdn", testAgentFQDN)

	return helper
}

// preregisterAgent set resource in API, Cache & State as if the agent was previously
// registered.
func (helper *syncTestHelper) preregisterAgent(t *testing.T) {
	t.Helper()

	const password = "the initial password"

	_ = helper.state.SetBleemeoCredentials(testAgent.ID, password)

	helper.api.JWTPassword = password
	helper.api.JWTUsername = testAgent.ID + "@bleemeo.com"

	helper.api.resources[mockAPIResourceAgent].AddStore(testAgent)
}

// addMonitorOnAPI pre-create a monitor in the API.
func (helper *syncTestHelper) addMonitorOnAPI(t *testing.T) serviceMonitor {
	t.Helper()

	newMonitorCopy := newMonitor
	newMonitorCopy.AccountConfig = helper.api.AccountConfigNewAgent

	helper.api.resources[mockAPIResourceService].AddStore(newMonitorCopy)

	return newMonitorCopy
}

// Create or re-create the Synchronizer. It also reset the store.
func (helper *syncTestHelper) initSynchronizer(t *testing.T) {
	t.Helper()

	helper.store = store.New(time.Hour, 2*time.Hour)

	helper.store.InternalSetNowAndRunOnce(context.Background(), helper.api.now.Now)

	var docker bleemeoTypes.DockerProvider
	if helper.containers != nil {
		docker = &mockDocker{helper: helper}
	}

	s, err := newForTest(Option{
		Cache: helper.cache,
		GlobalOption: bleemeoTypes.GlobalOption{
			Config:                  helper.cfg,
			Facts:                   helper.facts,
			State:                   helper.state,
			Docker:                  docker,
			Discovery:               helper.discovery,
			Store:                   helper.store,
			MonitorManager:          mockMonitorManager{},
			NotifyFirstRegistration: func() {},
			MetricFormat:            helper.MetricFormat,
			SNMP:                    helper.SNMP,
			SNMPOnlineTarget:        func() int { return len(helper.SNMP) },
			NotifyLabelsUpdate:      helper.NotifyLabelsUpdate,
			IsContainerEnabled:      facts.ContainerFilter{}.ContainerEnabled,
			IsMetricAllowed:         func(_ map[string]string) bool { return true },
			BlackboxScraperName:     helper.cfg.Blackbox.ScraperName,
		},
	}, helper.api.now.Now)
	if err != nil {
		t.Fatalf("newWithNow failed: %v", err)
	}

	helper.s = s

	// Do actions done by s.Run()
	s.ctx = context.Background()
	s.startedAt = helper.api.now.Now()

	if err = s.setClient(); err != nil {
		t.Fatalf("setClient failed: %v", err)
	}

	// Some part of synchronizer don't like having *exact* same time for Now() & startedAt
	helper.api.now.Advance(time.Microsecond)
}

// pushPoints write points to the store with current time.
// It known some meta labels (see code for supported one).
func (helper *syncTestHelper) pushPoints(t *testing.T, metrics []labels.Labels) {
	t.Helper()

	points := make([]types.MetricPoint, 0, len(metrics))

	for _, m := range metrics {
		mCopy := labels.New(m...)
		annotations := model.MetaLabelsToAnnotation(mCopy)
		mCopy = model.DropMetaLabels(mCopy)

		points = append(points, types.MetricPoint{
			Point: types.Point{
				Time:  helper.api.now.Now(),
				Value: 42.0,
			},
			Labels:      mCopy.Map(),
			Annotations: annotations,
		})
	}

	if helper.store == nil {
		t.Fatal("pushPoints called before store is initilized")
	}

	helper.store.PushPoints(context.Background(), points)
}

func (helper *syncTestHelper) Close() {
	if helper.httpServer != nil {
		helper.httpServer.Close()

		helper.httpServer = nil
	}
}

func (helper *syncTestHelper) AddTime(d time.Duration) {
	helper.api.now.Advance(d)
}

func (helper *syncTestHelper) SetTime(now time.Time) {
	helper.api.now.now = now
}

func (helper *syncTestHelper) SetTimeToNextFullSync() {
	helper.api.now.now = helper.s.nextFullSync.Add(time.Second)
}

func (helper *syncTestHelper) Now() time.Time {
	return helper.api.now.Now()
}

func (helper *syncTestHelper) runOnce(t *testing.T) error {
	t.Helper()

	result := helper.runOnceWithResult(t)

	if result.err != nil {
		return fmt.Errorf("runOnce failed: %w", result.err)
	}

	if helper.api.ServerErrorCount > 0 {
		return fmt.Errorf("%w: %d server error, last %v", errServerError, helper.api.ServerErrorCount, helper.api.LastServerError)
	}

	if helper.api.ClientErrorCount > 0 {
		return fmt.Errorf("%w: %d client error", errClientError, helper.api.ClientErrorCount)
	}

	return nil
}

func (helper *syncTestHelper) runOnceWithResult(t *testing.T) runOnceResult {
	t.Helper()

	helper.api.ResetCount()

	return helper.runOnceNoReset(t)
}

func (helper *syncTestHelper) runOnceNoReset(t *testing.T) runOnceResult {
	t.Helper()

	ctx := context.Background()

	result := runOnceResult{}

	if helper.s == nil {
		result.err = fmt.Errorf("%w: runOnce called before initSynchronizer", errUnexpectedOperation)

		return result
	}

	result.runCount++
	result.syncMethod, result.err = helper.s.runOnce(ctx, false)

	return result
}

// runUntilNoError run runOnceWithResult until it don't return error (or maxRun is reached).
// Each additional run, clock advance of timeStep.
// runOnceResult contains merges from all runs.
func (helper *syncTestHelper) runUntilNoError(t *testing.T, maxRun int, timeStep time.Duration) runOnceResult {
	t.Helper()

	helper.api.ResetCount()

	result := runOnceResult{}

	for run := 0; run < maxRun; run++ {
		tmp := helper.runOnceNoReset(t)
		result = mergeResult(result, tmp)

		result.err = tmp.err

		if result.err == nil {
			return result
		}

		helper.AddTime(timeStep)
	}

	result.err = fmt.Errorf("still had error after %d runs. Last err: %w", result.runCount, result.err)

	return result
}

// SetAPIMetrics define the list of metric present on Bleemeo API mock.
func (helper *syncTestHelper) SetAPIMetrics(metrics ...metricPayload) {
	tmp := make([]interface{}, 0, len(metrics))

	for _, m := range metrics {
		tmp = append(tmp, m)
	}

	helper.api.resources[mockAPIResourceMetric].SetStore(tmp...)
}

// SetAPIServices define the list of service present on Bleemeo API mock.
func (helper *syncTestHelper) SetAPIServices(services ...servicePayload) {
	tmp := make([]interface{}, 0, len(services))

	for _, m := range services {
		tmp = append(tmp, serviceMonitor{
			Monitor: bleemeoTypes.Monitor{
				Service: m.Service,
				AgentID: m.Agent,
			},
			Account:   m.Account,
			IsMonitor: false,
		})
	}

	helper.api.resources[mockAPIResourceService].SetStore(tmp...)
}

// SetAPIAccountConfig define the list of AccountConfig and AgentConfig present on Bleemeo API mock.
// It also enable using the AccountConfig as default config for new Agent.
func (helper *syncTestHelper) SetAPIAccountConfig(accountConfig bleemeoTypes.AccountConfig, agentConfigs []bleemeoTypes.AgentConfig) {
	helper.api.AccountConfigNewAgent = accountConfig.ID
	helper.api.resources[mockAPIResourceAccountConfig].SetStore(accountConfig)

	tmp := make([]interface{}, 0, len(agentConfigs))

	for _, x := range agentConfigs {
		tmp = append(tmp, x)
	}

	helper.api.resources[mockAPIResourceAgentConfig].SetStore(tmp...)
}

// SetCacheMetrics define the list of metric present in Glouton cache.
func (helper *syncTestHelper) SetCacheMetrics(metrics ...metricPayload) {
	tmp := make([]bleemeoTypes.Metric, 0, len(metrics))

	for _, m := range metrics {
		tmp = append(tmp, m.metricFromAPI(helper.Now()))
	}

	helper.s.option.Cache.SetMetrics(tmp)
}

// MetricsFromAPI returns metrics present on Bleemeo API mock.
func (helper *syncTestHelper) MetricsFromAPI() []metricPayload {
	var metrics []metricPayload

	helper.api.resources[mockAPIResourceMetric].Store(&metrics)
	sort.Slice(metrics, func(i, j int) bool {
		return metrics[i].ID < metrics[j].ID
	})

	return metrics
}

// AgentsFromAPI returns agents present on Bleemeo API mock.
func (helper *syncTestHelper) AgentsFromAPI() []payloadAgent {
	var agents []payloadAgent

	helper.api.resources[mockAPIResourceAgent].Store(&agents)
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].ID < agents[j].ID
	})

	return agents
}

// FactsFromAPI returns facts present on Bleemeo API mock.
func (helper *syncTestHelper) FactsFromAPI() []bleemeoTypes.AgentFact {
	var facts []bleemeoTypes.AgentFact

	helper.api.resources[mockAPIResourceAgentFact].Store(&facts)
	sort.Slice(facts, func(i, j int) bool {
		return facts[i].ID < facts[j].ID
	})

	return facts
}

// ServicesFromAPI returns services present on Bleemeo API mock.
func (helper *syncTestHelper) ServicesFromAPI() []serviceMonitor {
	var services []serviceMonitor

	helper.api.resources[mockAPIResourceService].Store(&services)
	sort.Slice(services, func(i, j int) bool {
		return services[i].ID < services[j].ID
	})

	return services
}

// assertAgentsInAPI check that wanted agents (and only wanted agents) are present in API.
// This is mostly a wrapper around cmp.Diff which also do:
// * replace idAny by corresponding ID (match metric by same fqdn)
// * sort list by ID.
func (helper *syncTestHelper) assertAgentsInAPI(t *testing.T, want []payloadAgent) {
	t.Helper()

	agents := helper.AgentsFromAPI()

	copyWant := make([]payloadAgent, 0, len(want))

	for _, x := range want {
		if x.ID == idAny {
			x.ID = idObjectNotFound

			for _, existing := range agents {
				if x.FQDN == existing.FQDN {
					x.ID = existing.ID

					break
				}
			}
		}

		copyWant = append(copyWant, x)
	}

	optSort := cmpopts.SortSlices(func(x payloadAgent, y payloadAgent) bool { return x.ID < y.ID })
	if diff := cmp.Diff(copyWant, agents, cmpopts.EquateEmpty(), optSort); diff != "" {
		t.Errorf("agents mismatch (-want +got)\n%s", diff)
	}
}

// assertServicesInAPI check that wanted agents (and only wanted agents) are present in API.
// This is mostly a wrapper around cmp.Diff which also do:
// * replace idAny by corresponding ID (match service by same name, instance, url)
// * sort list by ID.
func (helper *syncTestHelper) assertServicesInAPI(t *testing.T, want []serviceMonitor) {
	t.Helper()

	services := helper.ServicesFromAPI()

	copyWant := make([]serviceMonitor, 0, len(want))

	for _, x := range want {
		if x.ID == idAny {
			x.ID = idObjectNotFound

			for _, existing := range services {
				if x.Label == existing.Label && x.Instance == existing.Instance && x.URL == existing.URL {
					x.ID = existing.ID

					break
				}
			}
		}

		copyWant = append(copyWant, x)
	}

	optSort := cmpopts.SortSlices(func(x serviceMonitor, y serviceMonitor) bool { return x.ID < y.ID })
	if diff := cmp.Diff(copyWant, services, cmpopts.EquateEmpty(), optSort); diff != "" {
		t.Errorf("services mismatch (-want +got)\n%s", diff)
	}
}

// assertMetricsInAPI check that wanted metrics (and only wanted metrics) are present in API.
// This is mostly a wrapper around cmp.Diff which also do:
// * replace idAny by corresponding ID (match metric by same agentID, label, item & labels_text)
// * sort list by ID.
func (helper *syncTestHelper) assertMetricsInAPI(t *testing.T, want []metricPayload) {
	t.Helper()

	metrics := helper.MetricsFromAPI()

	copyWant := make([]metricPayload, 0, len(want))

	for _, m := range want {
		if m.ID == idAny {
			m.ID = idObjectNotFound

			for _, existingM := range metrics {
				if m.AgentID == existingM.AgentID && m.Name == existingM.Name && m.LabelsText == existingM.LabelsText && m.Item == existingM.Item {
					m.ID = existingM.ID

					break
				}
			}
		}

		copyWant = append(copyWant, m)
	}

	optSort := cmpopts.SortSlices(func(x metricPayload, y metricPayload) bool { return x.ID < y.ID })
	if diff := cmp.Diff(copyWant, metrics, cmpopts.EquateEmpty(), optSort); diff != "" {
		t.Errorf("metrics mismatch (-want +got)\n%s", diff)
	}
}

// assertFactsInAPI check that wanted facts (and only wanted facts) are present in API.
// This is mostly a wrapper around cmp.Diff which also do:
// * replace idAny but corresponding ID (match metric by same agentID, key & value)
// * sort list by ID.
func (helper *syncTestHelper) assertFactsInAPI(t *testing.T, want []bleemeoTypes.AgentFact) {
	t.Helper()

	facts := helper.FactsFromAPI()

	copyWant := make([]bleemeoTypes.AgentFact, 0, len(want))

	for _, fact := range want {
		if fact.ID == idAny {
			fact.ID = idObjectNotFound

			for _, existingFact := range facts {
				if fact.AgentID == existingFact.AgentID && fact.Key == existingFact.Key && fact.Value == existingFact.Value {
					fact.ID = existingFact.ID

					break
				}
			}
		}

		copyWant = append(copyWant, fact)
	}

	optSort := cmpopts.SortSlices(func(x bleemeoTypes.AgentFact, y bleemeoTypes.AgentFact) bool { return x.ID < y.ID })
	if diff := cmp.Diff(copyWant, facts, cmpopts.EquateEmpty(), optSort); diff != "" {
		t.Errorf("facts mismatch (-want +got)\n%s", diff)
	}
}

type runOnceResult struct {
	err        error
	runCount   int
	syncMethod map[string]bool
}

func (r runOnceResult) Check() error {
	return r.err
}

func (r runOnceResult) CheckMethodWithFull(method string) error {
	if r.err != nil {
		return r.err
	}

	got, present := r.syncMethod[method]
	if !present {
		return fmt.Errorf("%w: method %s", errMethodNotCalled, method)
	}

	if !got {
		return fmt.Errorf("%w: method %s", errMethodCalledWithoutFull, method)
	}

	return nil
}

func (r runOnceResult) CheckMethodWithoutFull(method string) error {
	if r.err != nil {
		return r.err
	}

	got, present := r.syncMethod[method]
	if !present {
		return fmt.Errorf("%w: method %s", errMethodNotCalled, method)
	}

	if got {
		return fmt.Errorf("%w: method %s", errMethodCalledWithFull, method)
	}

	return nil
}

func (r runOnceResult) CheckMethodNotRun(method string) error {
	if r.err != nil {
		return r.err
	}

	_, present := r.syncMethod[method]
	if present {
		return fmt.Errorf("%w: method %s", errMethodCalled, method)
	}

	return nil
}

func mergeResult(r1 runOnceResult, r2 runOnceResult) runOnceResult {
	r1.runCount += r2.runCount
	if r1.err == nil {
		r1.err = r2.err
	}

	syncMethod := make(map[string]bool)

	for k, v := range r1.syncMethod {
		syncMethod[k] = syncMethod[k] || v
	}

	for k, v := range r2.syncMethod {
		syncMethod[k] = syncMethod[k] || v
	}

	r1.syncMethod = syncMethod

	return r1
}
