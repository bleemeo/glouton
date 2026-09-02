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

package api

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/bleemeo/glouton/store"
	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/util/annotations"
)

// closeRecordingQuerier is a storage.Querier that records whether Close was
// called on it. It stands in for the on-disk TSDB querier.
type closeRecordingQuerier struct {
	closed bool
}

func (c *closeRecordingQuerier) Select(context.Context, bool, *storage.SelectHints, ...*labels.Matcher) storage.SeriesSet {
	return storage.EmptySeriesSet()
}

func (c *closeRecordingQuerier) LabelValues(context.Context, string, *storage.LabelHints, ...*labels.Matcher) ([]string, annotations.Annotations, error) {
	return nil, nil, nil
}

func (c *closeRecordingQuerier) LabelNames(context.Context, *storage.LabelHints, ...*labels.Matcher) ([]string, annotations.Annotations, error) {
	return nil, nil, nil
}

func (c *closeRecordingQuerier) Close() error {
	c.closed = true

	return nil
}

type closeRecordingQueryable struct {
	querier *closeRecordingQuerier
}

func (c closeRecordingQueryable) Querier(int64, int64) (storage.Querier, error) {
	return c.querier, nil
}

// TestApiQuerierCloseClosesSecondary checks that closing the querier returned
// by apiQueryable.Querier also closes the wrapped secondary (TSDB) querier.
//
// Without this, every API query (the dashboard polls query_range continuously)
// leaks a TSDB reader: the head isolation state is never released, so
// Head.truncateMemory spins forever and db.Close() blocks at shutdown.
func TestApiQuerierCloseClosesSecondary(t *testing.T) {
	memStore := store.New("test", time.Hour, time.Hour)
	secondaryQuerier := &closeRecordingQuerier{}
	secondary := closeRecordingQueryable{querier: secondaryQuerier}

	queryable := NewQueryableWithSecondary(memStore, secondary, func() string { return "agent-1" })

	querier, err := queryable.Querier(0, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("Querier: %v", err)
	}

	if err := querier.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if !secondaryQuerier.closed {
		t.Fatal("secondary (TSDB) querier was not closed by apiQuerier.Close(): TSDB readers leak on every API query")
	}
}

// labelSourceQuerier is a storage.Querier returning a fixed set of label
// names/values, and recording the matchers it was called with. It stands in for
// the on-disk TSDB.
type labelSourceQuerier struct {
	names        []string
	values       []string
	seenMatchers []*labels.Matcher
}

func (q *labelSourceQuerier) Select(context.Context, bool, *storage.SelectHints, ...*labels.Matcher) storage.SeriesSet {
	return storage.EmptySeriesSet()
}

func (q *labelSourceQuerier) LabelValues(
	_ context.Context, _ string, _ *storage.LabelHints, matchers ...*labels.Matcher,
) ([]string, annotations.Annotations, error) {
	q.seenMatchers = matchers

	return q.values, nil, nil
}

func (q *labelSourceQuerier) LabelNames(
	_ context.Context, _ *storage.LabelHints, matchers ...*labels.Matcher,
) ([]string, annotations.Annotations, error) {
	q.seenMatchers = matchers

	return q.names, nil, nil
}

func (q *labelSourceQuerier) Close() error {
	return nil
}

type labelSourceQueryable struct {
	querier *labelSourceQuerier
}

func (c labelSourceQueryable) Querier(int64, int64) (storage.Querier, error) {
	return c.querier, nil
}

// TestApiQuerierLabelsAreAgentScoped checks that the label endpoints only see the
// main agent's metrics: the local API must never leak the metrics of the
// monitored SNMP targets, containers of other agents, or of monitors.
func TestApiQuerierLabelsAreAgentScoped(t *testing.T) {
	now := time.Now()
	memStore := store.New("test", time.Hour, time.Hour)

	memStore.PushPoints(context.Background(), []types.MetricPoint{
		{
			Point: types.Point{Time: now, Value: 42},
			Labels: map[string]string{
				types.LabelName:         "cpu_used",
				types.LabelInstanceUUID: "agent-1",
			},
		},
		{
			Point: types.Point{Time: now, Value: 1},
			Labels: map[string]string{
				types.LabelName:         "snmp_metric",
				types.LabelInstanceUUID: "agent-2",
			},
		},
	})

	queryable := NewQueryable(memStore, func() string { return "agent-1" })

	querier, err := queryable.Querier(now.Add(-time.Hour).UnixMilli(), now.UnixMilli())
	if err != nil {
		t.Fatalf("Querier: %v", err)
	}

	defer querier.Close()

	values, _, err := querier.LabelValues(context.Background(), types.LabelName, nil)
	if err != nil {
		t.Fatalf("LabelValues: %v", err)
	}

	if len(values) != 1 || values[0] != "cpu_used" {
		t.Errorf("LabelValues(__name__) = %v, want [cpu_used] — another agent's metrics leaked", values)
	}

	names, _, err := querier.LabelNames(context.Background(), nil)
	if err != nil {
		t.Fatalf("LabelNames: %v", err)
	}

	wantNames := []string{types.LabelName, types.LabelInstanceUUID}
	sort.Strings(wantNames)

	if !reflect.DeepEqual(names, wantNames) {
		t.Errorf("LabelNames() = %v, want %v", names, wantNames)
	}
}

// TestApiQuerierLabelsUseSecondary checks that the label endpoints also see the
// on-disk TSDB when it is enabled, and that the agent matcher is passed down to
// it — the in-memory store only keeps the last few minutes, so the label
// endpoints would be nearly empty without the TSDB.
func TestApiQuerierLabelsUseSecondary(t *testing.T) {
	memStore := store.New("test", time.Hour, time.Hour)
	secondaryQuerier := &labelSourceQuerier{
		names:  []string{"__name__", "item"},
		values: []string{"from_tsdb"},
	}

	queryable := NewQueryableWithSecondary(
		memStore,
		labelSourceQueryable{querier: secondaryQuerier},
		func() string { return "agent-1" },
	)

	querier, err := queryable.Querier(0, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("Querier: %v", err)
	}

	defer querier.Close()

	values, _, err := querier.LabelValues(context.Background(), "__name__", nil)
	if err != nil {
		t.Fatalf("LabelValues: %v", err)
	}

	if !reflect.DeepEqual(values, []string{"from_tsdb"}) {
		t.Errorf("LabelValues(__name__) = %v, want [from_tsdb] — the TSDB was not queried", values)
	}

	var found bool

	for _, matcher := range secondaryQuerier.seenMatchers {
		if matcher.Name == types.LabelInstanceUUID && matcher.Value == "agent-1" {
			found = true
		}
	}

	if !found {
		t.Errorf("matchers passed to the TSDB = %v, want one on %s", secondaryQuerier.seenMatchers, types.LabelInstanceUUID)
	}

	names, _, err := querier.LabelNames(context.Background(), nil)
	if err != nil {
		t.Fatalf("LabelNames: %v", err)
	}

	if !reflect.DeepEqual(names, []string{"__name__", "item"}) {
		t.Errorf("LabelNames() = %v, want [__name__ item]", names)
	}
}
