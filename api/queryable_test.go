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
	"testing"
	"time"

	"github.com/bleemeo/glouton/store"

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
