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

// Package registry package implement a dynamic collection of metrics sources
//
// It support both pushed metrics (using AddMetricPointFunction) and pulled
// metrics thought Collector or Gatherer
//
//nolint:dupl
package registry

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/influxdata/telegraf"
	dto "github.com/prometheus/client_model/go"
	commonmodel "github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/relabel"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/util/gate"
	"golang.org/x/sync/errgroup"
)

//nolint:gochecknoinits
func init() {
	// We want to keep the strict name validation.
	// It is done globally in agent/agent.go,
	// but since the agent package isn't loaded during these tests,
	// we must do it here too.
	commonmodel.NameValidationScheme = commonmodel.LegacyValidation //nolint: staticcheck
}

const testAgentID = "fcdc81a8-5bce-4305-8108-8e1e75439329"

// nilTime is a time that is replaced by "no timestamp" when the input support it.
// When not supported, it fallback on zero and/or epoc timestamp.
var nilTime = time.Date(1980, 1, 2, 3, 4, 5, 7, time.UTC) //nolint:gochecknoglobals

type fakeGatherer struct {
	l         sync.Mutex
	name      string
	callCount int
	hangCtx   context.Context //nolint:containedctx
	response  []*dto.MetricFamily
}

type fakeFilter struct{}

type fakeAppenderCallback struct {
	input []types.MetricPoint
}

type fakeInput struct {
	input []types.MetricPoint
}

func (f *fakeFilter) FilterPoints(points []types.MetricPoint, allowNeededByRules bool) []types.MetricPoint {
	_ = allowNeededByRules

	return points
}

func (f *fakeFilter) FilterFamilies(families []*dto.MetricFamily, allowNeededByRules bool) []*dto.MetricFamily {
	_ = allowNeededByRules

	return families
}

func (f *fakeFilter) IsMetricAllowed(lbls labels.Labels, allowNeededByRules bool) bool {
	_ = lbls
	_ = allowNeededByRules

	return true
}

type fakeArchive struct {
	l           sync.Mutex
	currentFile string
}

func (a *fakeArchive) Create(filename string) (io.Writer, error) {
	a.l.Lock()
	defer a.l.Unlock()

	a.currentFile = filename

	return io.Discard, nil
}

func (a *fakeArchive) CurrentFileName() string {
	a.l.Lock()
	defer a.l.Unlock()

	return a.currentFile
}

func (g *fakeGatherer) fillResponse() {
	helpStr := "fake metric"
	value := 1.0

	g.response = append(g.response, &dto.MetricFamily{
		Help: &helpStr,
		Name: &g.name,
		Type: dto.MetricType_GAUGE.Enum(),
		Metric: []*dto.Metric{
			{
				Label: []*dto.LabelPair{},
				Gauge: &dto.Gauge{Value: &value},
			},
		},
	})
}

func (g *fakeGatherer) CallCount() int {
	g.l.Lock()
	defer g.l.Unlock()

	return g.callCount
}

func (g *fakeGatherer) Gather() ([]*dto.MetricFamily, error) {
	g.l.Lock()
	g.callCount++
	g.l.Unlock()

	if g.hangCtx != nil {
		<-g.hangCtx.Done()
	}

	return model.FamiliesDeepCopy(g.response), nil
}

func (cb fakeAppenderCallback) CollectWithState(_ context.Context, state GatherState, app storage.Appender) error {
	_ = state

	if err := model.SendPointsToAppender(cb.input, app); err != nil {
		return err
	}

	return app.Commit()
}

func (cb fakeInput) SampleConfig() string {
	return ""
}

func (cb fakeInput) Gather(acc telegraf.Accumulator) error {
	for _, pts := range cb.input {
		part := strings.SplitN(pts.Labels[types.LabelName], "_", 2)

		var (
			measurement string
			fieldName   string
		)

		if len(part) == 1 {
			// If there is zero "_" in the metric name, we use
			// empty measurement.
			// This is a bit an hack but match behavior of glouton/inputs/Accumulator
			measurement = ""
			fieldName = part[0]
		} else {
			measurement = part[0]
			fieldName = part[1]
		}

		if pts.Time.Equal(nilTime) {
			acc.AddGauge(
				measurement,
				map[string]any{fieldName: pts.Value},
				pts.Labels, // no meta-label. No Input send meta-label today
			)
		} else {
			acc.AddGauge(
				measurement,
				map[string]any{fieldName: pts.Value},
				pts.Labels, // no meta-label. No Input send meta-label today
				pts.Time,
			)
		}
	}

	return nil
}

func makeDelayHook(delay time.Duration) UpdateDelayHook {
	return func(map[string]string) (time.Duration, bool) {
		return delay, false
	}
}

func TestRegistry_Register(t *testing.T) {
	reg, err := New(Option{})
	if err != nil {
		t.Fatal(err)
	}

	var (
		id1 int
		id2 int
	)

	gather1 := &fakeGatherer{
		name: "gather1",
	}
	gather1.fillResponse()

	gather2 := &fakeGatherer{
		name: "gather2",
	}
	gather2.fillResponse()

	if id1, err = reg.RegisterGatherer(RegistrationOption{}, gather1); err != nil {
		t.Errorf("reg.RegisterGatherer(gather1) failed: %v", err)
	}

	_, _ = reg.Gather()

	if gather1.callCount != 1 {
		t.Errorf("gather1.callCount = %v, want 1", gather1.callCount)
	}

	if !reg.Unregister(id1) {
		t.Errorf("reg.Unregister(%d) failed", id1)
	}

	_, _ = reg.Gather()

	if gather1.callCount != 1 {
		t.Errorf("gather1.callCount = %v, want 1", gather1.callCount)
	}

	if id1, err = reg.RegisterGatherer(RegistrationOption{ExtraLabels: map[string]string{"name": "value"}}, gather1); err != nil {
		t.Errorf("re-reg.RegisterGatherer(gather1) failed: %v", err)
	}

	if id2, err = reg.RegisterGatherer(RegistrationOption{}, gather2); err != nil {
		t.Errorf("re-reg.RegisterGatherer(gather2) failed: %v", err)
	}

	_, _ = reg.Gather()

	if gather1.callCount != 2 {
		t.Errorf("gather1.callCount = %v, want 2", gather1.callCount)
	}

	if gather2.callCount != 1 {
		t.Errorf("gather2.callCount = %v, want 1", gather2.callCount)
	}

	if !reg.Unregister(id1) {
		t.Errorf("reg.Unregister(%d) failed", id1)
	}

	if !reg.Unregister(id2) {
		t.Errorf("reg.Unregister(%d) failed", id2)
	}

	_, _ = reg.Gather()

	if gather1.callCount != 2 {
		t.Errorf("gather1.callCount = %v, want 2", gather1.callCount)
	}

	if gather2.callCount != 1 {
		t.Errorf("gather2.callCount = %v, want 1", gather2.callCount)
	}

	stopCallCount := 0

	if id1, err = reg.RegisterGatherer(RegistrationOption{StopCallback: func() { stopCallCount++ }, ExtraLabels: map[string]string{"dummy": "value", "empty-value-to-dropped": ""}}, gather1); err != nil {
		t.Errorf("reg.RegisterGatherer(gather1) failed: %v", err)
	}

	if _, err = reg.RegisterGatherer(RegistrationOption{}, gather2); err != nil {
		t.Errorf("re-reg.RegisterGatherer(gather2) failed: %v", err)
	}

	reg.UpdateRegistrationHooks(func(_ context.Context, labels map[string]string) (newLabel map[string]string, retryLater bool) {
		labels[types.LabelMetaBleemeoUUID] = testAgentID

		return labels, false
	}, makeDelayHook(gloutonMinimalInterval))

	now := time.Now()

	result, err := reg.GatherWithState(t.Context(), GatherState{T0: now})
	if err != nil {
		t.Error(err)
	}

	helpText := ""
	dummyName := "dummy"
	dummyValue := "value"
	instanceIDName := types.LabelInstanceUUID
	instanceIDValue := testAgentID
	value := 1.0
	want := []*dto.MetricFamily{
		{
			Name: &gather1.name,
			Help: &helpText,
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: &dummyName, Value: &dummyValue},
						{Name: &instanceIDName, Value: &instanceIDValue},
					},
					Untyped: &dto.Untyped{
						Value: &value,
					},
				},
			},
		},
		{
			Name: &gather2.name,
			Help: &helpText,
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: &instanceIDName, Value: &instanceIDValue},
					},
					Untyped: &dto.Untyped{
						Value: &value,
					},
				},
			},
		},
	}

	if diff := types.DiffMetricFamilies(want, result, false, false); diff != "" {
		t.Errorf("reg.Gather() diff: (-want +got)\n%s", diff)
	}

	reg.Unregister(id1)

	if stopCallCount != 1 {
		t.Errorf("stopCallCount = %v, want 1", stopCallCount)
	}
}

func TestRegistryDiagnostic(t *testing.T) {
	reg, err := New(Option{})
	if err != nil {
		t.Fatal(err)
	}

	gather1 := &fakeGatherer{
		name: "gather1",
	}
	gather2 := &fakeGatherer{
		name: "gather2",
	}

	if _, err := reg.RegisterGatherer(RegistrationOption{}, gather1); err != nil {
		t.Errorf("reg.RegisterGatherer(gather1) failed: %v", err)
	}

	if _, err := reg.RegisterGatherer(RegistrationOption{ExtraLabels: map[string]string{"name": "value"}}, gather1); err != nil {
		t.Errorf("re-reg.RegisterGatherer(gather1) failed: %v", err)
	}

	if _, err := reg.RegisterGatherer(RegistrationOption{}, gather2); err != nil {
		t.Errorf("re-reg.RegisterGatherer(gather2) failed: %v", err)
	}

	if _, err := reg.RegisterGatherer(RegistrationOption{StopCallback: func() {}, ExtraLabels: map[string]string{"dummy": "value", "empty-value-to-dropped": ""}}, gather1); err != nil {
		t.Errorf("reg.RegisterGatherer(gather1) failed: %v", err)
	}

	if _, err := reg.RegisterGatherer(RegistrationOption{}, gather2); err != nil {
		t.Errorf("re-reg.RegisterGatherer(gather2) failed: %v", err)
	}

	reg.UpdateRegistrationHooks(func(_ context.Context, labels map[string]string) (newLabel map[string]string, retryLater bool) {
		labels[types.LabelMetaBleemeoUUID] = testAgentID

		return labels, false
	}, makeDelayHook(250*time.Millisecond))

	// After this delay, registry should have run at least once.
	time.Sleep(300 * time.Millisecond)

	if err := reg.DiagnosticArchive(t.Context(), &fakeArchive{}); err != nil {
		t.Error(err)
	}
}

func TestRegistry_pushPoint(t *testing.T) {
	reg, err := New(Option{Filter: &fakeFilter{}})
	if err != nil {
		t.Fatal(err)
	}

	t0 := time.Date(2020, 3, 2, 10, 30, 0, 0, time.UTC)
	t0MS := t0.UnixNano() / 1e6

	pusher := reg.WithTTL(24 * time.Hour)
	pusher.PushPoints(
		t.Context(),
		[]types.MetricPoint{
			{
				Point: types.Point{Value: 1.0, Time: t0},
				Labels: map[string]string{
					"__name__": "point1",
					"dummy":    "value",
				},
			},
			{
				Point: types.Point{Value: 2.0, Time: t0},
				Labels: map[string]string{
					"__name__": "unfixable-name#~",
					"item":     "something",
				},
			},
			{
				Point: types.Point{Value: 3.0, Time: t0},
				Labels: map[string]string{
					"__name__": "fixable-name.0",
					"extra":    "label",
				},
			},
		},
	)

	got, err := reg.Gather()
	if err != nil {
		t.Error(err)
	}

	metricName1 := "point1"
	metricName2 := "fixable_name_0"
	helpText := ""
	instanceIDName := types.LabelInstanceUUID
	instanceIDValue := testAgentID
	value1 := 1.0
	value2 := 3.0
	want := []*dto.MetricFamily{
		{
			Name: &metricName2,
			Help: &helpText,
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Untyped: &dto.Untyped{
						Value: &value2,
					},
					TimestampMs: &t0MS,
				},
			},
		},
		{
			Name: &metricName1,
			Help: &helpText,
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Untyped: &dto.Untyped{
						Value: &value1,
					},
					TimestampMs: &t0MS,
				},
			},
		},
	}

	sort.Slice(got, func(i, j int) bool {
		return got[i].GetName() < got[j].GetName()
	})

	if diff := types.DiffMetricFamilies(want, got, false, false); diff != "" {
		t.Errorf("Gather() mismatch: (-want +got):\n%s", diff)
	}

	reg.UpdateRegistrationHooks(func(_ context.Context, labels map[string]string) (newLabel map[string]string, retryLater bool) {
		labels[types.LabelMetaBleemeoUUID] = testAgentID

		return labels, false
	}, makeDelayHook(gloutonMinimalInterval))

	got, err = reg.Gather()
	if err != nil {
		t.Error(err)
	}

	if len(got) > 0 {
		t.Errorf("reg.Gather() len=%v, want 0", len(got))
	}

	pusher.PushPoints(
		t.Context(),
		[]types.MetricPoint{
			{
				Point: types.Point{Value: 1.0, Time: t0},
				Labels: map[string]string{
					"__name__": "point1",
					"dummy":    "value",
				},
			},
		},
	)

	got, err = reg.Gather()
	if err != nil {
		t.Error(err)
	}

	want = []*dto.MetricFamily{
		{
			Name: &metricName1,
			Help: &helpText,
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: &instanceIDName, Value: &instanceIDValue},
					},
					Untyped: &dto.Untyped{
						Value: &value1,
					},
					TimestampMs: &t0MS,
				},
			},
		},
	}

	if diff := types.DiffMetricFamilies(want, got, false, false); diff != "" {
		t.Errorf("Gather() mismatch: (-want +got):\n%s", diff)
	}
}

func TestRegistry_applyRelabel(t *testing.T) {
	type fields struct {
		relabelConfigs []*relabel.Config
	}

	type args struct {
		input map[string]string
	}

	tests := []struct {
		name            string
		fields          fields
		relabelHook     RelabelHook
		args            args
		want            labels.Labels
		wantWithMeta    map[string]string
		wantAnnotations types.MetricAnnotations
	}{
		{
			name:   "node_exporter",
			fields: fields{relabelConfigs: getDefaultRelabelConfig()},
			args: args{map[string]string{
				types.LabelMetaGloutonFQDN: "hostname",
				types.LabelMetaGloutonPort: "8015",
				types.LabelMetaPort:        "8015",
			}},
			want: labels.FromMap(map[string]string{
				types.LabelInstance: "hostname:8015",
			}),
			wantAnnotations: types.MetricAnnotations{},
		},
		{
			name:   "node_exporter, with LabelMetaSendScraperUUID",
			fields: fields{relabelConfigs: getDefaultRelabelConfig()},
			args: args{map[string]string{
				types.LabelMetaGloutonFQDN:     "hostname",
				types.LabelMetaGloutonPort:     "8015",
				types.LabelMetaPort:            "8015",
				types.LabelMetaSendScraperUUID: "yes",
			}},
			want: labels.FromMap(map[string]string{
				types.LabelInstance: "hostname:8015",
			}),
			wantAnnotations: types.MetricAnnotations{},
		},
		{
			name:   "mysql container",
			fields: fields{relabelConfigs: getDefaultRelabelConfig()},
			args: args{map[string]string{
				types.LabelMetaServiceName:     "mysql",
				types.LabelMetaServiceInstance: "mysql_1",
				types.LabelMetaContainerName:   "mysql_1",
				types.LabelMetaContainerID:     "1234",
				types.LabelMetaGloutonFQDN:     "hostname",
				types.LabelMetaGloutonPort:     "8015",
				types.LabelMetaServicePort:     "3306",
				types.LabelMetaPort:            "3306",
				// addMetaLabels should add this meta-label
				types.LabelMetaInstanceUseContainerName: "yes",
			}},
			want: labels.FromMap(map[string]string{
				types.LabelContainerName: "mysql_1",
				types.LabelInstance:      "hostname-mysql_1:3306",
			}),
			wantAnnotations: types.MetricAnnotations{
				ServiceName:     "mysql",
				ServiceInstance: "mysql_1",
				ContainerID:     "1234",
			},
		},
		{
			name:   "blackbox_probe_icmp",
			fields: fields{relabelConfigs: getDefaultRelabelConfig()},
			args: args{map[string]string{
				types.LabelMetaBleemeoTargetAgent:     "icmp://8.8.8.8",
				types.LabelMetaProbeScraperName:       "test",
				types.LabelMetaBleemeoTargetAgentUUID: "c571f9cf-6f07-492a-9e86-b8d5f5027557",
				types.LabelMetaBleemeoUUID:            "a39e5a8e-34cf-4b15-87bd-4b9cdaa59c42",
				// necessary for some labels to be applied
				types.LabelMetaProbeServiceUUID: "dcb8e864-0a1f-4a67-b470-327ceb461b4e",
				types.LabelMetaSendScraperUUID:  "yes",
			}},
			want: labels.FromMap(map[string]string{
				types.LabelInstance:     "icmp://8.8.8.8",
				types.LabelScraper:      "test",
				types.LabelInstanceUUID: "c571f9cf-6f07-492a-9e86-b8d5f5027557",
				types.LabelScraperUUID:  "a39e5a8e-34cf-4b15-87bd-4b9cdaa59c42",
				types.LabelServiceUUID:  "dcb8e864-0a1f-4a67-b470-327ceb461b4e",
			}),
			wantAnnotations: types.MetricAnnotations{
				BleemeoAgentID: "c571f9cf-6f07-492a-9e86-b8d5f5027557",
			},
		},
		// when LabelMetaProbeScraperName is not provided, the 'scraper' label is the traditional 'instance' label  without port
		{
			name:   "blackbox_probe_icmp_no_scraper_name",
			fields: fields{relabelConfigs: getDefaultRelabelConfig()},
			args: args{map[string]string{
				types.LabelMetaBleemeoTargetAgent:     "icmp://8.8.8.8",
				types.LabelInstance:                   "super-instance:1111",
				types.LabelMetaBleemeoTargetAgentUUID: "c571f9cf-6f07-492a-9e86-b8d5f5027557",
				types.LabelMetaBleemeoUUID:            "a39e5a8e-34cf-4b15-87bd-4b9cdaa59c42",
				// necessary for some labels to be applied
				types.LabelMetaProbeServiceUUID: "dcb8e864-0a1f-4a67-b470-327ceb461b4e",
				types.LabelMetaSendScraperUUID:  "yes",
			}},
			want: labels.FromMap(map[string]string{
				types.LabelInstance:     "icmp://8.8.8.8",
				types.LabelScraper:      "super-instance",
				types.LabelInstanceUUID: "c571f9cf-6f07-492a-9e86-b8d5f5027557",
				types.LabelScraperUUID:  "a39e5a8e-34cf-4b15-87bd-4b9cdaa59c42",
				types.LabelServiceUUID:  "dcb8e864-0a1f-4a67-b470-327ceb461b4e",
			}),
			wantAnnotations: types.MetricAnnotations{
				BleemeoAgentID: "c571f9cf-6f07-492a-9e86-b8d5f5027557",
			},
		},
		{
			name:   "vSphere_realtime_gatherer",
			fields: fields{relabelConfigs: getDefaultRelabelConfig()},
			relabelHook: func(_ context.Context, labels map[string]string) (result map[string]string, retryLater bool) {
				labels["__meta_additional_info"] = "some value"

				return labels, false
			},
			args: args{map[string]string{
				types.LabelMetaGloutonFQDN:     "some-instance",
				types.LabelMetaGloutonPort:     "8015",
				types.LabelMetaPort:            "8015",
				types.LabelMetaSendScraperUUID: "yes",
			}},
			want: labels.FromMap(map[string]string{
				types.LabelInstance: "some-instance:8015",
			}),
			wantWithMeta: map[string]string{
				types.LabelInstance:            "some-instance:8015",
				types.LabelMetaGloutonFQDN:     "some-instance",
				types.LabelMetaGloutonPort:     "8015",
				types.LabelMetaPort:            "8015",
				types.LabelMetaSendScraperUUID: "yes",
				"__meta_additional_info":       "some value",
			},
			wantAnnotations: types.MetricAnnotations{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := New(Option{})
			if err != nil {
				t.Fatal(err)
			}

			r.relabelConfigs = tt.fields.relabelConfigs
			r.relabelHook = tt.relabelHook

			promLabels, labelsWithMeta, annotations, _ := r.applyRelabel(t.Context(), tt.args.input)
			if !reflect.DeepEqual(promLabels, tt.want) {
				t.Errorf("Registry.applyRelabel() promLabels = %+v, want %+v", promLabels, tt.want)
			}

			if tt.relabelHook != nil {
				// For our current use case, labels with meta are only useful in combination with the relabel-hook.
				if diff := cmp.Diff(tt.wantWithMeta, labelsWithMeta); diff != "" {
					t.Errorf("Registry.applyRelabel() labelsWithMeta (-want +got):\n%s", diff)
				}
			}

			if !reflect.DeepEqual(annotations, tt.wantAnnotations) {
				t.Errorf("Registry.applyRelabel() annotations = %+v, want %+v", annotations, tt.wantAnnotations)
			}
		})
	}
}

func BenchmarkRegistry_applyRelabel(b *testing.B) {
	cases := []struct {
		name   string
		labels map[string]string
	}{
		{
			name: "cpu_used",
			labels: map[string]string{
				types.LabelName: "cpu_used",
			},
		},
		{
			name: "disk_used_1",
			labels: map[string]string{
				types.LabelName: "disk_used",
				types.LabelItem: "/",
			},
		},
		{
			name: "disk_used_2",
			labels: map[string]string{
				types.LabelName:     "disk_used",
				types.LabelItem:     "/",
				types.LabelInstance: "localhost:8015",
			},
		},
		{
			name: "mysql",
			labels: map[string]string{
				types.LabelName:                "mysql_command_select",
				types.LabelMetaServiceName:     "mysql",
				types.LabelMetaServiceInstance: "mysql_1",
				types.LabelMetaContainerName:   "mysql_1",
				types.LabelMetaContainerID:     "1234",
				types.LabelMetaGloutonFQDN:     "hostname",
				types.LabelMetaGloutonPort:     "8015",
				types.LabelMetaServicePort:     "3306",
				types.LabelMetaPort:            "3306",
			},
		},
	}

	for _, tt := range cases {
		b.Run(tt.name, func(b *testing.B) {
			r, err := New(Option{})
			if err != nil {
				b.Fatal(err)
			}

			r.relabelConfigs = getDefaultRelabelConfig()

			b.ResetTimer()

			for b.Loop() {
				r.applyRelabel(b.Context(), tt.labels)
			}
		})
	}
}

// TestRegistry_slowGather test that Registry work "well" enough with very slow gatherer.
func TestRegistry_slowGather(t *testing.T) { //nolint:maintidx
	t.Parallel()

	var (
		l      sync.Mutex
		points []types.MetricPoint
	)

	reg, err := New(Option{
		PushPoint: pushFunction(func(_ context.Context, pts []types.MetricPoint) {
			l.Lock()
			points = append(points, pts...)
			l.Unlock()
		}),
		FQDN:             "example.com",
		GloutonPort:      "1234",
		Filter:           &fakeFilter{},
		ShutdownDeadline: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var registryWG sync.WaitGroup

	registryWG.Add(1)

	go func() {
		defer registryWG.Done()

		_ = reg.Run(ctx)
	}()

	gather1 := &fakeGatherer{name: "name1"}
	gather1.fillResponse()

	slowCtx, cancelSlow := context.WithCancel(t.Context())
	defer cancelSlow()

	gather2 := &fakeGatherer{name: "verySlow", hangCtx: slowCtx}
	gather2.fillResponse()

	grp, _ := errgroup.WithContext(t.Context())

	var (
		id1 int
		id2 int
	)

	grp.Go(func() error {
		var err error

		id1, err = reg.RegisterGatherer(RegistrationOption{}, gather1)
		if err != nil {
			return fmt.Errorf("gather1: %w", err)
		}

		return nil
	})

	grp.Go(func() error {
		var err error

		id2, err = reg.RegisterGatherer(RegistrationOption{}, gather2)
		if err != nil {
			return fmt.Errorf("gather1: %w", err)
		}

		return nil
	})

	grp.Go(func() error {
		reg.UpdateRegistrationHooks(func(_ context.Context, labels map[string]string) (newLabel map[string]string, retryLater bool) {
			labels[types.LabelMetaBleemeoUUID] = testAgentID

			return labels, false
		}, makeDelayHook(100*time.Millisecond))

		return nil
	})

	err = grp.Wait()
	if err != nil {
		t.Error(err)
	}

	waitPointAndGatherCall := func(t *testing.T, expectPoint bool, expectedName string) {
		t.Helper()

		// We don't know the schedule of scraper... wait until we see point from gather1.
		// Since scrapeLoopOffset(...) may return a time up to now+gloutonMinimalInterval, it may take a while.
		deadline := time.Now().Add(gloutonMinimalInterval).Truncate(gloutonMinimalInterval).Add(time.Second)

		l.Lock()
		defer l.Unlock()

		points = nil
		seenPoints := false

		for time.Now().Before(deadline) && !seenPoints {
			l.Unlock()
			time.Sleep(50 * time.Millisecond)
			l.Lock()

			for _, pts := range points {
				if pts.Labels[types.LabelName] == expectedName {
					seenPoints = true

					break
				}
			}
		}

		// Just wait a bit more, so that verySlow might send point, which is not expected
		l.Unlock()
		time.Sleep(reg.option.ShutdownDeadline)
		l.Lock()

		for _, pts := range points {
			if strings.HasPrefix(pts.Labels[types.LabelName], "verySlow") {
				t.Errorf("See points from verySlow gatherer !")
			}
		}

		if !seenPoints && expectPoint {
			t.Errorf("didn't see point from %s", expectedName)
		} else if seenPoints && !expectPoint {
			t.Errorf("see point from %s", expectedName)
		}
	}

	waitPointAndGatherCall(t, true, "name1")

	if count := gather1.CallCount(); count == 0 {
		t.Errorf("gather1 was never called")
	}

	if count := gather2.CallCount(); count == 0 {
		t.Errorf("gather2 was never called")
	}

	reg.UpdateRegistrationHooks(func(_ context.Context, labels map[string]string) (newLabel map[string]string, retryLater bool) {
		labels[types.LabelMetaBleemeoUUID] = testAgentID

		return labels, false
	}, makeDelayHook(50*time.Millisecond))

	waitPointAndGatherCall(t, true, "name1")

	grp.Go(func() error {
		reg.Unregister(id1)

		return nil
	})

	grp.Go(func() error {
		reg.Unregister(id2)

		return nil
	})

	_ = grp.Wait()

	waitPointAndGatherCall(t, false, "name1")

	gather1 = &fakeGatherer{name: "name2"}
	gather1.fillResponse()

	gather2 = &fakeGatherer{name: "verySlow2", hangCtx: slowCtx}
	gather2.fillResponse()

	grp.Go(func() error {
		var err error

		_, err = reg.RegisterGatherer(RegistrationOption{}, gather1)
		if err != nil {
			return fmt.Errorf("gather1: %w", err)
		}

		return nil
	})

	grp.Go(func() error {
		var err error

		_, err = reg.RegisterGatherer(RegistrationOption{}, gather2)
		if err != nil {
			return fmt.Errorf("gather1: %w", err)
		}

		return nil
	})

	err = grp.Wait()
	if err != nil {
		t.Error(err)
	}

	waitPointAndGatherCall(t, true, "name2")

	cancel()
	registryWG.Wait()

	waitPointAndGatherCall(t, false, "name2")

	// We will restart the full registry. This is normally not something we do. But for completeness let's do it.
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()

	registryWG.Add(1)

	go func() {
		defer registryWG.Done()

		_ = reg.Run(ctx)
	}()

	// During shutdow, all gatherer get Unregistered, we will need to re-register them.
	waitPointAndGatherCall(t, false, "name2")

	_, err = reg.RegisterGatherer(RegistrationOption{}, gather1)
	if err != nil {
		t.Error(err)
	}

	_, err = reg.RegisterGatherer(RegistrationOption{}, gather2)
	if err != nil {
		t.Error(err)
	}

	waitPointAndGatherCall(t, true, "name2")
}

func TestRegistry_run(t *testing.T) {
	t.Parallel()

	var (
		l      sync.Mutex
		t0     time.Time
		points []types.MetricPoint
	)

	reg, err := New(Option{
		PushPoint: pushFunction(func(_ context.Context, pts []types.MetricPoint) {
			l.Lock()
			points = append(points, pts...)
			l.Unlock()
		}),
		FQDN:        "example.com",
		GloutonPort: "1234",
		Filter:      &fakeFilter{},
	},
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go reg.Run(ctx) //nolint: errcheck

	const delay = 100 * time.Millisecond

	reg.UpdateRegistrationHooks(func(_ context.Context, labels map[string]string) (newLabel map[string]string, retryLater bool) {
		labels[types.LabelMetaBleemeoUUID] = testAgentID

		return labels, false
	}, makeDelayHook(delay))

	gather1 := &fakeGatherer{name: "name1"}
	gather1.fillResponse()

	gather2 := &fakeGatherer{name: "name2"}
	gather2.fillResponse()

	// Sleep until the next rounded 250 millisecond.
	// Then sleep another millisecond.
	// We do this because the 3 gatherer added below should start at the same time, so we
	// must ensure the first isn't registered just before a rounded 250ms and other are resgistered after.
	// If this occur, the first will run while the other aren't yet registered.
	time.Sleep(time.Until(time.Now().Truncate(delay).Add(delay).Add(time.Millisecond)))

	id1, err := reg.RegisterGatherer(RegistrationOption{}, gather1)
	if err != nil {
		t.Error(err)
	}

	id2, err := reg.RegisterGatherer(RegistrationOption{}, gather2)
	if err != nil {
		t.Error(err)
	}

	id3, err := reg.RegisterPushPointsCallback(RegistrationOption{HonorTimestamp: true}, func(_ context.Context, t time.Time) error {
		l.Lock()
		t0 = t     //nolint: wsl_v5
		l.Unlock() //nolint: wsl_v5

		reg.WithTTL(5*time.Minute).PushPoints(ctx, []types.MetricPoint{
			{Point: types.Point{Time: t, Value: 42.0}, Labels: map[string]string{"__name__": "push", "something": "value", types.LabelItem: "/home"}},
		})

		return nil
	})
	if err != nil {
		t.Error(err)
	}

	// We don't know the schedule of scraper... wait until we have the expected number of points.
	// Since scrapeLoopOffset(...) may return a time up to now+gloutonMinimalInterval, it may take a while.
	deadline := time.Now().Add(gloutonMinimalInterval).Truncate(gloutonMinimalInterval).Add(time.Second)

	l.Lock()

	for time.Now().Before(deadline) {
		l.Unlock()
		time.Sleep(10 * time.Millisecond)
		l.Lock()

		if len(points) >= 3 {
			break
		}
	}

	if len(points) == 0 {
		t.Log("breakpoint")
	}

	want := []types.MetricPoint{
		{Point: types.Point{Time: t0, Value: 42.0}, Labels: map[string]string{"__name__": "push", "item": "/home", "instance": "example.com:1234", "instance_uuid": testAgentID}},
		{Point: types.Point{Time: t0, Value: 1.0}, Labels: map[string]string{"__name__": "name1", "instance": "example.com:1234", "instance_uuid": testAgentID}},
		{Point: types.Point{Time: t0, Value: 1.0}, Labels: map[string]string{"__name__": "name2", "instance": "example.com:1234", "instance_uuid": testAgentID}},
	}

	pointLess := func(x, y types.MetricPoint) bool {
		return x.Labels[types.LabelName] < y.Labels[types.LabelName]
	}

	if diff := cmp.Diff(want, points, cmpopts.SortSlices(pointLess), cmpopts.EquateApproxTime(50*time.Millisecond)); diff != "" {
		t.Errorf("points mismatch (-want +got)\n%s", diff)
	}

	l.Unlock()

	reg.Unregister(id1)
	reg.Unregister(id2)
	reg.Unregister(id3)
}

type sourceKind string

const (
	kindPushPoint         sourceKind = "pushpoint"
	kindPushPointCallback sourceKind = "pushpointCallback"
	kindAppenderCallback  sourceKind = "appenderCallback"
	kindGatherer          sourceKind = "gatherer"
	kindInput             sourceKind = "input"
)

func registryRunOnce(t *testing.T, now time.Time, reg *Registry, kindToTest sourceKind, opt RegistrationOption, input []types.MetricPoint) error {
	t.Helper()

	switch kindToTest {
	case kindPushPoint:
		reg.WithTTL(5*time.Minute).PushPoints(t.Context(), input)
	case kindPushPointCallback:
		id, err := reg.registerPushPointsCallback(
			opt,
			func(c context.Context, _ time.Time) error {
				reg.WithTTL(5*time.Minute).PushPoints(c, input)

				return nil
			},
		)
		if err != nil {
			return err
		}

		reg.InternalRunScrape(t.Context(), t.Context(), now, id)
	case kindAppenderCallback:
		id, err := reg.RegisterAppenderCallback(
			opt,
			fakeAppenderCallback{
				input: input,
			},
		)
		if err != nil {
			return err
		}

		reg.InternalRunScrape(t.Context(), t.Context(), now, id)
	case kindGatherer:
		id, err := reg.RegisterGatherer(
			opt,
			&fakeGatherer{
				response: dropNilTime(model.MetricPointsToFamilies(input)),
			},
		)
		if err != nil {
			return err
		}

		reg.InternalRunScrape(t.Context(), t.Context(), now, id)
	case kindInput:
		id, err := reg.RegisterInput(
			opt,
			fakeInput{
				input: input,
			},
		)
		if err != nil {
			return err
		}

		reg.InternalRunScrape(t.Context(), t.Context(), now, id)
	default:
		t.Fatalf("unknown kind: %s", kindToTest)
	}

	return nil
}

func dropNilTime(in []*dto.MetricFamily) []*dto.MetricFamily {
	for _, mf := range in {
		for _, m := range mf.GetMetric() {
			if m.GetTimestampMs() == nilTime.UnixMilli() {
				m.TimestampMs = nil
			}
		}
	}

	return in
}

type metricPointTimeOverride struct {
	types.Point

	Labels       map[string]string
	Annotations  types.MetricAnnotations
	TimeOnGather time.Time
}

func TestRegistry_pointsAlteration(t *testing.T) { //nolint:maintidx
	now := time.Date(2021, 12, 7, 10, 11, 13, 0, time.UTC)

	tests := []struct {
		name       string
		input      []types.MetricPoint
		opt        RegistrationOption
		kindToTest sourceKind
		want       []metricPointTimeOverride
	}{
		{
			name:       "pushpoint-bleemeo",
			kindToTest: kindPushPoint,
			opt:        RegistrationOption{}, // unused for pushpoint
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "cpu_used",
					},
					Point: types.Point{
						Time:  now,
						Value: 12,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "/home",
					},
					Point: types.Point{
						Time:  now,
						Value: 0.1,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
						types.LabelItem: "/srv",
					},
					Point: types.Point{
						Time:  now,
						Value: 1.2,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:     "cpu_used",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 12,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used",
						types.LabelItem:     "/home",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 0.1,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used_perc",
						types.LabelItem:     "/srv",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 1.2,
					},
					TimeOnGather: now,
				},
			},
		},
		{
			name:       "pushpointCallback-bleemeo",
			kindToTest: kindPushPointCallback,
			opt: RegistrationOption{
				HonorTimestamp: true,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "cpu_used",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "/home",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
						types.LabelItem: "/srv",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:     "cpu_used",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used",
						types.LabelItem:     "/home",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used_perc",
						types.LabelItem:     "/srv",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
			},
		},
		{
			name:       "appender",
			kindToTest: kindAppenderCallback,
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "cpu_used",
						"anyOther":      "label",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "/home",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
						types.LabelItem: "/srv",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:     "cpu_used",
						"anyOther":          "label",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used",
						types.LabelItem:     "/home",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used_perc",
						types.LabelItem:     "/srv",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
			},
		},
		{
			name:       "gatherer-bleemeo",
			kindToTest: kindGatherer,
			opt: RegistrationOption{
				HonorTimestamp: true,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "cpu_used",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "/home",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
						types.LabelItem: "/srv",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:     "cpu_used",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used",
						types.LabelItem:     "/home",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used_perc",
						types.LabelItem:     "/srv",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
			},
		},
		{
			name:       "input",
			kindToTest: kindInput,
			opt: RegistrationOption{
				HonorTimestamp: true,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "cpu_used",
						"anyOther":      "label",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "/home",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used_perc",
						types.LabelItem: "/srv",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:     "cpu_used",
						"anyOther":          "label",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used",
						types.LabelItem:     "/home",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used_perc",
						types.LabelItem:     "/srv",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
			},
		},
		{
			name:       "gatherer-extralabels",
			kindToTest: kindGatherer,
			opt: RegistrationOption{
				ExtraLabels: map[string]string{
					types.LabelMetaSNMPTarget: "1.2.3.4:8080",
					"another":                 "value",
				},
				Rules:          DefaultSNMPRules(time.Minute),
				HonorTimestamp: true,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "ifOutBytes",
						"ifDesc":        "Some value",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:       "ifOutBytes",
						"ifDesc":              "Some value",
						types.LabelInstance:   "server.bleemeo.com:8016",
						"another":             "value",
						types.LabelSNMPTarget: "1.2.3.4:8080",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "1.2.3.4:8080",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
			},
		},
		{
			name:       "metric-rename-simple-gatherer",
			kindToTest: kindGatherer,
			opt: RegistrationOption{
				ExtraLabels: map[string]string{
					types.LabelMetaSNMPTarget: "192.168.1.2",
				},
				Rules:          DefaultSNMPRules(time.Minute),
				HonorTimestamp: true,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "hrProcessorLoad",
						"hrDeviceDescr": "CPU Pkg/ID/Node: 0/0/0 Intel Xeon E3-12xx v2 (Ivy Bridge, IBRS)",
						"hrDeviceIndex": "1",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:  "hrStorageUsed",
						"hrStorageDescr": "Real Memory",
					},
					Point: types.Point{
						Time:  now,
						Value: 8,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:  "hrStorageUsed",
						"hrStorageDescr": "Unreal Memory",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:  "hrStorageAllocationUnits",
						"hrStorageDescr": "Real Memory",
					},
					Point: types.Point{
						Time:  now,
						Value: 1024,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:  "hrStorageAllocationUnits",
						"hrStorageDescr": "Unreal Memory",
					},
					Point: types.Point{
						Time:  now,
						Value: 1,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:       "cpu_used",
						"core":                "1",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{ // This metric will be dropped by the metric filter.
					Labels: map[string]string{
						types.LabelName:       "hrProcessorLoad",
						"hrDeviceDescr":       "CPU Pkg/ID/Node: 0/0/0 Intel Xeon E3-12xx v2 (Ivy Bridge, IBRS)",
						"hrDeviceIndex":       "1",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{SNMPTarget: "192.168.1.2"},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "hrStorageAllocationUnits",
						"hrStorageDescr":      "Real Memory",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 1024,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "hrStorageAllocationUnits",
						"hrStorageDescr":      "Unreal Memory",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 1,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "hrStorageUsed",
						"hrStorageDescr":      "Real Memory",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 8,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "hrStorageUsed",
						"hrStorageDescr":      "Unreal Memory",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 8192,
					},
					TimeOnGather: now,
				},
			},
		},
		{
			name:       "metric-rename-simple-2",
			kindToTest: kindGatherer,
			opt: RegistrationOption{
				ExtraLabels: map[string]string{
					types.LabelMetaSNMPTarget: "192.168.1.2",
				},
				Rules:          DefaultSNMPRules(time.Minute),
				HonorTimestamp: true,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:    "cpmCPUTotal1minRev",
						"cpmCPUTotalIndex": "1",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:    "cpmCPUMemoryUsed",
						"cpmCPUTotalIndex": "42",
					},
					Point: types.Point{
						Time:  now,
						Value: 145,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:    "cpmCPUMemoryFree",
						"cpmCPUTotalIndex": "42",
					},
					Point: types.Point{
						Time:  now,
						Value: 7,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:       "cpu_used",
						"core":                "1",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "cpmCPUMemoryUsed",
						"cpmCPUTotalIndex":    "42",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 145,
					},
					TimeOnGather: now,
				},
				{ // This metric will be dropped by the metric filter.
					Labels: map[string]string{
						types.LabelName:       "cpmCPUTotal1minRev",
						"cpmCPUTotalIndex":    "1",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{SNMPTarget: "192.168.1.2"},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},

				{
					Labels: map[string]string{
						types.LabelName:       "cpmCPUMemoryFree",
						"cpmCPUTotalIndex":    "42",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 7,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_free",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 7168,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 148480,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used_perc",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 95.39,
					},
					TimeOnGather: now,
				},
			},
		},
		{
			name:       "metric-rename-multiple-1",
			kindToTest: kindGatherer,
			opt: RegistrationOption{
				ExtraLabels: map[string]string{
					types.LabelMetaSNMPTarget: "192.168.1.2",
				},
				Rules:          DefaultSNMPRules(time.Minute),
				HonorTimestamp: true,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolUsed",
						"ciscoMemoryPoolName": "Processor",
						"uniqueValue":         "1",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolUsed",
						"ciscoMemoryPoolName": "Processor",
						"uniqueValue":         "2",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
			},
			want: []metricPointTimeOverride{
				{ // This metric will be dropped by the metric filter.
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolUsed",
						"ciscoMemoryPoolName": "Processor",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "1",
					},
					Annotations: types.MetricAnnotations{SNMPTarget: "192.168.1.2"},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{ // This metric will be dropped by the metric filter.
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolUsed",
						"ciscoMemoryPoolName": "Processor",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "2",
					},
					Annotations: types.MetricAnnotations{SNMPTarget: "192.168.1.2"},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "1",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
			},
		},
		{
			name:       "metric-rename-multiple-2",
			kindToTest: kindGatherer,
			opt: RegistrationOption{
				ExtraLabels: map[string]string{
					types.LabelMetaSNMPTarget: "192.168.1.2",
				},
				Rules:          DefaultSNMPRules(time.Minute),
				HonorTimestamp: true,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolUsed",
						"ciscoMemoryPoolName": "System memory",
						"uniqueValue":         "1",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolFree",
						"ciscoMemoryPoolName": "System memory",
						"uniqueValue":         "1",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolUsed",
						"ciscoMemoryPoolName": "Anything Else",
						"uniqueValue":         "2",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolFree",
						"ciscoMemoryPoolName": "Processor",
						"uniqueValue":         "3",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolFree",
						"ciscoMemoryPoolName": "anything else",
						"uniqueValue":         "5",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:    "cpmCPUMemoryFree",
						"cpmCPUTotalIndex": "2021",
						"uniqueValue":      "6",
					},
					Point: types.Point{
						Time:  now,
						Value: 789,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:                     "ciscoEnvMonTemperatureStatusValue",
						"ciscoEnvMonTemperatureStatusDescr": "CPU",
						"uniqueValue":                       "7",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "rlCpuUtilDuringLastMinute",
						"uniqueValue":   "8",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:              "rlPhdUnitEnvParamTempSensorValue",
						"rlPhdUnitEnvParamStackUnit": "1",
						"uniqueValue":                "9",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
			},
			want: []metricPointTimeOverride{
				{ // This metric will be dropped by the metric filter.
					Labels: map[string]string{
						types.LabelName:                     "ciscoEnvMonTemperatureStatusValue",
						"ciscoEnvMonTemperatureStatusDescr": "CPU",
						types.LabelInstance:                 "server.bleemeo.com:8016",
						types.LabelSNMPTarget:               "192.168.1.2",
						"uniqueValue":                       "7",
					},
					Annotations: types.MetricAnnotations{SNMPTarget: "192.168.1.2"},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{ // This metric will be dropped by the metric filter.
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolFree",
						"ciscoMemoryPoolName": "Processor",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "3",
					},
					Annotations: types.MetricAnnotations{SNMPTarget: "192.168.1.2"},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{ // This metric will be dropped by the metric filter.
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolFree",
						"ciscoMemoryPoolName": "System memory",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "1",
					},
					Annotations: types.MetricAnnotations{SNMPTarget: "192.168.1.2"},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "1",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_free",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "1",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{ // This metric will be dropped by the metric filter.
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolUsed",
						"ciscoMemoryPoolName": "System memory",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "1",
					},
					Annotations: types.MetricAnnotations{SNMPTarget: "192.168.1.2"},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used_perc",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "1",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 50,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolUsed",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"ciscoMemoryPoolName": "Anything Else",
						"uniqueValue":         "2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_free",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "3",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{ // This metric will be dropped by the metric filter.
					Labels: map[string]string{
						types.LabelName:       "rlCpuUtilDuringLastMinute",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "8",
					},
					Annotations: types.MetricAnnotations{SNMPTarget: "192.168.1.2"},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{ // This metric will be dropped by the metric filter.
					Labels: map[string]string{
						types.LabelName:              "rlPhdUnitEnvParamTempSensorValue",
						types.LabelInstance:          "server.bleemeo.com:8016",
						"rlPhdUnitEnvParamStackUnit": "1",
						types.LabelSNMPTarget:        "192.168.1.2",
						"uniqueValue":                "9",
					},
					Annotations: types.MetricAnnotations{SNMPTarget: "192.168.1.2"},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "ciscoMemoryPoolFree",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"ciscoMemoryPoolName": "anything else",
						"uniqueValue":         "5",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "cpmCPUMemoryFree",
						"cpmCPUTotalIndex":    "2021",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "6",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 789,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_free",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "6",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 807936,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "temperature",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"sensor":              "CPU",
						"uniqueValue":         "7",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "cpu_used",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"uniqueValue":         "8",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "temperature",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
						"sensor":              "CPU",
						"uniqueValue":         "9",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
			},
		},
		{
			name:       "metric-rule-and-rename",
			kindToTest: kindGatherer,
			opt: RegistrationOption{
				ExtraLabels: map[string]string{
					types.LabelMetaSNMPTarget: "192.168.1.2",
				},
				Rules:          DefaultSNMPRules(time.Minute),
				HonorTimestamp: true,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:  "hrStorageUsed",
						"hrStorageDescr": "Real Memory",
						"hrStorageIndex": "6",
					},
					Point: types.Point{
						Time:  now,
						Value: 1.49028e+06,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:  "hrStorageAllocationUnits",
						"hrStorageDescr": "Real Memory",
						"hrStorageIndex": "6",
					},
					Point: types.Point{
						Time:  now,
						Value: 1024,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:  "hrStorageSize",
						"hrStorageDescr": "Real Memory",
						"hrStorageIndex": "6",
					},
					Point: types.Point{
						Time:  now,
						Value: 8.385008e+06,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:       "hrStorageAllocationUnits",
						"hrStorageDescr":      "Real Memory",
						"hrStorageIndex":      "6",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 1024.0,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "hrStorageSize",
						"hrStorageDescr":      "Real Memory",
						"hrStorageIndex":      "6",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 8.385008e+06,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "hrStorageUsed",
						"hrStorageDescr":      "Real Memory",
						"hrStorageIndex":      "6",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 1.49028e+06,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 1526046720.0,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_used_perc",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 17.77,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:       "mem_free",
						types.LabelInstance:   "server.bleemeo.com:8016",
						types.LabelSNMPTarget: "192.168.1.2",
					},
					Annotations: types.MetricAnnotations{
						SNMPTarget: "192.168.1.2",
					},
					Point: types.Point{
						Time:  now,
						Value: 7060201472.0,
					},
					TimeOnGather: now,
				},
			},
		},
		{
			name:       "gatherer-with-relabel",
			kindToTest: kindGatherer,
			opt: RegistrationOption{
				ApplyDynamicRelabel: true,
				HonorTimestamp:      true,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName:                       "hrStorageAllocationUnits",
						types.LabelMetaBleemeoTargetAgent:     "test-agent",
						types.LabelMetaBleemeoTargetAgentUUID: "test-uuid",
					},
					Point: types.Point{
						Time:  now,
						Value: 1024.0,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:         "hrStorageAllocationUnits",
						types.LabelInstance:     "test-agent",
						types.LabelInstanceUUID: "test-uuid",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: "test-uuid",
					},
					Point: types.Point{
						Time:  now,
						Value: 1024.0,
					},
					TimeOnGather: now,
				},
			},
		},
		{
			// Test output from our telegraf-input. input should match what plugin really send.
			name:       "telegraf-input",
			kindToTest: kindPushPointCallback,
			opt: RegistrationOption{
				HonorTimestamp: true,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "cpu_used",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						"item":          "/home",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "io_utilization",
						"item":          "sda",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName:              "container_cpu_used",
						types.LabelMetaContainerName: "myredis",
						types.LabelItem:              "myredis",
					},
					Annotations: types.MetricAnnotations{
						ContainerID: "1234",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:     "cpu_used",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "disk_used",
						types.LabelItem:     "/home",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "io_utilization",
						types.LabelItem:     "sda",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "container_cpu_used",
						types.LabelItem:     "myredis",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Annotations: types.MetricAnnotations{
						ContainerID: "1234",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
			},
		},
		{
			// Test output from miscAppender
			name:       "miscAppender",
			kindToTest: kindAppenderCallback,
			opt: RegistrationOption{
				ApplyDynamicRelabel: true,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "containers_count",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				// containerd runtime
				{
					Labels: map[string]string{
						types.LabelName:            "container_cpu_used",
						types.LabelItem:            "myredis",
						types.LabelMetaContainerID: "1234",
					},
					Annotations: types.MetricAnnotations{
						ContainerID: "1234",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:     "containers_count",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "container_cpu_used",
						types.LabelItem:     "myredis",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Annotations: types.MetricAnnotations{
						ContainerID: "1234",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
			},
		},
		{
			// Test output from node_exporter for /metrics (for example/prometheus)
			name:       "node_exporter",
			kindToTest: kindGatherer,
			opt: RegistrationOption{
				HonorTimestamp: true,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "node_cpu_seconds_total",
						"cpu":           "3",
						"mode":          "iowait",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "node_uname_info",
						"domainname":    "(none)",
						"machine":       "aarch64",
						"nodename":      "docker-desktop",
						"release":       "5.15.49-linuxkit",
						"sysname":       "Linux",
						"version":       "#1 SMP PREEMPT Tue Sep 13 07:51:32 UTC 2022",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:     "node_cpu_seconds_total",
						"cpu":               "3",
						"mode":              "iowait",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "node_uname_info",
						"domainname":        "(none)",
						"machine":           "aarch64",
						"nodename":          "docker-desktop",
						"release":           "5.15.49-linuxkit",
						"sysname":           "Linux",
						"version":           "#1 SMP PREEMPT Tue Sep 13 07:51:32 UTC 2022",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
			},
		},
		{
			name:       "appender-time",
			kindToTest: kindAppenderCallback,
			opt: RegistrationOption{
				HonorTimestamp: true,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "zero",
					},
					Point: types.Point{
						Time:  time.Time{},
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "epoc",
					},
					Point: types.Point{
						Time:  time.UnixMilli(0),
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "now",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "future",
					},
					Point: types.Point{
						Time:  now.Add(42 * time.Minute),
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "past",
					},
					Point: types.Point{
						Time:  now.Add(-42 * time.Minute),
						Value: 42,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "zero",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: time.Time{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "epoc",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: time.Time{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "now",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "future",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now.Add(42 * time.Minute),
						Value: 42,
					},
					TimeOnGather: now.Add(42 * time.Minute),
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "past",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now.Add(-42 * time.Minute),
						Value: 42,
					},
					TimeOnGather: now.Add(-42 * time.Minute),
				},
			},
		},
		{
			name:       "gatherer-bleemeo-time",
			kindToTest: kindGatherer,
			opt: RegistrationOption{
				HonorTimestamp: true,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "zero",
					},
					Point: types.Point{
						Time:  time.Time{},
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "epoc",
					},
					Point: types.Point{
						Time:  time.UnixMilli(0),
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "now",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "future",
					},
					Point: types.Point{
						Time:  now.Add(42 * time.Minute),
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "past",
					},
					Point: types.Point{
						Time:  now.Add(-42 * time.Minute),
						Value: 42,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "zero",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: time.Time{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "epoc",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: time.Time{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "now",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "future",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now.Add(42 * time.Minute),
						Value: 42,
					},
					TimeOnGather: now.Add(42 * time.Minute),
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "past",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now.Add(-42 * time.Minute),
						Value: 42,
					},
					TimeOnGather: now.Add(-42 * time.Minute),
				},
			},
		},
		{
			name:       "input-time",
			kindToTest: kindInput,
			opt: RegistrationOption{
				HonorTimestamp: true,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "zero",
					},
					Point: types.Point{
						Time:  time.Time{},
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "epoc",
					},
					Point: types.Point{
						Time:  time.UnixMilli(0),
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "now",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "future",
					},
					Point: types.Point{
						Time:  now.Add(42 * time.Minute),
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "past",
					},
					Point: types.Point{
						Time:  now.Add(-42 * time.Minute),
						Value: 42,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "zero",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: time.Time{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "epoc",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: time.Time{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "now",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "future",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now.Add(42 * time.Minute),
						Value: 42,
					},
					TimeOnGather: now.Add(42 * time.Minute),
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "past",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now.Add(-42 * time.Minute),
						Value: 42,
					},
					TimeOnGather: now.Add(-42 * time.Minute),
				},
			},
		},
		{
			name:       "appender-time-no-honor-timestamp",
			kindToTest: kindAppenderCallback,
			opt: RegistrationOption{
				HonorTimestamp: false,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "zero",
					},
					Point: types.Point{
						Time:  time.Time{},
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "epoc",
					},
					Point: types.Point{
						Time:  time.UnixMilli(0),
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "now",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "future",
					},
					Point: types.Point{
						Time:  now.Add(42 * time.Minute),
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "past",
					},
					Point: types.Point{
						Time:  now.Add(-42 * time.Minute),
						Value: 42,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "zero",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now, // a timestamp is used, because CallForMetricsEndpoint isn't used
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "epoc",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "now",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "future",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "past",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: now,
				},
			},
		},
		{
			name:       "gatherer-bleemeo-time-no-honor-timestamp",
			kindToTest: kindGatherer,
			opt: RegistrationOption{
				HonorTimestamp: false,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "zero",
					},
					Point: types.Point{
						Time:  time.Time{},
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "epoc",
					},
					Point: types.Point{
						Time:  time.UnixMilli(0),
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "now",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "future",
					},
					Point: types.Point{
						Time:  now.Add(42 * time.Minute),
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "past",
					},
					Point: types.Point{
						Time:  now.Add(-42 * time.Minute),
						Value: 42,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "zero",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: time.Time{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "epoc",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: time.Time{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "now",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: time.Time{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "future",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: time.Time{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "past",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: time.Time{},
				},
			},
		},
		{
			name:       "input-time-no-honor-timestamp",
			kindToTest: kindInput,
			opt: RegistrationOption{
				HonorTimestamp: false,
			},
			input: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "zero",
					},
					Point: types.Point{
						Time:  time.Time{},
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "epoc",
					},
					Point: types.Point{
						Time:  time.UnixMilli(0),
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "now",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "future",
					},
					Point: types.Point{
						Time:  now.Add(42 * time.Minute),
						Value: 42,
					},
				},
				{
					Labels: map[string]string{
						types.LabelName: "metric_time",
						types.LabelItem: "past",
					},
					Point: types.Point{
						Time:  now.Add(-42 * time.Minute),
						Value: 42,
					},
				},
			},
			want: []metricPointTimeOverride{
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "zero",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: time.Time{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "epoc",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: time.Time{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "now",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: time.Time{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "future",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: time.Time{},
				},
				{
					Labels: map[string]string{
						types.LabelName:     "metric_time",
						types.LabelItem:     "past",
						types.LabelInstance: "server.bleemeo.com:8016",
					},
					Point: types.Point{
						Time:  now,
						Value: 42,
					},
					TimeOnGather: time.Time{},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				l         sync.Mutex
				gotPoints []types.MetricPoint
			)

			reg, err := New(
				Option{
					PushPoint: pushFunction(func(_ context.Context, pts []types.MetricPoint) {
						l.Lock()
						gotPoints = append(gotPoints, pts...)
						l.Unlock()
					}),
					FQDN:        "server.bleemeo.com",
					GloutonPort: "8016",
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			input := copyPoints(tt.input)
			want := pointsWithOverrideToPoints(tt.want)

			if err := registryRunOnce(t, now, reg, tt.kindToTest, tt.opt, input); err != nil {
				t.Fatal(err)
			}

			gotPoints = sortMetricPoints(gotPoints)

			if diff := types.DiffMetricPoints(want, gotPoints, true); diff != "" {
				t.Errorf("gotPoints mismatch (-want +got):\n%s", diff)
			}

			got, err := reg.GatherWithState(t.Context(), GatherState{T0: now})
			if err != nil {
				t.Fatal(err)
			}

			wantMFs := pointsWithOverrideToMFS(tt.want)

			if diff := types.DiffMetricFamilies(wantMFs, got, true, false); diff != "" {
				t.Errorf("Gather mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWaitForSecrets(t *testing.T) {
	testCases := []struct {
		name     string
		gateSize int
		// map number of inputs -> number of secrets
		secretsDistribution map[int]int
		maxAllowedDuration  time.Duration
		shouldFail          bool
	}{
		{
			name:                "[5] 20*1",
			gateSize:            5,
			secretsDistribution: map[int]int{20: 1}, // 20 inputs of 1 secret each
			maxAllowedDuration:  500 * time.Millisecond,
		},
		{
			name:                "[5] 8*3",
			gateSize:            5,
			secretsDistribution: map[int]int{8: 3},
			maxAllowedDuration:  2 * time.Second,
		},
		{
			name:                "[5] 3*3+10*1",
			gateSize:            5,
			secretsDistribution: map[int]int{3: 3, 10: 1},
			maxAllowedDuration:  750 * time.Millisecond,
		},
		{
			name:                "[5] 3*3+10*10",
			gateSize:            5,
			secretsDistribution: map[int]int{3: 3, 10: 10},
			maxAllowedDuration:  2 * time.Second,
			shouldFail:          true,
		},
		{
			name:                "[1] 1*1342185292",
			gateSize:            1, // WaitForSecrets doesn't rely on the gate size, but on inputs.MaxParallelSecrets().
			secretsDistribution: map[int]int{1: 1342185292},
			maxAllowedDuration:  10 * time.Millisecond,
			shouldFail:          true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			secretsGate := gate.New(tc.gateSize)

			ctx, cancel := context.WithTimeout(t.Context(), tc.maxAllowedDuration)
			defer cancel()

			var (
				errGrp      errgroup.Group
				gatherCount atomic.Int64

				expectedGathers int
			)

			for inputsCount, secretsCount := range tc.secretsDistribution {
				expectedGathers += inputsCount

				for range inputsCount {
					errGrp.Go(func() error {
						releaseFn, err := WaitForSecrets(ctx, secretsGate, secretsCount)
						if err != nil {
							if tc.shouldFail {
								return err // but it's ok
							}

							return fmt.Errorf("%s: couldn't take the %d needed slots: %w", tc.name, secretsCount, err)
						}

						time.Sleep(100 * time.Millisecond) // Doing some time-consuming gathering
						gatherCount.Add(1)
						releaseFn()

						return nil
					})
				}
			}

			err := errGrp.Wait()
			if err != nil {
				if !tc.shouldFail {
					t.Fatalf("Failed: %v", err)
				}
			} else {
				if tc.shouldFail {
					t.Fatal("Should have failed to take all the wanted slots.")
				}

				if expectedGathers != int(gatherCount.Load()) {
					t.Fatalf("Expected %d gatherings to be done, but got %d.", expectedGathers, gatherCount.Load())
				}
			}
		})
	}
}

func copyPoints(in []types.MetricPoint) []types.MetricPoint {
	result := make([]types.MetricPoint, len(in))
	copy(result, in)

	return result
}

func pointsWithOverrideToPoints(in []metricPointTimeOverride) []types.MetricPoint {
	result := make([]types.MetricPoint, len(in))

	for idx, pts := range in {
		result[idx] = types.MetricPoint{
			Point:       pts.Point,
			Labels:      pts.Labels,
			Annotations: pts.Annotations,
		}
	}

	return result
}

func pointsWithOverrideToMFS(in []metricPointTimeOverride) []*dto.MetricFamily {
	result := make([]types.MetricPoint, len(in))

	for idx, pts := range in {
		pts.Point.Time = pts.TimeOnGather

		result[idx] = types.MetricPoint{
			Point:       pts.Point,
			Labels:      pts.Labels,
			Annotations: pts.Annotations,
		}
	}

	wantMFs := model.MetricPointsToFamilies(result)
	model.DropMetaLabelsFromFamilies(wantMFs)

	return wantMFs
}

func sortMetricPoints(points []types.MetricPoint) []types.MetricPoint {
	sort.SliceStable(points, func(i, j int) bool {
		nameA := points[i].Labels[types.LabelName]
		nameB := points[j].Labels[types.LabelName]

		return nameA < nameB
	})

	return points
}
