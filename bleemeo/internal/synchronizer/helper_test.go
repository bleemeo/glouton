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

	"github.com/prometheus/prometheus/model/labels"
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

	helper.api.resources["agent"].AddStore(testAgent)
}

// addMonitorOnAPI pre-create a monitor in the API.
func (helper *syncTestHelper) addMonitorOnAPI(t *testing.T) {
	t.Helper()

	helper.api.resources["service"].AddStore(newMonitor)
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

	s, err := newWithNow(Option{
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
