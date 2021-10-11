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

//nolint:scopelint,goconst
package synchronizer

import (
	"context"
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

	"github.com/google/go-cmp/cmp"
)

const (
	idAgentTypeAgent = "44aefd1c-29bc-4c67-89cd-197efc1d6650"
	idAgentTypeSNMP  = "cf1d4e06-1058-4149-864f-82c6b2ba7c7a"
	idAgentMain      = "1ea3eaa7-3c29-413c-b00e-9dbd7183fb26"
	idAgentSNMP      = "69956bc0-943f-4125-bb9b-eb4743c83b3c"
	idAccountConfig  = "553b1cd5-f10a-4f17-87e8-92dc6717a93f"
	passwordAgent    = "a-secret-password"
)

type mockMetric struct {
	Name   string
	labels map[string]string
}

func (m mockMetric) Labels() map[string]string {
	if m.labels != nil {
		return m.labels
	}

	return map[string]string{types.LabelName: m.Name}
}

func (m mockMetric) Annotations() types.MetricAnnotations {
	return types.MetricAnnotations{}
}

func (m mockMetric) Points(start, end time.Time) ([]types.Point, error) {
	return nil, errNotImplemented
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

	metrics = prioritizeAndFilterMetrics(types.MetricFormatBleemeo, metrics, false)
	metrics2 = prioritizeAndFilterMetrics(types.MetricFormatBleemeo, metrics2, true)

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

func TestPrioritizeAndFilterMetrics2(t *testing.T) {
	type order struct {
		LabelBefore string
		LabelAfter  string
	}

	cases := []struct {
		name   string
		inputs []string
		format types.MetricFormat
		order  []order
	}{
		{
			name: "without item are sorted first",
			inputs: []string{
				`__name__="cpu_used"`,
				`__name__="net_bits_recv",item="eth0"`,
				`__name__="net_bits_sent",item="eth0"`,
				`__name__="mem_used"`,
			},
			format: types.MetricFormatBleemeo,
			order: []order{
				{LabelBefore: `__name__="cpu_used"`, LabelAfter: `__name__="net_bits_recv",item="eth0"`},
				{LabelBefore: `__name__="cpu_used"`, LabelAfter: `__name__="net_bits_sent",item="eth0"`},
				{LabelBefore: `__name__="mem_used"`, LabelAfter: `__name__="net_bits_recv",item="eth0"`},
				{LabelBefore: `__name__="mem_used"`, LabelAfter: `__name__="net_bits_sent",item="eth0"`},
			},
		},
		{
			name: "network and fs are after",
			inputs: []string{
				`__name__="io_reads",item="the_item"`,
				`__name__="net_bits_recv",item="the_item"`,
				`__name__="io_writes",item="the_item"`,
				`__name__="cpu_used"`,
				`__name__="disk_used_perc",item="the_item"`,
				`__name__="io_utilization",item="the_item"`,
			},
			format: types.MetricFormatBleemeo,
			order: []order{
				{LabelBefore: `__name__="io_reads",item="the_item"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="io_writes",item="the_item"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="io_utilization",item="the_item"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="cpu_used"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="io_reads",item="the_item"`, LabelAfter: `__name__="disk_used_perc",item="the_item"`},
				{LabelBefore: `__name__="io_writes",item="the_item"`, LabelAfter: `__name__="disk_used_perc",item="the_item"`},
				{LabelBefore: `__name__="io_utilization",item="the_item"`, LabelAfter: `__name__="disk_used_perc",item="the_item"`},
				{LabelBefore: `__name__="cpu_used"`, LabelAfter: `__name__="disk_used_perc",item="the_item"`},
			},
		},
		{
			name: "custom are always after",
			inputs: []string{
				`__name__="custom_metric"`,
				`__name__="net_bits_recv",item="eth0"`,
				`__name__="net_bits_recv",item="the_item"`,
				`__name__="cpu_used"`,
				`__name__="custom_metric_item",item="the_item"`,
				`__name__="io_reads",item="the_item"`,
				`__name__="custom_metric2"`,
			},
			format: types.MetricFormatBleemeo,
			order: []order{
				{LabelBefore: `__name__="cpu_used"`, LabelAfter: `__name__="custom_metric_item",item="the_item"`},
				{LabelBefore: `__name__="cpu_used"`, LabelAfter: `__name__="custom_metric2"`},
				{LabelBefore: `__name__="cpu_used"`, LabelAfter: `__name__="custom_metric"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth0"`, LabelAfter: `__name__="custom_metric_item",item="the_item"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth0"`, LabelAfter: `__name__="custom_metric2"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth0"`, LabelAfter: `__name__="custom_metric"`},
				{LabelBefore: `__name__="net_bits_recv",item="the_item"`, LabelAfter: `__name__="custom_metric_item",item="the_item"`},
				{LabelBefore: `__name__="net_bits_recv",item="the_item"`, LabelAfter: `__name__="custom_metric2"`},
				{LabelBefore: `__name__="net_bits_recv",item="the_item"`, LabelAfter: `__name__="custom_metric"`},
				{LabelBefore: `__name__="io_reads",item="the_item"`, LabelAfter: `__name__="custom_metric_item",item="the_item"`},
				{LabelBefore: `__name__="io_reads",item="the_item"`, LabelAfter: `__name__="custom_metric2"`},
				{LabelBefore: `__name__="io_reads",item="the_item"`, LabelAfter: `__name__="custom_metric"`},
			},
		},
		{
			name: "network item are sorted",
			inputs: []string{
				`__name__="net_bits_sent",item="eno1"`,
				`__name__="net_bits_recv",item="the_item"`,
				`__name__="net_bits_recv",item="eth0"`,
				`__name__="net_bits_recv",item="br-1234"`,
				`__name__="net_bits_recv",item="eno1"`,
				`__name__="net_bits_sent",item="eth0"`,
				`__name__="net_bits_recv",item="eth1"`,
				`__name__="net_bits_recv",item="br-1"`,
				`__name__="net_bits_recv",item="br-0"`,
			},
			format: types.MetricFormatBleemeo,
			order: []order{
				{LabelBefore: `__name__="net_bits_recv",item="eth0"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth0"`, LabelAfter: `__name__="net_bits_recv",item="br-0"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth0"`, LabelAfter: `__name__="net_bits_recv",item="br-1234"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth0"`, LabelAfter: `__name__="net_bits_recv",item="eth1"`},
				{LabelBefore: `__name__="net_bits_sent",item="eth0"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="net_bits_sent",item="eth0"`, LabelAfter: `__name__="net_bits_recv",item="br-0"`},
				{LabelBefore: `__name__="net_bits_sent",item="eth0"`, LabelAfter: `__name__="net_bits_recv",item="br-1234"`},
				{LabelBefore: `__name__="net_bits_sent",item="eth0"`, LabelAfter: `__name__="net_bits_recv",item="eth1"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth1"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth1"`, LabelAfter: `__name__="net_bits_recv",item="br-0"`},
				{LabelBefore: `__name__="net_bits_recv",item="eth1"`, LabelAfter: `__name__="net_bits_recv",item="br-1234"`},
				{LabelBefore: `__name__="net_bits_recv",item="eno1"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="net_bits_recv",item="eno1"`, LabelAfter: `__name__="net_bits_recv",item="br-0"`},
				{LabelBefore: `__name__="net_bits_recv",item="eno1"`, LabelAfter: `__name__="net_bits_recv",item="br-1234"`},
				{LabelBefore: `__name__="net_bits_sent",item="eno1"`, LabelAfter: `__name__="net_bits_recv",item="the_item"`},
				{LabelBefore: `__name__="net_bits_sent",item="eno1"`, LabelAfter: `__name__="net_bits_recv",item="br-0"`},
				{LabelBefore: `__name__="net_bits_sent",item="eno1"`, LabelAfter: `__name__="net_bits_recv",item="br-1234"`},
				{LabelBefore: `__name__="net_bits_recv",item="br-0"`, LabelAfter: `__name__="net_bits_recv",item="br-1"`},
			},
		},
	}

	for _, tt := range cases {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			metrics := make([]types.Metric, 0, len(tt.inputs))

			for _, lbls := range tt.inputs {
				metrics = append(metrics, mockMetric{labels: types.TextToLabels(lbls)})
			}

			result := prioritizeAndFilterMetrics(tt.format, metrics, false)

			for _, ord := range tt.order {
				firstIdx := -1
				secondIdx := -1

				for i, m := range result {
					if reflect.DeepEqual(m.Labels(), types.TextToLabels(ord.LabelBefore)) {
						if firstIdx == -1 {
							firstIdx = i
						} else {
							t.Errorf("metric labels %s present at index %d and %d", ord.LabelBefore, firstIdx, i)
						}
					}

					if reflect.DeepEqual(m.Labels(), types.TextToLabels(ord.LabelAfter)) {
						if secondIdx == -1 {
							secondIdx = i
						} else {
							t.Errorf("metric labels %s present at index %d and %d", ord.LabelAfter, secondIdx, i)
						}
					}
				}

				switch {
				case firstIdx == -1:
					t.Errorf("metric %s is not present", ord.LabelBefore)
				case secondIdx == -1:
					t.Errorf("metric %s is not present", ord.LabelAfter)
				case firstIdx >= secondIdx:
					t.Errorf("metric %s is after metric %s (%d >= %d)", ord.LabelBefore, ord.LabelAfter, firstIdx, secondIdx)
				}
			}
		})
	}
}

func Test_metricComparator_IsSignificantItem(t *testing.T) {
	tests := []struct {
		item string
		want bool
	}{
		{
			item: "/",
			want: true,
		},
		{
			item: "/home",
			want: true,
		},
		{
			item: "/home",
			want: true,
		},
		{
			item: "/srv",
			want: true,
		},
		{
			item: "/var",
			want: true,
		},
		{
			item: "eth0",
			want: true,
		},
		{
			item: "eth1",
			want: true,
		},
		{
			item: "ens18",
			want: true,
		},
		{
			item: "ens1",
			want: true,
		},
		{
			item: "eno1",
			want: true,
		},
		{
			item: "eno5",
			want: true,
		},
		{
			item: "enp7s0",
			want: true,
		},
		{
			item: "ens1f1",
			want: true,
		},

		{
			item: "/home/user",
			want: false,
		},
		{
			item: "enp7s0.4010",
			want: false,
		},
		{
			item: "eth99",
			want: false,
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.item, func(t *testing.T) {
			t.Parallel()

			m := newComparator(types.MetricFormatBleemeo)
			if got := m.IsSignificantItem(tt.item); got != tt.want {
				t.Errorf("metricComparator.IsSignificantItem() = %v, want %v", got, tt.want)
			}
		})
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
	t.Helper()

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
	cfg.Set("blackbox.enable", true)

	cache := cache.Cache{}

	cache.SetAccountConfigs([]bleemeoTypes.AccountConfig{
		{
			ID:   idAccountConfig,
			Name: "default",
		},
	})
	cache.SetAgentTypes([]bleemeoTypes.AgentType{
		{
			ID:   idAgentTypeAgent,
			Name: bleemeoTypes.AgentTypeAgent,
		},
		{
			ID:   idAgentTypeSNMP,
			Name: bleemeoTypes.AgentTypeSNMP,
		},
	})
	cache.SetAgentConfigs([]bleemeoTypes.AgentConfig{
		{
			MetricsAllowlist: "",
			MetricResolution: 10,
			AccountConfig:    idAccountConfig,
			AgentType:        idAgentTypeAgent,
		},
		{
			MetricsAllowlist: "",
			MetricResolution: 60,
			AccountConfig:    idAccountConfig,
			AgentType:        idAgentTypeSNMP,
		},
	})

	mainAgent := bleemeoTypes.Agent{
		ID:              idAgentMain,
		CurrentConfigID: idAccountConfig,
		AgentType:       idAgentTypeAgent,
	}

	cache.SetAgentList([]bleemeoTypes.Agent{
		mainAgent,
		{
			ID:              idAgentSNMP,
			CurrentConfigID: idAccountConfig,
			AgentType:       idAgentTypeSNMP,
		},
	})
	cache.SetAgent(mainAgent)

	state := state.NewMock()

	discovery := &discovery.MockDiscoverer{
		UpdatedAt: helper.mt.Now(),
	}

	var err error

	helper.s, err = New(Option{
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
	if err != nil {
		t.Fatal(err)
	}

	helper.s.now = helper.mt.Now
	helper.s.ctx = context.Background()
	helper.s.startedAt = helper.mt.Now()
	helper.s.nextFullSync = helper.mt.Now()
	helper.s.agentID = idAgentMain
	helper.api.JWTUsername = idAgentMain + "@bleemeo.com"
	helper.api.JWTPassword = passwordAgent
	_ = helper.s.option.State.Set("password", passwordAgent)

	if err := helper.s.setClient(); err != nil {
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
		h.s.client = &wrapperClient{s: h.s, client: h.s.realClient, duplicateChecked: true}

		methods := h.s.syncToPerform()
		if full, ok := methods[syncMethodMetric]; !ok && !forceFirst {
			break
		} else if full {
			result.didFull = true
		}

		err := h.s.syncMetrics(methods[syncMethodMetric], false)
		result.lastErr = err

		if err == nil {
			// when no error, Synchronizer update it lastSync/nextFullSync
			h.s.lastSync = startAt
			h.s.nextFullSync = h.mt.Now().Add(time.Hour)
		}

		h.mt.now = h.mt.Now().Add(timeStep)
	}

	_, result.stillWantSync = h.s.syncToPerform()[syncMethodMetric]

	return result
}

// Check verify that runSync was successful without any server error.
func (res runResult) Check(name string, wantFull bool) {
	res.h.t.Helper()

	res.CheckAllowError(name, wantFull)

	if res.h.api.ServerErrorCount > 0 {
		res.h.t.Errorf("%s: had %d server error, last is %v", name, res.h.api.ServerErrorCount, res.h.api.LastServerError)
	}
}

// CheckNoError verify that runSync was successful without any error (client or server).
func (res runResult) CheckNoError(name string, wantFull bool) {
	res.h.t.Helper()

	res.Check(name, wantFull)

	if res.h.api.ClientErrorCount > 0 {
		res.h.t.Errorf("%s: had %d client error", name, res.h.api.ClientErrorCount)
	}
}

// CheckAllowError verify that runSync was successful possibly with error but last sync must be successful.
func (res runResult) CheckAllowError(name string, wantFull bool) {
	res.h.t.Helper()

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
//nolint:cyclop
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
				AgentID:    idAgentMain,
				LabelsText: "",
			},
			Name: agentStatusName,
		},
	}

	if diff := cmp.Diff(want, metrics); diff != "" {
		t.Errorf("metrics mismatch (-want +got):\n%s", diff)
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
				AgentID:    idAgentMain,
				LabelsText: "",
			},
			Name: agentStatusName,
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:         "2",
				AgentID:    idAgentMain,
				LabelsText: "",
			},
			Name: "cpu_system",
		},
	}

	if diff := cmp.Diff(want, metrics); diff != "" {
		t.Errorf("metrics mismatch (-want +got):\n%s", diff)
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
//nolint:cyclop
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
		metric, _ := valuePtr.(*metricPayload)
		if metric.Name == "deny-me" {
			return clientError{
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
//nolint:cyclop
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
				metric, _ := valuePtr.(*metricPayload)
				if metric.Name == "deny-me" || metric.Name == "deny-me-also" {
					return clientError{
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
func TestMetricTooMany(t *testing.T) { //nolint:cyclop
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

		metric, _ := valuePtr.(*metricPayload)

		if metric.DeactivatedAt.IsZero() {
			metrics := helper.Metrics()
			countActive := 0

			for _, m := range metrics {
				if m.DeactivatedAt.IsZero() && m.ID != metric.ID {
					countActive++
				}
			}

			if countActive >= 3 {
				return clientError{
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

// Few tests with SNMP metrics.
func TestWithSNMP(t *testing.T) {
	helper := newMetricHelper(t)
	defer helper.Close()

	helper.AddTime(time.Minute)

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
				types.LabelName:       "ifOutOctets",
				types.LabelSNMPTarget: "127.0.0.1",
			},
			Annotations: types.MetricAnnotations{
				BleemeoAgentID: idAgentSNMP,
			},
		},
	})

	helper.RunSync(1, 0, false).CheckNoError("first sync", true)

	metrics := helper.Metrics()
	want := []metricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:         "1",
				AgentID:    idAgentMain,
				LabelsText: "",
			},
			Name: agentStatusName,
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:         "2",
				AgentID:    idAgentMain,
				LabelsText: "",
			},
			Name: "cpu_system",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:         "3",
				AgentID:    idAgentSNMP,
				LabelsText: `__name__="ifOutOctets",snmp_target="127.0.0.1"`,
			},
			Name: "ifOutOctets",
		},
	}

	if diff := cmp.Diff(want, metrics); diff != "" {
		t.Errorf("metrics mismatch (-want +got):\n%s", diff)
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
			name:    "too many custom metrics",
			content: `{"label":["Too many non standard metrics"]}`,
			want:    bleemeoTypes.FailureTooManyMetric,
		},
		{
			name:    "too many metrics",
			content: `{"label":["Too many metrics"]}`,
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
