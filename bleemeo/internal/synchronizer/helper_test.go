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

//nolint:dupl
package synchronizer

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/prometheus/exporter/snmp"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/store"
	gloutonTypes "github.com/bleemeo/glouton/types"

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
	now               *mockTime
	wrapperClientMock *wrapperClientMock
	s                 *Synchronizer
	cfg               config.Config
	facts             *facts.FactProviderMock
	containers        []facts.Container
	cache             *cache.Cache
	state             *stateMock
	discovery         *discovery.MockDiscoverer
	store             *store.Store
	httpServer        *httptest.Server
	devices           []bleemeoTypes.VSphereDevice

	// Following fields are options used by some method
	SNMP               []*snmp.Target
	NotifyLabelsUpdate func()
}

// newHelper create an helper with all permanent resource: API, cache, state.
// It does not create the Synchronizer (use initSynchronizer).
func newHelper(t *testing.T) *syncTestHelper {
	t.Helper()

	now := &mockTime{now: time.Now()}

	helper := &syncTestHelper{
		now:   now,
		facts: facts.NewMockFacter(nil),
		cache: &cache.Cache{},
		state: newStateMock(),
		discovery: &discovery.MockDiscoverer{
			UpdatedAt: now.Now(),
		},
		cfg: config.Config{
			Logging: config.Logging{
				Level: "debug",
			},
			Bleemeo: config.Bleemeo{
				APIBase:   "we don't care for tests",
				AccountID: accountID,
				Cache: config.BleemeoCache{
					DeactivatedMetricsExpirationDays: 200,
				},
				RegistrationKey: registrationKey,
			},
			Blackbox: config.Blackbox{
				Enable:      true,
				ScraperName: "paris",
			},
		},
	}

	helper.wrapperClientMock = newClientMock(helper)
	helper.facts.SetFact("fqdn", testAgentFQDN)

	return helper
}

// preregisterAgent set resource in API, Cache & State as if the agent was previously
// registered.
func (helper *syncTestHelper) preregisterAgent(t *testing.T) {
	t.Helper()

	const password = "the initial password"

	_ = helper.state.SetBleemeoCredentials(testAgent.ID, password)

	helper.wrapperClientMock.password = password
	helper.wrapperClientMock.username = testAgent.ID + "@bleemeo.com"

	helper.wrapperClientMock.resources.agents.elems = []bleemeoapi.AgentPayload{testAgent}
}

// addMonitorOnAPI pre-create a monitor in the API.
func (helper *syncTestHelper) addMonitorOnAPI(t *testing.T) bleemeoapi.ServicePayload {
	t.Helper()

	newMonitorCopy := newMonitor
	newMonitorCopy.AccountConfig = helper.wrapperClientMock.accountConfigNewAgent

	helper.wrapperClientMock.resources.monitors.add(newMonitorCopy.Monitor)

	return newMonitorCopy
}

// Create or re-create the Synchronizer. It also resets the store.
func (helper *syncTestHelper) initSynchronizer(t *testing.T) {
	t.Helper()

	helper.store = store.New("test store", time.Hour, 2*time.Hour)

	helper.store.InternalSetNowAndRunOnce(helper.Now)

	var docker bleemeoTypes.DockerProvider
	if helper.containers != nil {
		docker = &mockDocker{helper: helper}
	}

	s := newForTest(types.Option{
		Cache:           helper.cache,
		IsMqttConnected: func() bool { return false },
		ProvideClient:   func() types.Client { return helper.wrapperClientMock },
		GlobalOption: bleemeoTypes.GlobalOption{
			Config:                     helper.cfg,
			Facts:                      helper.facts,
			State:                      helper.state,
			Docker:                     docker,
			Discovery:                  helper.discovery,
			Store:                      helper.store,
			MonitorManager:             mockMonitorManager{},
			NotifyFirstRegistration:    func() {},
			Process:                    mockProcessLister{},
			SNMP:                       helper.SNMP,
			SNMPOnlineTarget:           func() int { return len(helper.SNMP) },
			NotifyHooksUpdate:          helper.NotifyLabelsUpdate,
			VSphereDevices:             func(context.Context, time.Duration) []bleemeoTypes.VSphereDevice { return helper.devices },
			LastVSphereChange:          func(_ context.Context) time.Time { return time.Time{} },
			VSphereEndpointsInError:    func() map[string]bool { return map[string]bool{} },
			IsContainerEnabled:         facts.ContainerFilter{}.ContainerEnabled,
			IsMetricAllowed:            func(_ map[string]string) bool { return true },
			BlackboxScraperName:        helper.cfg.Blackbox.ScraperName,
			LastMetricAnnotationChange: func() time.Time { return time.Time{} },
		},
	}, helper.Now)

	helper.s = s

	// Do actions done by s.Run()
	s.startedAt = helper.Now()

	if err := s.setClient(); err != nil {
		t.Fatalf("setClient failed: %v", err)
	}

	// Some part of synchronizer don't like having *exact* same time for Now() & startedAt
	helper.AddTime(time.Microsecond)
}

// pushPoints write points to the store with current time.
// It known some meta labels (see code for supported one).
func (helper *syncTestHelper) pushPoints(t *testing.T, metrics []labels.Labels) {
	t.Helper()

	points := make([]gloutonTypes.MetricPoint, 0, len(metrics))

	for _, m := range metrics {
		mCopy := m.Copy()
		annotations := model.MetaLabelsToAnnotation(mCopy)
		mCopy = model.DropMetaLabels(mCopy)

		points = append(points, gloutonTypes.MetricPoint{
			Point: gloutonTypes.Point{
				Time:  helper.Now(),
				Value: 42.0,
			},
			Labels:      mCopy.Map(),
			Annotations: annotations,
		})
	}

	if helper.store == nil {
		t.Fatal("pushPoints called before store is initialized")
	}

	helper.store.PushPoints(t.Context(), points)
}

func (helper *syncTestHelper) Close() {
	if helper.httpServer != nil {
		helper.httpServer.Close()

		helper.httpServer = nil
	}
}

func (helper *syncTestHelper) AddTime(d time.Duration) {
	helper.now.Advance(d)
}

func (helper *syncTestHelper) SetTime(now time.Time) {
	helper.now.now = now
}

func (helper *syncTestHelper) SetTimeToNextFullSync() {
	helper.now.now = helper.s.nextFullSync.Add(time.Second)
}

func (helper *syncTestHelper) Now() time.Time {
	return helper.now.Now()
}

func (helper *syncTestHelper) runOnce(t *testing.T) error {
	t.Helper()

	result := helper.runOnceWithResult(t)

	if result.err != nil {
		return fmt.Errorf("runOnce failed: %w", result.err)
	}

	if helper.wrapperClientMock.errorsCount > 0 {
		return fmt.Errorf("%w: %d API error(s)", errClientError, helper.wrapperClientMock.errorsCount)
	}

	return nil
}

func (helper *syncTestHelper) runOnceWithResult(t *testing.T) runOnceResult {
	t.Helper()

	helper.wrapperClientMock.resetCount()

	return helper.runOnceNoReset(t)
}

func (helper *syncTestHelper) runOnceNoReset(t *testing.T) runOnceResult {
	t.Helper()

	ctx := t.Context()

	result := runOnceResult{}

	if helper.s == nil {
		result.err = fmt.Errorf("%w: runOnce called before initSynchronizer", errUnexpectedOperation)

		return result
	}

	var execution *Execution

	result.runCount++
	execution, result.err = helper.s.runOnce(ctx, false)

	if execution != nil {
		result.syncPerEntity = make(map[types.EntityName]types.SyncType, len(execution.entities))

		for _, ee := range execution.entities {
			if ee.syncType == types.SyncTypeNone {
				continue
			}

			result.syncPerEntity[ee.entity.Name()] = ee.syncType
		}
	}

	return result
}

// runUntilNoError run runOnceWithResult until it no longer returns error (or maxRun is reached).
// Each additional run, clock advance of timeStep.
// runOnceResult contains merges from all runs.
func (helper *syncTestHelper) runUntilNoError(t *testing.T, maxRun int, timeStep time.Duration) runOnceResult {
	t.Helper()

	helper.wrapperClientMock.resetCount()

	result := runOnceResult{}

	for range maxRun {
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
func (helper *syncTestHelper) SetAPIMetrics(metrics ...bleemeoapi.MetricPayload) {
	helper.wrapperClientMock.resources.metrics.elems = metrics
}

// SetAPIServices define the list of service present on Bleemeo API mock.
func (helper *syncTestHelper) SetAPIServices(services ...bleemeoapi.ServicePayload) {
	helper.wrapperClientMock.resources.services.elems = services
}

// SetAPIAccountConfig define the list of AccountConfig and AgentConfig present on Bleemeo API mock.
// It also enable using the AccountConfig as default config for new Agent.
func (helper *syncTestHelper) SetAPIAccountConfig(accountConfig bleemeoTypes.AccountConfig, agentConfigs []bleemeoTypes.AgentConfig) {
	helper.wrapperClientMock.accountConfigNewAgent = accountConfig.ID
	helper.wrapperClientMock.resources.accountConfigs.elems = []bleemeoTypes.AccountConfig{accountConfig}
	helper.wrapperClientMock.resources.agentConfigs.elems = agentConfigs
}

// SetCacheMetrics define the list of metric present in Glouton cache.
func (helper *syncTestHelper) SetCacheMetrics(metrics ...bleemeoapi.MetricPayload) {
	tmp := make([]bleemeoTypes.Metric, 0, len(metrics))

	for _, m := range metrics {
		tmp = append(tmp, metricFromAPI(m, helper.Now()))
	}

	helper.s.option.Cache.SetMetrics(tmp)
}

// MetricsFromAPI returns metrics present on Bleemeo API mock.
func (helper *syncTestHelper) MetricsFromAPI() []bleemeoapi.MetricPayload {
	metrics := helper.wrapperClientMock.resources.metrics.clone()
	sort.Slice(metrics, func(i, j int) bool {
		return metrics[i].ID < metrics[j].ID
	})

	return metrics
}

// AgentsFromAPI returns agents present on Bleemeo API mock.
func (helper *syncTestHelper) AgentsFromAPI() []bleemeoapi.AgentPayload {
	agents := helper.wrapperClientMock.resources.agents.clone()
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].ID < agents[j].ID
	})

	return agents
}

// FactsFromAPI returns facts present on Bleemeo API mock.
func (helper *syncTestHelper) FactsFromAPI() []bleemeoTypes.AgentFact {
	facts := helper.wrapperClientMock.resources.agentFacts.clone()
	sort.Slice(facts, func(i, j int) bool {
		return facts[i].ID < facts[j].ID
	})

	return facts
}

// ServicesFromAPI returns services present on Bleemeo API mock.
func (helper *syncTestHelper) ServicesFromAPI() []bleemeoapi.ServicePayload {
	services := helper.wrapperClientMock.resources.services.clone()
	sort.Slice(services, func(i, j int) bool {
		return services[i].ID < services[j].ID
	})

	return services
}

// assertAgentsInAPI check that wanted agents (and only wanted agents) are present in API.
// This is mostly a wrapper around cmp.Diff which also do:
// * replace idAny by corresponding ID (match metric by same fqdn)
// * sort list by ID.
func (helper *syncTestHelper) assertAgentsInAPI(t *testing.T, want []bleemeoapi.AgentPayload) {
	t.Helper()

	agents := helper.AgentsFromAPI()

	copyWant := make([]bleemeoapi.AgentPayload, 0, len(want))

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

	optSort := cmpopts.SortSlices(func(x, y bleemeoapi.AgentPayload) bool { return x.ID < y.ID })
	if diff := cmp.Diff(copyWant, agents, cmpopts.EquateEmpty(), optSort); diff != "" {
		t.Errorf("agents mismatch (-want +got)\n%s", diff)
	}
}

// assertServicesInAPI check that wanted agents (and only wanted agents) are present in API.
// This is mostly a wrapper around cmp.Diff which also do:
// * replace idAny by corresponding ID (match service by same name, instance, url)
// * sort list by ID.
func (helper *syncTestHelper) assertServicesInAPI(t *testing.T, want []bleemeoapi.ServicePayload) {
	t.Helper()

	services := helper.ServicesFromAPI()

	copyWant := make([]bleemeoapi.ServicePayload, 0, len(want))

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

	optSort := cmpopts.SortSlices(func(x bleemeoapi.ServicePayload, y bleemeoapi.ServicePayload) bool { return x.ID < y.ID })
	if diff := cmp.Diff(copyWant, services, cmpopts.EquateEmpty(), optSort); diff != "" {
		t.Errorf("services mismatch (-want +got)\n%s", diff)
	}
}

// assertMetricsInAPI check that wanted metrics (and only wanted metrics) are present in API.
// This is mostly a wrapper around cmp.Diff which also do:
// * replace idAny by corresponding ID (match metric by same agentID, label, item & labels_text)
// * sort list by ID.
func (helper *syncTestHelper) assertMetricsInAPI(t *testing.T, want []bleemeoapi.MetricPayload) {
	t.Helper()

	metrics := helper.MetricsFromAPI()

	copyWant := make([]bleemeoapi.MetricPayload, 0, len(want))

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

	optSort := cmpopts.SortSlices(func(x, y bleemeoapi.MetricPayload) bool { return x.ID < y.ID })
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
	err           error
	runCount      int
	syncPerEntity map[types.EntityName]types.SyncType
}

func (r runOnceResult) Check() error {
	return r.err
}

func (r runOnceResult) CheckMethodWithFull(method types.EntityName) error {
	if r.err != nil {
		return r.err
	}

	got, present := r.syncPerEntity[method]
	if !present {
		return fmt.Errorf("%w: method %s", errMethodNotCalled, method)
	}

	if got != types.SyncTypeForceCacheRefresh {
		return fmt.Errorf("%w: method %s", errMethodCalledWithoutFull, method)
	}

	return nil
}

func (r runOnceResult) CheckMethodWithoutFull(method types.EntityName) error {
	if r.err != nil {
		return r.err
	}

	got, present := r.syncPerEntity[method]
	if !present {
		return fmt.Errorf("%w: method %s", errMethodNotCalled, method)
	}

	if got != types.SyncTypeNormal {
		return fmt.Errorf("%w: method %s", errMethodCalledWithFull, method)
	}

	return nil
}

func (r runOnceResult) CheckMethodNotRun(method types.EntityName) error {
	if r.err != nil {
		return r.err
	}

	got, present := r.syncPerEntity[method]
	if present {
		return fmt.Errorf("%w: method %s", errMethodCalled, method)
	}

	if got != types.SyncTypeNone {
		return fmt.Errorf("%w: method %s", errMethodCalled, method)
	}

	return nil
}

func mergeResult(r1 runOnceResult, r2 runOnceResult) runOnceResult {
	r1.runCount += r2.runCount
	if r1.err == nil {
		r1.err = r2.err
	}

	syncMethod := make(map[types.EntityName]types.SyncType)

	for k, v := range r1.syncPerEntity {
		if v == types.SyncTypeForceCacheRefresh {
			syncMethod[k] = types.SyncTypeForceCacheRefresh
		} else if syncMethod[k] == types.SyncTypeNone {
			syncMethod[k] = v
		}
	}

	for k, v := range r2.syncPerEntity {
		if v == types.SyncTypeForceCacheRefresh {
			syncMethod[k] = types.SyncTypeForceCacheRefresh
		} else if syncMethod[k] == types.SyncTypeNone {
			syncMethod[k] = v
		}
	}

	r1.syncPerEntity = syncMethod

	return r1
}
