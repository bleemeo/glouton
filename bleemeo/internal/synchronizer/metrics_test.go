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

func setupMetricTest(nowFun func() time.Time) (*mockAPI, *store.Store, *Synchronizer) {
	api := newAPI()
	now := nowFun()
	cfg := &config.Configuration{}
	server := api.Server()

	if err := cfg.LoadByte([]byte("")); err != nil {
		panic(err)
	}

	cfg.Set("logging.level", "debug")
	cfg.Set("bleemeo.api_base", server.URL)
	cfg.Set("bleemeo.account_id", accountID)
	cfg.Set("bleemeo.registration_key", registrationKey)
	cfg.Set("blackbox.enabled", true)

	cache := cache.Cache{}

	state := state.NewMock()

	discovery := &discovery.MockDiscoverer{
		UpdatedAt: now,
	}

	store := store.New()

	s := New(Option{
		Cache: &cache,
		GlobalOption: bleemeoTypes.GlobalOption{
			Config:       cfg,
			Facts:        facts.NewMockFacter(),
			State:        state,
			Discovery:    discovery,
			Store:        store,
			MetricFormat: types.MetricFormatBleemeo,
		},
	})

	s.now = nowFun
	s.ctx = context.Background()
	s.startedAt = now
	s.nextFullSync = now

	err := s.setClient()
	if err != nil {
		panic(err)
	}

	return api, store, s
}

// TestMetricSimpleSync test "normal" scenario:
// Agent start and register metrics
// Some metrics disapear => mark inative
// Some re-appear and some new => mark active & register.
func TestMetricSimpleSync(t *testing.T) { // nolint: gocyclo
	var metrics []metricPayload

	now := time.Now()
	nowFun := func() time.Time {
		return now
	}

	api, store, s := setupMetricTest(nowFun)

	defer api.Close()

	now = now.Add(time.Minute)

	methods := s.syncToPerform()
	if full, ok := methods["metrics"]; !ok {
		t.Errorf("Expected to do a metrics sync")
	} else if !full {
		t.Errorf("Expected to do a full metrics sync")
	}

	if err := s.syncMetrics(methods["metrics"], false); err != nil {
		t.Fatal(err)
	}

	if api.ErrorCount > 0 {
		t.Fatal(api.LastError)
	}

	// We do 3 request: JWT auth, list metrics and register agent_status
	if api.RequestCount != 3 {
		t.Errorf("Did %d requests, want 3", api.RequestCount)
		api.ShowRequest(t, 10)
	}

	api.resources["metric"].Store(&metrics)

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

	api.ResetCount()

	s.lastSync = now
	s.nextFullSync = now.Add(time.Hour)
	now = now.Add(30 * time.Minute)

	store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: now},
			Labels: map[string]string{
				types.LabelName: "cpu_system",
			},
		},
	})

	methods = s.syncToPerform()
	if full, ok := methods["metrics"]; !ok {
		t.Errorf("Expected to do a metrics sync")
	} else if full {
		t.Errorf("Expected to NOT do a full metrics sync")
	}

	if err := s.syncMetrics(methods["metrics"], false); err != nil {
		t.Fatal(err)
	}

	if api.ErrorCount > 0 {
		t.Fatal(api.LastError)
	}

	// We do 2 request: list metrics, list inactive metrics
	// and register new metric
	if api.RequestCount != 3 {
		t.Errorf("Did %d requests, want 3", api.RequestCount)
		api.ShowRequest(t, 10)
	}

	api.resources["metric"].Store(&metrics)
	sort.Slice(metrics, func(i, j int) bool {
		return metrics[i].ID < metrics[j].ID
	})

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

	methods = s.syncToPerform()
	if _, ok := methods["metrics"]; ok {
		t.Errorf("Expected to NOT do a metrics sync")
	}

	api.ResetCount()

	s.lastSync = now
	s.nextFullSync = now.Add(time.Hour)
	now = now.Add(30 * time.Minute)

	// Register 1000 metrics
	for n := 0; n < 1000; n++ {
		store.PushPoints([]types.MetricPoint{
			{
				Point: types.Point{Time: now},
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

	methods = s.syncToPerform()
	if full, ok := methods["metrics"]; !ok {
		t.Errorf("Expected to do a metrics sync")
	} else if full {
		t.Errorf("Expected to NOT do a full metrics sync")
	}

	if err := s.syncMetrics(methods["metrics"], false); err != nil {
		t.Fatal(err)
	}

	if api.ErrorCount > 0 {
		t.Fatal(api.LastError)
	}

	// We do 1003 request: 3 for listing and 1000 registration
	if api.RequestCount != 1003 {
		t.Errorf("Did %d requests, want 1003", api.RequestCount)
		api.ShowRequest(t, 10)
	}

	api.resources["metric"].Store(&metrics)

	if len(metrics) != 1002 {
		t.Errorf("len(metrics) = %v, want %v", len(metrics), 1002)
	}

	api.ResetCount()

	s.lastSync = now
	s.nextFullSync = now.Add(time.Hour)
	now = now.Add(30 * time.Minute)

	store.DropAllMetrics()

	methods = s.syncToPerform()

	if full, ok := methods["metrics"]; !ok {
		t.Errorf("Expected to do a metrics sync")
	} else if full {
		t.Errorf("Expected to NOT do a full metrics sync")
	}

	if err := s.syncMetrics(methods["metrics"], false); err != nil {
		t.Fatal(err)
	}

	if api.ErrorCount > 0 {
		t.Fatal(api.LastError)
	}

	// We do 1001 request: 1001 to mark inactive all metrics but agent_status
	if api.RequestCount != 1001 {
		t.Errorf("Did %d requests, want 1001", api.RequestCount)
		api.ShowRequest(t, 10)
	}

	api.resources["metric"].Store(&metrics)

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

	api.ResetCount()

	s.lastSync = now
	s.nextFullSync = now.Add(time.Hour)
	now = now.Add(30 * time.Minute)

	// re-activate one metric + register one
	store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: now},
			Labels: map[string]string{
				types.LabelName: "cpu_system",
			},
		},
		{
			Point: types.Point{Time: now},
			Labels: map[string]string{
				types.LabelName: "disk_used",
				"item":          "/home",
			},
			Annotations: types.MetricAnnotations{BleemeoItem: "/home"},
		},
	})

	methods = s.syncToPerform()
	if full, ok := methods["metrics"]; !ok {
		t.Errorf("Expected to do a metrics sync")
	} else if full {
		t.Errorf("Expected to NOT do a full metrics sync")
	}

	if err := s.syncMetrics(methods["metrics"], false); err != nil {
		t.Fatal(err)
	}

	if api.ErrorCount > 0 {
		t.Fatal(api.LastError)
	}

	// We do 3 request: 1 to re-enable metric,
	// 1 search for metric before registration, 1 to register metric
	if api.RequestCount != 3 {
		t.Errorf("Did %d requests, want 3", api.RequestCount)
		api.ShowRequest(t, 10)
	}

	api.resources["metric"].Store(&metrics)

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

	api.ResetCount()

	s.lastSync = now
	s.nextFullSync = now.Add(time.Hour)
	now = now.Add(30 * time.Minute)
}

// TestMetricDeleted test that Glouton can update metrics deleted on Bleemeo.
func TestMetricDeleted(t *testing.T) { // nolint: gocyclo
	var metrics []metricPayload

	now := time.Now()
	nowFun := func() time.Time {
		return now
	}

	api, store, s := setupMetricTest(nowFun)

	defer api.Close()

	now = now.Add(time.Minute)

	store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: now},
			Labels: map[string]string{
				types.LabelName: "metric1",
			},
		},
		{
			Point: types.Point{Time: now},
			Labels: map[string]string{
				types.LabelName: "metric2",
			},
		},
		{
			Point: types.Point{Time: now},
			Labels: map[string]string{
				types.LabelName: "metric3",
			},
		},
	})

	methods := s.syncToPerform()
	if full, ok := methods["metrics"]; !ok {
		t.Errorf("Expected to do a metrics sync")
	} else if !full {
		t.Errorf("Expected to do a full metrics sync")
	}

	if err := s.syncMetrics(methods["metrics"], false); err != nil {
		t.Fatal(err)
	}

	if api.ErrorCount > 0 {
		t.Fatal(api.LastError)
	}

	// We do JWT auth, list of active metrics, 2 query to register metric + 1 to register agent_status
	if api.RequestCount != 9 {
		t.Errorf("Did %d requests, want 9", api.RequestCount)
		api.ShowRequest(t, 10)
	}

	api.resources["metric"].Store(&metrics)

	if len(metrics) != 4 { // 3 + agent_status
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}

	api.ResetCount()

	s.lastSync = now
	s.nextFullSync = now.Add(time.Hour)
	now = now.Add(70 * time.Minute)

	store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: now},
			Labels: map[string]string{
				types.LabelName: "metric1",
			},
		},
		{
			Point: types.Point{Time: now},
			Labels: map[string]string{
				types.LabelName: "metric2",
			},
		},
		{
			Point: types.Point{Time: now},
			Labels: map[string]string{
				types.LabelName: "metric3",
			},
		},
	})

	// API deleted metric1
	for _, m := range metrics {
		if m.Name == "metric1" {
			api.resources["metric"].DelStore(m.ID)
		}
	}

	methods = s.syncToPerform()
	if full, ok := methods["metrics"]; !ok {
		t.Errorf("Expected to do a metrics sync")
	} else if !full {
		t.Errorf("Expected to do a full metrics sync")
	}

	if err := s.syncMetrics(methods["metrics"], false); err != nil {
		t.Fatal(err)
	}

	if api.ErrorCount > 0 {
		t.Fatal(api.LastError)
	}

	api.ResetCount()

	s.lastSync = now
	s.nextFullSync = now.Add(time.Hour)
	now = now.Add(1 * time.Minute)

	// metric1 is still alive
	store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: now},
			Labels: map[string]string{
				types.LabelName: "metric1",
			},
		},
	})

	methods = s.syncToPerform()
	if full, ok := methods["metrics"]; !ok {
		t.Errorf("Expected to do a metrics sync")
	} else if full {
		t.Errorf("Expected to NOT do a full metrics sync")
	}

	if err := s.syncMetrics(methods["metrics"], false); err != nil {
		t.Fatal(err)
	}

	if api.ErrorCount > 0 {
		t.Fatal(api.LastError)
	}

	// We list active metrics, 2 query to re-register metric
	if api.RequestCount != 3 {
		t.Errorf("Did %d requests, want 3", api.RequestCount)
		api.ShowRequest(t, 10)
	}

	api.resources["metric"].Store(&metrics)

	if len(metrics) != 4 { // 3 + agent_status
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}

	api.ResetCount()

	s.lastSync = now
	s.nextFullSync = now.Add(2 * time.Hour)
	now = now.Add(90 * time.Minute)

	// API deleted metric2
	for _, m := range metrics {
		if m.Name == "metric2" {
			api.resources["metric"].DelStore(m.ID)
		}
	}

	// all metrics are inactive
	if err := s.syncMetrics(false, false); err != nil {
		t.Fatal(err)
	}

	if api.ErrorCount > 0 {
		t.Fatal(api.LastError)
	}

	api.resources["metric"].Store(&metrics)

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

	api.ResetCount()

	s.lastSync = now
	s.nextFullSync = now.Add(time.Hour)
	now = now.Add(1 * time.Minute)

	// API deleted metric3
	for _, m := range metrics {
		if m.Name == "metric3" {
			api.resources["metric"].DelStore(m.ID)
		}
	}

	store.PushPoints([]types.MetricPoint{
		{
			Point: types.Point{Time: now},
			Labels: map[string]string{
				types.LabelName: "metric1",
			},
		},
		{
			Point: types.Point{Time: now},
			Labels: map[string]string{
				types.LabelName: "metric2",
			},
		},
		{
			Point: types.Point{Time: now},
			Labels: map[string]string{
				types.LabelName: "metric3",
			},
		},
	})

	if err := s.syncMetrics(false, false); err != nil {
		t.Fatal(err)
	}

	if api.ErrorCount > 0 {
		t.Fatal(api.LastError)
	}

	api.resources["metric"].Store(&metrics)

	if len(metrics) != 4 {
		t.Errorf("len(metrics) = %d, want 4", len(metrics))
	}

	for _, m := range metrics {
		if !m.DeactivatedAt.IsZero() {
			t.Errorf("%v should not be deactivated", m)
		}
	}
}
