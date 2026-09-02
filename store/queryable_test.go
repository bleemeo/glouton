// Copyright 2015-2026 Bleemeo
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

package store

import (
	"context"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/prometheus/model/labels"
)

const testLabelInstance = "instance"

// labelsTestStore returns a store holding four series, all with one point at the
// given time:
//
//	cpu_used{instance="host1"}
//	disk_used{instance="host1",item="/home"}
//	disk_used{instance="host1",item="/srv"}
//	disk_used{instance="host2",item="/home"}
func labelsTestStore(t *testing.T, ts time.Time) *Store {
	t.Helper()

	db := New("test", time.Hour, time.Hour)

	db.PushPoints(context.Background(), []types.MetricPoint{
		{
			Point:  types.Point{Time: ts, Value: 42},
			Labels: map[string]string{types.LabelName: testCPUUsed, testLabelInstance: "host1"},
		},
		{
			Point:  types.Point{Time: ts, Value: 10},
			Labels: map[string]string{types.LabelName: testDiskUsed, testLabelInstance: "host1", testLabelItem: testItemHome},
		},
		{
			Point:  types.Point{Time: ts, Value: 20},
			Labels: map[string]string{types.LabelName: testDiskUsed, testLabelInstance: "host1", testLabelItem: testItemSrv},
		},
		{
			Point:  types.Point{Time: ts, Value: 30},
			Labels: map[string]string{types.LabelName: testDiskUsed, testLabelInstance: "host2", testLabelItem: testItemHome},
		},
	})

	return db
}

func mustMatcher(t *testing.T, matchType labels.MatchType, name string, value string) *labels.Matcher {
	t.Helper()

	matcher, err := labels.NewMatcher(matchType, name, value)
	if err != nil {
		t.Fatalf("NewMatcher(%s, %s, %s): %v", matchType, name, value, err)
	}

	return matcher
}

func TestQuerierLabelNames(t *testing.T) {
	now := time.Now()
	db := labelsTestStore(t, now)

	cases := []struct {
		name     string
		mint     time.Time
		maxt     time.Time
		matchers []*labels.Matcher
		want     []string
	}{
		{
			name: "no matcher",
			mint: now.Add(-time.Hour),
			maxt: now,
			want: []string{types.LabelName, testLabelInstance, testLabelItem},
		},
		{
			name:     "restricted to a metric without item",
			mint:     now.Add(-time.Hour),
			maxt:     now,
			matchers: []*labels.Matcher{mustMatcher(t, labels.MatchEqual, types.LabelName, testCPUUsed)},
			want:     []string{types.LabelName, testLabelInstance},
		},
		{
			name:     "regexp matcher",
			mint:     now.Add(-time.Hour),
			maxt:     now,
			matchers: []*labels.Matcher{mustMatcher(t, labels.MatchRegexp, testLabelItem, "/s.*")},
			want:     []string{types.LabelName, testLabelInstance, testLabelItem},
		},
		{
			name:     "matcher selecting nothing",
			mint:     now.Add(-time.Hour),
			maxt:     now,
			matchers: []*labels.Matcher{mustMatcher(t, labels.MatchEqual, types.LabelName, "unknown")},
			want:     []string{},
		},
		{
			name: "time range before the points",
			mint: now.Add(-2 * time.Hour),
			maxt: now.Add(-time.Hour),
			want: []string{},
		},
		{
			name: "time range after the points",
			mint: now.Add(time.Hour),
			maxt: now.Add(2 * time.Hour),
			want: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			querier, err := db.Querier(tc.mint.UnixMilli(), tc.maxt.UnixMilli())
			if err != nil {
				t.Fatalf("Querier: %v", err)
			}

			defer querier.Close()

			got, warnings, err := querier.LabelNames(context.Background(), nil, tc.matchers...)
			if err != nil {
				t.Fatalf("LabelNames: %v", err)
			}

			if len(warnings) != 0 {
				t.Errorf("unexpected warnings: %v", warnings)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("label names mismatch: (-want +got)\n%s", diff)
			}
		})
	}
}

func TestQuerierLabelValues(t *testing.T) {
	now := time.Now()
	db := labelsTestStore(t, now)

	cases := []struct {
		name     string
		label    string
		matchers []*labels.Matcher
		want     []string
	}{
		{
			name:  "metric names",
			label: types.LabelName,
			want:  []string{testCPUUsed, testDiskUsed},
		},
		{
			name:  "duplicate values are collapsed and sorted",
			label: testLabelItem,
			want:  []string{testItemHome, testItemSrv},
		},
		{
			name:  "instances",
			label: testLabelInstance,
			want:  []string{"host1", "host2"},
		},
		{
			name:     "restricted by matcher",
			label:    testLabelItem,
			matchers: []*labels.Matcher{mustMatcher(t, labels.MatchEqual, testLabelInstance, "host2")},
			want:     []string{testItemHome},
		},
		{
			name:     "label absent from the selected series",
			label:    testLabelItem,
			matchers: []*labels.Matcher{mustMatcher(t, labels.MatchEqual, types.LabelName, testCPUUsed)},
			want:     []string{},
		},
		{
			name:  "unknown label name",
			label: "no_such_label",
			want:  []string{},
		},
	}

	querier, err := db.Querier(now.Add(-time.Hour).UnixMilli(), now.UnixMilli())
	if err != nil {
		t.Fatalf("Querier: %v", err)
	}

	defer querier.Close()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := querier.LabelValues(context.Background(), tc.label, nil, tc.matchers...)
			if err != nil {
				t.Fatalf("LabelValues: %v", err)
			}

			if len(warnings) != 0 {
				t.Errorf("unexpected warnings: %v", warnings)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("label values mismatch: (-want +got)\n%s", diff)
			}
		})
	}
}

// TestQuerierLabelsIgnoreDeletedMetrics checks that a metric whose points were
// dropped no longer shows up in the label endpoints, which must stay consistent
// with what Select() returns.
func TestQuerierLabelsIgnoreDeletedMetrics(t *testing.T) {
	now := time.Now()
	db := labelsTestStore(t, now)

	db.DropMetrics([]map[string]string{
		{types.LabelName: testCPUUsed, testLabelInstance: "host1"},
	})

	querier, err := db.Querier(now.Add(-time.Hour).UnixMilli(), now.UnixMilli())
	if err != nil {
		t.Fatalf("Querier: %v", err)
	}

	defer querier.Close()

	got, _, err := querier.LabelValues(context.Background(), types.LabelName, nil)
	if err != nil {
		t.Fatalf("LabelValues: %v", err)
	}

	if diff := cmp.Diff([]string{testDiskUsed}, got); diff != "" {
		t.Errorf("label values mismatch: (-want +got)\n%s", diff)
	}
}
