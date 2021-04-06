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

// nolint: scopelint,goconst
package synchronizer

import (
	"context"
	"errors"
	"glouton/agent/state"
	"glouton/bleemeo/internal/cache"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/config"
	"glouton/discovery"
	"glouton/facts"
	"glouton/store"
	"glouton/types"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"
)

type mockMetric struct {
	Name string
}

func (m mockMetric) Labels() map[string]string {
	return map[string]string{types.LabelName: m.Name}
}
func (m mockMetric) Annotations() types.MetricAnnotations {
	return types.MetricAnnotations{}
}
func (m mockMetric) Points(start, end time.Time) ([]types.Point, error) {
	return nil, errors.New("not implemented")
}

type mockTime struct {
	now time.Time
}

func (mt *mockTime) Now() time.Time {
	return mt.now
}

func TestPrioritizeAndFilterMetrics(t *testing.T) {
	inputNames := []struct {
		Name         string
		HighPriority bool
	}{
		{"cpu_used", true},
		{"cassandra_status", false},
		{"io_utilization", true},
		{"nginx_requests", false},
		{"mem_used", true},
		{"mem_used_perc", true},
	}
	isHighPriority := make(map[string]bool)
	countHighPriority := 0
	metrics := make([]types.Metric, len(inputNames))
	metrics2 := make([]types.Metric, len(inputNames))

	for i, n := range inputNames {
		metrics[i] = mockMetric{Name: n.Name}
		metrics2[i] = mockMetric{Name: n.Name}

		if n.HighPriority {
			countHighPriority++

			isHighPriority[n.Name] = true
		}
	}

	metrics = prioritizeAndFilterMetrics(metrics, false)
	metrics2 = prioritizeAndFilterMetrics(metrics2, true)

	for i, m := range metrics {
		if !isHighPriority[m.Labels()[types.LabelName]] && i < countHighPriority {
			t.Errorf("Found metrics %#v at index %d, want after %d", m.Labels()[types.LabelName], i, countHighPriority)
		}

		if isHighPriority[m.Labels()[types.LabelName]] && i >= countHighPriority {
			t.Errorf("Found metrics %#v at index %d, want before %d", m.Labels()[types.LabelName], i, countHighPriority)
		}
	}

	for i, m := range metrics2 {
		if !isHighPriority[m.Labels()[types.LabelName]] {
			t.Errorf("Found metrics %#v at index %d, but it's not prioritary", m.Labels()[types.LabelName], i)
		}
	}
}

type metricTestHelper struct {
	api        *mockAPI
	s          *Synchronizer
	mt         *mockTime
	store      *store.Store
	httpServer *httptest.Server
	t          *testing.T
}

func newMetricHelper(t *testing.T) *metricTestHelper {
	helper := &metricTestHelper{
		t:     t,
		api:   newAPI(),
		mt:    &mockTime{now: time.Now()},
		store: store.New(),
	}
	cfg := &config.Configuration{}
	helper.httpServer = helper.api.Server()

	if err := cfg.LoadByte([]byte("")); err != nil {
		panic(err)
	}

	cfg.Set("logging.level", "debug")
	cfg.Set("bleemeo.api_base", helper.httpServer.URL)
	cfg.Set("bleemeo.account_id", accountID)
	cfg.Set("bleemeo.registration_key", registrationKey)
	cfg.Set("blackbox.enabled", true)

	cache := cache.Cache{}

	state := state.NewMock()

	discovery := &discovery.MockDiscoverer{
		UpdatedAt: helper.mt.Now(),
	}

	helper.s = New(Option{
		Cache: &cache,
		GlobalOption: bleemeoTypes.GlobalOption{
			Config:       cfg,
			Facts:        facts.NewMockFacter(),
			State:        state,
			Discovery:    discovery,
			Store:        helper.store,
			MetricFormat: types.MetricFormatBleemeo,
		},
	})

	helper.s.now = helper.mt.Now
	helper.s.ctx = context.Background()
	helper.s.startedAt = helper.mt.Now()
	helper.s.nextFullSync = helper.mt.Now()

	err := helper.s.setClient()
	if err != nil {
		panic(err)
	}

	return helper
}

type runResult struct {
	h             *metricTestHelper
	runCount      int
	lastErr       error
	didFull       bool
	stillWantSync bool
}

func (h *metricTestHelper) Close() {
	h.httpServer.Close()
	h.httpServer = nil
}

func (h *metricTestHelper) Metrics() []metricPayload {
	var metrics []metricPayload

	h.api.resources["metric"].Store(&metrics)
	sort.Slice(metrics, func(i, j int) bool {
		return metrics[i].ID < metrics[j].ID
	})

	return metrics
}

func (h *metricTestHelper) AddTime(d time.Duration) {
	h.mt.now = h.mt.now.Add(d)
}

func (h *metricTestHelper) RunSync(maxLoop int, timeStep time.Duration, forceFirst bool) runResult {
	startAt := h.mt.Now()
	result := runResult{
		h: h,
	}

	h.api.ResetCount()

	for result.runCount = 0; result.runCount < maxLoop; result.runCount++ {
		methods := h.s.syncToPerform()
		if full, ok := methods["metrics"]; !ok && !forceFirst {
			break
		} else if full {
			result.didFull = true
		}

		err := h.s.syncMetrics(methods["metrics"], false)
		result.lastErr = err

		if err == nil {
			// when no error, Synchronizer update it lastSync/nextFullSync
			h.s.lastSync = startAt
			h.s.nextFullSync = h.mt.Now().Add(time.Hour)
		}

		h.mt.now = h.mt.Now().Add(timeStep)
	}

	_, result.stillWantSync = h.s.syncToPerform()["metrics"]

	return result
}

// Check verify that runSync was successful without any server error.
func (res runResult) Check(name string, wantFull bool) {
	res.CheckAllowError(name, wantFull)

	if res.h.api.ServerErrorCount > 0 {
		res.h.t.Errorf("%s: had %d server error, last is %v", name, res.h.api.ServerErrorCount, res.h.api.LastServerError)
	}
}

// CheckNoError verify that runSync was successful without any error (client or server).
func (res runResult) CheckNoError(name string, wantFull bool) {
	res.Check(name, wantFull)

	if res.h.api.ClientErrorCount > 0 {
		res.h.t.Errorf("%s: had %d client error", name, res.h.api.ClientErrorCount)
	}
}

// CheckAllowError verify that runSync was successful possibly with error but last sync must be successful.
func (res runResult) CheckAllowError(name string, wantFull bool) {
	if res.lastErr != nil {
		res.h.t.Errorf("%s: sync failed after %d run: %v", name, res.runCount, res.lastErr)
	}

	if wantFull != res.didFull {
		res.h.t.Errorf("%s: full = %v, want %v", name, res.didFull, wantFull)
	}

	if res.runCount == 0 {
		res.h.t.Errorf("%s: did 0 run", name)
	}

	if res.stillWantSync {
		res.h.t.Errorf("%s: still want to synchronize metrics", name)
	}
}

// TestMetricSimpleSync test "normal" scenario:
// Agent start and register metrics
// Some metrics disapear => mark inative
// Some re-appear and some new => mark active & register.
func TestMetricSimpleSync(t *testing.T) {
	helper := newMetricHelper(t)
	defer helper.Close()

	helper.AddTime(time.Minute)

	helper.RunSync(1, 0, false).CheckNoError("initial full", true)

	// We do 3 request: JWT auth, list metrics and register agent_status
	if helper.api.RequestCount != 3 {
		t.Errorf("Did %d requests, want 3", helper.api.RequestCount)
		helper.api.ShowRequest(t, 10)
	}

	metrics := helper.Metrics()
	want := []metricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:         "1",
				LabelsText: "",
			},
			Name: agentStatusName,
		},
	}

	if !reflect.DeepEqual(metrics, want) {
		t.Errorf("metrics = %v, want %v", metrics, want)
	}

	helper.AddTime(30 * time.Minute)
	helper.store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "cpu_system",
			},
		},
	})

	helper.RunSync(1, 0, false).CheckNoError("one new metric", false)

	// We do 2 request: list metrics, list inactive metrics
	// and register new metric
	if helper.api.RequestCount != 3 {
		t.Errorf("Did %d requests, want 3", helper.api.RequestCount)
		helper.api.ShowRequest(t, 10)
	}

	metrics = helper.Metrics()
	want = []metricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:         "1",
				LabelsText: "",
			},
			Name: agentStatusName,
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:         "2",
				LabelsText: "",
			},
			Name: "cpu_system",
		},
	}

	if !reflect.DeepEqual(metrics, want) {
		t.Errorf("metrics = %v, want %v", metrics, want)
	}

	helper.AddTime(30 * time.Minute)

	// Register 1000 metrics
	for n := 0; n < 1000; n++ {
		helper.store.PushPoints([]types.MetricPoint{
			{
				Point: types.Point{Time: helper.mt.Now()},
				Labels: map[string]string{
					types.LabelName: "metric",
					"item":          strconv.FormatInt(int64(n), 10),
				},
				Annotations: types.MetricAnnotations{
					BleemeoItem: strconv.FormatInt(int64(n), 10),
				},
			},
		})
	}

	helper.RunSync(1, 0, false).CheckNoError("add 1000 metrics", false)

	// We do 1003 request: 3 for listing and 1000 registration
	if helper.api.RequestCount != 1003 {
		t.Errorf("Did %d requests, want 1003", helper.api.RequestCount)
		helper.api.ShowRequest(t, 10)
	}

	metrics = helper.Metrics()
	if len(metrics) != 1002 {
		t.Errorf("len(metrics) = %v, want %v", len(metrics), 1002)
	}

	helper.AddTime(30 * time.Minute)
	helper.store.DropAllMetrics()
	helper.RunSync(1, 0, false).CheckNoError("all metrics inactive", false)

	// We do 1001 request: 1001 to mark inactive all metrics but agent_status
	if helper.api.RequestCount != 1001 {
		t.Errorf("Did %d requests, want 1001", helper.api.RequestCount)
		helper.api.ShowRequest(t, 10)
	}

	metrics = helper.Metrics()
	if len(metrics) != 1002 {
		t.Errorf("len(metrics) = %v, want %v", len(metrics), 1002)
	}

	for _, m := range metrics {
		if m.DeactivatedAt.IsZero() && m.Name != agentStatusName {
			t.Errorf("%v should be deactivated", m)
			break
		} else if !m.DeactivatedAt.IsZero() && m.Name == agentStatusName {
			t.Errorf("%v should not be deactivated", m)
		}
	}

	helper.AddTime(30 * time.Minute)

	// re-activate one metric + register one
	helper.store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "cpu_system",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "disk_used",
				"item":          "/home",
			},
			Annotations: types.MetricAnnotations{BleemeoItem: "/home"},
		},
	})

	helper.RunSync(1, 0, false).CheckNoError("re-active one + reg one", false)

	// We do 3 request: 1 to re-enable metric,
	// 1 search for metric before registration, 1 to register metric
	if helper.api.RequestCount != 3 {
		t.Errorf("Did %d requests, want 3", helper.api.RequestCount)
		helper.api.ShowRequest(t, 10)
	}

	metrics = helper.Metrics()
	if len(metrics) != 1003 {
		t.Errorf("len(metrics) = %v, want %v", len(metrics), 1002)
	}

	for _, m := range metrics {
		if m.Name == agentStatusName || m.Name == "cpu_system" || m.Name == "disk_used" {
			if !m.DeactivatedAt.IsZero() {
				t.Errorf("%v should be active", m)
			}
		} else if m.DeactivatedAt.IsZero() {
			t.Errorf("%v should be deactivated", m)
			break
		}

		if m.Name == "disk_used" {
			if m.Item != "/home" {
				t.Errorf("%v miss item=/home", m)
			}
		}
	}

	helper.AddTime(30 * time.Minute)
}

// TestMetricDeleted test that Glouton can update metrics deleted on Bleemeo.
func TestMetricDeleted(t *testing.T) {
	helper := newMetricHelper(t)
	defer helper.Close()

	helper.AddTime(time.Minute)

	helper.store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric1",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric2",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric3",
			},
		},
	})

	helper.RunSync(1, 0, false).CheckNoError("initial sync", true)

	// We do JWT auth, list of active metrics, 2 query to register metric + 1 to register agent_status
	if helper.api.RequestCount != 9 {
		t.Errorf("Did %d requests, want 9", helper.api.RequestCount)
		helper.api.ShowRequest(t, 10)
	}

	metrics := helper.Metrics()
	if len(metrics) != 4 { // 3 + agent_status
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}

	helper.AddTime(70 * time.Minute)
	helper.store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric1",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric2",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric3",
			},
		},
	})

	// API deleted metric1
	for _, m := range metrics {
		if m.Name == "metric1" {
			helper.api.resources["metric"].DelStore(m.ID)
		}
	}

	helper.RunSync(1, 0, false).CheckNoError("full after API delete", true)
	helper.AddTime(1 * time.Minute)

	// metric1 is still alive
	helper.store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric1",
			},
		},
	})

	helper.RunSync(1, 0, false).CheckNoError("re-reg after API delete", false)
	// We list active metrics, 2 query to re-register metric
	if helper.api.RequestCount != 3 {
		t.Errorf("Did %d requests, want 3", helper.api.RequestCount)
		helper.api.ShowRequest(t, 10)
	}

	metrics = helper.Metrics()
	if len(metrics) != 4 { // 3 + agent_status
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}

	helper.s.nextFullSync = helper.mt.Now().Add(2 * time.Hour)
	helper.AddTime(90 * time.Minute)

	// API deleted metric2
	for _, m := range metrics {
		if m.Name == "metric2" {
			helper.api.resources["metric"].DelStore(m.ID)
		}
	}

	// all metrics are inactive
	helper.RunSync(1, 0, true).Check("mark inactive", false)

	metrics = helper.Metrics()
	if len(metrics) != 3 {
		t.Errorf("len(metrics) = %d, want 3", len(metrics))
	}

	for _, m := range metrics {
		if m.DeactivatedAt.IsZero() && m.Name != agentStatusName {
			t.Errorf("%v should be deactivated", m)
			break
		} else if !m.DeactivatedAt.IsZero() && m.Name == agentStatusName {
			t.Errorf("%v should not be deactivated", m)
		}
	}

	helper.AddTime(1 * time.Minute)

	// API deleted metric3
	for _, m := range metrics {
		if m.Name == "metric3" {
			helper.api.resources["metric"].DelStore(m.ID)
		}
	}

	helper.store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric1",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric2",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric3",
			},
		},
	})

	helper.RunSync(1, 0, true).Check("re-active metrics", false)

	metrics = helper.Metrics()
	if len(metrics) != 4 {
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}

	for _, m := range metrics {
		if !m.DeactivatedAt.IsZero() {
			t.Errorf("%v should not be deactivated", m)
		}
	}
}

// TestMetricError test that Glouton handle random error from Bleemeo correctly.
func TestMetricError(t *testing.T) {
	helper := newMetricHelper(t)
	defer helper.Close()

	// API fail 1/6th of the time
	helper.api.PreRequestHook = func(ma *mockAPI, h *http.Request) (interface{}, int, error) {
		if len(ma.RequestList)%6 == 0 {
			return nil, http.StatusInternalServerError, nil
		}

		return nil, 0, nil
	}
	helper.AddTime(time.Minute)

	helper.store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric1",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric2",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric3",
			},
		},
	})

	helper.RunSync(10, 20*time.Second, false).CheckAllowError("initial sync", true)

	if helper.api.ServerErrorCount == 0 {
		t.Errorf("We should have some error, had %d", helper.api.ServerErrorCount)
	}

	metrics := helper.Metrics()
	if len(metrics) != 4 { // 3 + agent_status
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}
}

// TestMetricUnknownError test that Glouton handle failing metric from Bleemeo correctly.
func TestMetricUnknownError(t *testing.T) {
	helper := newMetricHelper(t)
	defer helper.Close()

	// API always reject registering "deny-me" metric
	helper.api.resources["metric"].(*genericResource).CreateHook = func(r *http.Request, body []byte, valuePtr interface{}) error {
		metric := valuePtr.(*metricPayload)
		if metric.Name == "deny-me" {
			return errClient{
				body:       "no information about whether the error is permanent or not",
				statusCode: http.StatusBadRequest,
			}
		}

		return nil
	}

	helper.AddTime(time.Minute)

	helper.store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric1",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "deny-me",
			},
		},
	})

	helper.RunSync(1, 0, false).Check("initial sync", true)

	if helper.api.ClientErrorCount == 0 {
		t.Errorf("We should have some client error, had %d", helper.api.ClientErrorCount)
	}

	// jwt-auth, list active metrics, register agent_status + 2x metrics to register
	if helper.api.RequestCount != 7 {
		t.Errorf("Had %d requests, want 7", helper.api.RequestCount)
	}

	metrics := helper.Metrics()
	if len(metrics) != 2 { // 1 + agent_status
		t.Errorf("len(metrics) = %d, want 2", len(metrics))
	}

	// immediately re-run: should not run at all
	res := helper.RunSync(1, 0, false)
	if res.runCount != 0 {
		t.Errorf("had %d run count, want 0", res.runCount)
	}

	// After a short delay we retry
	helper.s.nextFullSync = helper.mt.Now().Add(24 * time.Hour)
	helper.AddTime(31 * time.Second)
	helper.RunSync(10, 15*time.Minute, false).Check("retry-run", false)

	// we expected 5 retry than stopping for longer delay. Each retry had 3 requests
	if helper.api.RequestCount != 15 {
		t.Errorf("Had %d requests, want 15", helper.api.RequestCount)
		helper.api.ShowRequest(t, 20)
	}

	// Finally on next fullSync re-retry the metrics registration
	helper.mt.now = helper.s.nextFullSync.Add(5 * time.Second)
	helper.store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric1",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "deny-me",
			},
		},
	})

	helper.RunSync(1, 0, false).Check("next full sync", true)

	if helper.api.ClientErrorCount == 0 {
		t.Errorf("We should have some client error, had %d", helper.api.ClientErrorCount)
	}

	// list active metrics, 2 for registration
	if helper.api.RequestCount != 3 {
		t.Errorf("Had %d requests, want 3", helper.api.RequestCount)
		helper.api.ShowRequest(t, 10)
	}

	metrics = helper.Metrics()
	if len(metrics) != 2 { // 1 + agent_status
		t.Errorf("len(metrics) = %d, want 2", len(metrics))
	}
}

// TestMetricPermanentError test that Glouton handle permanent failure metric from Bleemeo correctly.
func TestMetricPermanentError(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "allow-list",
			content: `{"label":["This metric is not whitelisted for this agent"]}`,
		},
		{
			name:    "too many metrics",
			content: `{"label":["Too many non standard metrics"]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := newMetricHelper(t)
			defer helper.Close()

			// API always reject registering "deny-me" metric
			helper.api.resources["metric"].(*genericResource).CreateHook = func(r *http.Request, body []byte, valuePtr interface{}) error {
				metric := valuePtr.(*metricPayload)
				if metric.Name == "deny-me" || metric.Name == "deny-me-also" {
					return errClient{
						body:       tt.content,
						statusCode: http.StatusBadRequest,
					}
				}

				return nil
			}

			helper.AddTime(time.Minute)

			helper.store.PushPoints([]types.MetricPoint{
				{
					Point: types.Point{Time: helper.mt.Now()},
					Labels: map[string]string{
						types.LabelName: "metric1",
					},
				},
				{
					Point: types.Point{Time: helper.mt.Now()},
					Labels: map[string]string{
						types.LabelName: "deny-me",
					},
				},
				{
					Point: types.Point{Time: helper.mt.Now()},
					Labels: map[string]string{
						types.LabelName: "deny-me-also",
					},
				},
			})

			helper.RunSync(1, 0, false).Check("initial sync", true)

			if helper.api.ClientErrorCount == 0 {
				t.Errorf("We should have some client error, had %d", helper.api.ClientErrorCount)
			}

			// jwt-auth, list active metrics, register agent_status + 2x metrics to register
			if helper.api.RequestCount != 9 {
				t.Errorf("Had %d requests, want 9", helper.api.RequestCount)
				helper.api.ShowRequest(t, 10)
			}

			metrics := helper.Metrics()
			if len(metrics) != 2 { // 1 + agent_status
				t.Errorf("len(metrics) = %d, want 2", len(metrics))
			}

			// immediately re-run: should not run at all
			res := helper.RunSync(1, 0, false)
			if res.runCount != 0 {
				t.Errorf("had %d run count, want 0", res.runCount)
			}

			// After a short delay we do not retry because error is permanent
			helper.AddTime(31 * time.Second)
			res = helper.RunSync(10, 15*time.Minute, false)
			if res.runCount != 0 {
				t.Errorf("had %d run count, want 0", res.runCount)
			}

			// After a long delay we do not retry because error is permanent
			helper.AddTime(50 * time.Minute)
			helper.store.PushPoints([]types.MetricPoint{
				{
					Point: types.Point{Time: helper.mt.Now()},
					Labels: map[string]string{
						types.LabelName: "metric1",
					},
				},
				{
					Point: types.Point{Time: helper.mt.Now()},
					Labels: map[string]string{
						types.LabelName: "deny-me",
					},
				},
				{
					Point: types.Point{Time: helper.mt.Now()},
					Labels: map[string]string{
						types.LabelName: "deny-me-also",
					},
				},
			})

			res = helper.RunSync(10, 15*time.Minute, false)
			if res.runCount != 0 {
				t.Errorf("had %d run count, want 0", res.runCount)
			}

			// Finally long enough to reach fullSync, we will retry ONE
			helper.AddTime(20 * time.Minute)
			helper.RunSync(1, 0, false).Check("second full sync", true)

			if helper.api.ClientErrorCount == 0 {
				t.Errorf("We should have some client error, had %d", helper.api.ClientErrorCount)
			}

			// list active metrics, retry ONE register (but for now query for existence of the 2 metrics)
			if helper.api.RequestCount != 4 {
				t.Errorf("Had %d requests, want 4", helper.api.RequestCount)
				helper.api.ShowRequest(t, 10)
			}

			metrics = helper.Metrics()
			if len(metrics) != 2 { // 1 + agent_status
				t.Errorf("len(metrics) = %d, want 2", len(metrics))
			}

			// Now metric registration will succeeds and retry all
			helper.api.resources["metric"].(*genericResource).CreateHook = nil
			helper.AddTime(70 * time.Minute)
			helper.store.PushPoints([]types.MetricPoint{
				{
					Point: types.Point{Time: helper.mt.Now()},
					Labels: map[string]string{
						types.LabelName: "metric1",
					},
				},
				{
					Point: types.Point{Time: helper.mt.Now()},
					Labels: map[string]string{
						types.LabelName: "deny-me",
					},
				},
				{
					Point: types.Point{Time: helper.mt.Now()},
					Labels: map[string]string{
						types.LabelName: "deny-me-also",
					},
				},
			})

			helper.RunSync(1, 0, false).Check("3rd full sync", true)

			if helper.api.ClientErrorCount != 0 {
				t.Errorf("had %d client error, want 0", helper.api.ClientErrorCount)
			}

			// list active metrics, retry two register
			if helper.api.RequestCount != 5 {
				t.Errorf("Had %d requests, want 5", helper.api.RequestCount)
				helper.api.ShowRequest(t, 10)
			}

			metrics = helper.Metrics()
			if len(metrics) != 4 { // 3 + agent_status
				t.Errorf("len(metrics) = %d, want 4", len(metrics))
			}
		})
	}
}

// TestMetricTooMany test that Glouton handle too many non-standard metric correctly.
func TestMetricTooMany(t *testing.T) { // nolint: gocyclo
	helper := newMetricHelper(t)
	defer helper.Close()

	defaultPatchHook := helper.api.resources["metric"].(*genericResource).PatchHook

	// API always reject more than 3 active metrics
	helper.api.resources["metric"].(*genericResource).CreateHook = func(r *http.Request, body []byte, valuePtr interface{}) error {
		if defaultPatchHook != nil {
			err := defaultPatchHook(r, body, valuePtr)
			if err != nil {
				return err
			}
		}

		metric := valuePtr.(*metricPayload)

		if metric.DeactivatedAt.IsZero() {
			metrics := helper.Metrics()
			countActive := 0

			for _, m := range metrics {
				if m.DeactivatedAt.IsZero() && m.ID != metric.ID {
					countActive++
				}
			}

			if countActive >= 3 {
				return errClient{
					body:       `{"label":["Too many non standard metrics"]}`,
					statusCode: http.StatusBadRequest,
				}
			}
		}

		return nil
	}
	helper.api.resources["metric"].(*genericResource).PatchHook = helper.api.resources["metric"].(*genericResource).CreateHook

	helper.AddTime(time.Minute)

	helper.store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric1",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric2",
			},
		},
	})

	helper.RunSync(1, 0, false).CheckNoError("initial sync", true)

	// jwt-auth, list active metrics, register agent_status + 2x metrics to register
	if helper.api.RequestCount != 7 {
		t.Errorf("Had %d requests, want 7", helper.api.RequestCount)
	}

	metrics := helper.Metrics()
	if len(metrics) != 3 { // 2 + agent_status
		t.Errorf("len(metrics) = %d, want 3", len(metrics))
	}

	helper.AddTime(5 * time.Minute)
	helper.store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric3",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric4",
			},
		},
	})

	helper.RunSync(1, 0, false).Check("try-two-new", false)

	if helper.api.ClientErrorCount == 0 {
		t.Errorf("We should have some client error, had %d", helper.api.ClientErrorCount)
	}

	// list active metrics + 2x metrics to register
	if helper.api.RequestCount != 5 {
		t.Errorf("Had %d requests, want 5", helper.api.RequestCount)
		helper.api.ShowRequest(t, 10)
	}

	metrics = helper.Metrics()
	if len(metrics) != 3 { // 2 + agent_status
		t.Errorf("len(metrics) = %d, want 3", len(metrics))
	}

	helper.AddTime(5 * time.Minute)

	res := helper.RunSync(1, 0, false)
	if res.runCount != 0 {
		t.Errorf("had %d run count, want 0", res.runCount)
	}

	helper.AddTime(70 * time.Minute)
	// drop all because normally store drop inactive metrics and
	// metric1 don't emitted for 70 minutes
	helper.store.DropAllMetrics()
	helper.store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric2",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric3",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric4",
			},
		},
	})

	helper.RunSync(2, 1*time.Second, false).Check("next full-sync", true)

	metrics = helper.Metrics()
	if len(metrics) != 4 { // metric1 is now disabled, another get registered
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}

	for _, m := range metrics {
		if !m.DeactivatedAt.IsZero() && m.Name != "metric1" {
			t.Errorf("metric %s is deactivated, want active", m.Name)
		}

		if m.DeactivatedAt.IsZero() && m.Name == "metric1" {
			t.Errorf("metric %s is active, want deactivated", m.Name)
		}
	}

	helper.AddTime(5 * time.Minute)
	helper.store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric1",
			},
		},
	})
	helper.RunSync(1, 1*time.Second, false).Check("re-enable metric1", false)

	if helper.api.ClientErrorCount == 0 {
		t.Errorf("We should have some client error, had %d", helper.api.ClientErrorCount)
	}

	metrics = helper.Metrics()
	if len(metrics) != 4 { // metric1 is now disabled, another get registered
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}

	for _, m := range metrics {
		if !m.DeactivatedAt.IsZero() && m.Name != "metric1" {
			t.Errorf("metric %s is deactivated, want active", m.Name)
		}

		if m.DeactivatedAt.IsZero() && m.Name == "metric1" {
			t.Errorf("metric %s is active, want deactivated", m.Name)
		}
	}

	// We do not retry to register them
	helper.AddTime(5 * time.Minute)

	res = helper.RunSync(1, 0, false)
	if res.runCount != 0 {
		t.Errorf("had %d run count, want 0", res.runCount)
	}

	// Excepted ONE per full-sync
	helper.AddTime(70 * time.Minute)
	helper.store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric2",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric3",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "metric4",
			},
		},
	})

	helper.RunSync(1, 1*time.Second, false).Check("3rd full-sync", true)

	if helper.api.ClientErrorCount != 1 {
		t.Errorf("had %d client error, want 1", helper.api.ClientErrorCount)
	}

	metrics = helper.Metrics()
	if len(metrics) != 4 { // metric1 is now disabled, another get registered
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}

	for _, m := range metrics {
		if !m.DeactivatedAt.IsZero() && m.Name != "metric1" {
			t.Errorf("metric %s is deactivated, want active", m.Name)
		}

		if m.DeactivatedAt.IsZero() && m.Name == "metric1" {
			t.Errorf("metric %s is active, want deactivated", m.Name)
		}
	}

	// list active metrics + check existence of the metric we want to reg +
	// retry to register
	if helper.api.RequestCount != 3 {
		t.Errorf("Had %d requests, want 3", helper.api.RequestCount)
		helper.api.ShowRequest(t, 10)
	}
}

// TestMetricLongItem test that metric with very long item works.
// Long item happen with long container name, test this scenario.
func TestMetricLongItem(t *testing.T) {
	helper := newMetricHelper(t)
	defer helper.Close()

	helper.AddTime(time.Minute)
	// This test is perfect as it don't set service, but helper does not yet support
	// synchronization with service.
	helper.store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "redis_status",
				types.LabelItem: "short-redis-container-name",
			},
			Annotations: types.MetricAnnotations{
				BleemeoItem: "short-redis-container-name",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "redis_status",
				types.LabelItem: "long-redis-container-name--this-one-is-more-than-100-char-which-is-the-limit-on-bleemeo-api-0123456789abcdef",
			},
			Annotations: types.MetricAnnotations{
				BleemeoItem: "long-redis-container-name--this-one-is-more-than-100-char-which-is-the-limit-on-bleemeo-api-0123456789abcdef",
			},
		},
	})

	helper.RunSync(1, 0, false).CheckNoError("first sync", true)

	metrics := helper.Metrics()
	// agent_status + the two redis_status metrics
	if len(metrics) != 3 {
		t.Errorf("len(metrics) = %v, want %v", len(metrics), 3)
	}

	helper.AddTime(70 * time.Minute)

	helper.store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "redis_status",
				types.LabelItem: "short-redis-container-name",
			},
			Annotations: types.MetricAnnotations{
				BleemeoItem: "short-redis-container-name",
			},
		},
		{
			Point: types.Point{Time: helper.mt.Now()},
			Labels: map[string]string{
				types.LabelName: "redis_status",
				types.LabelItem: "long-redis-container-name--this-one-is-more-than-100-char-which-is-the-limit-on-bleemeo-api-0123456789abcdef",
			},
			Annotations: types.MetricAnnotations{
				BleemeoItem: "long-redis-container-name--this-one-is-more-than-100-char-which-is-the-limit-on-bleemeo-api-0123456789abcdef",
			},
		},
	})

	helper.RunSync(1, 0, false).CheckNoError("new full sync", true)

	// We do 1 request: list metrics.
	if helper.api.RequestCount != 1 {
		t.Errorf("Did %d requests, want 1", helper.api.RequestCount)
		helper.api.ShowRequest(t, 10)
	}

	metrics = helper.Metrics()
	// No new metrics
	if len(metrics) != 3 {
		t.Errorf("len(metrics) = %v, want %v", len(metrics), 3)
	}
}

// inactive and MQTT

func Test_httpResponseToMetricFailureKind(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bleemeoTypes.FailureKind
	}{
		{
			name:    "random content",
			content: "any random content",
			want:    bleemeoTypes.FailureUnknown,
		},
		{
			name:    "not whitelisted",
			content: `{"label":["This metric is not whitelisted for this agent"]}`,
			want:    bleemeoTypes.FailureAllowList,
		},
		{
			name:    "not allowed",
			content: `{"label":["This metric is not in allow-list for this agent"]}`,
			want:    bleemeoTypes.FailureAllowList,
		},
		{
			name:    "too many metrics",
			content: `{"label":["Too many non standard metrics"]}`,
			want:    bleemeoTypes.FailureTooManyMetric,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := httpResponseToMetricFailureKind(tt.content); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("httpResponseToMetricFailureKind() = %v, want %v", got, tt.want)
			}
		})
	}
}
