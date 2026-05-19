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

package api

import (
	"context"
	"errors"

	"github.com/bleemeo/glouton/store"
	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/util/annotations"
)

var errNotImplemented = errors.New("not implemented")

type MetricQueryable interface {
	storage.Queryable
	Metrics(filters map[string]string) (result []types.Metric, err error)
}

type apiQueryable struct {
	store       *store.Store
	secondary   storage.Queryable
	agentIDFunc func() string
	agentID     string
}

// NewQueryable returns a MetricQueryable that only do queries on the main agent.
func NewQueryable(store *store.Store, agentIDFunc func() string) MetricQueryable {
	return NewQueryableWithSecondary(store, nil, agentIDFunc)
}

// NewQueryableWithSecondary is like NewQueryable but also queries an
// additional storage.Queryable (typically the on-disk TSDB) and merges
// the results. The secondary may be nil.
func NewQueryableWithSecondary(store *store.Store, secondary storage.Queryable, agentIDFunc func() string) MetricQueryable {
	// We have a function to get the agent ID and not directly the agent ID because
	// the agent might not be registered yet.
	return apiQueryable{
		store:       store,
		secondary:   secondary,
		agentIDFunc: agentIDFunc,
	}
}

// Metrics return a list of Metric matching given labels filter.
func (q apiQueryable) Metrics(filters map[string]string) (result []types.Metric, err error) {
	if q.agentID == "" {
		q.agentID = q.agentIDFunc()
	}

	// Keep only metrics from the main agent.
	if q.agentID != "" {
		filters[types.LabelInstanceUUID] = q.agentID
	}

	return q.store.Metrics(filters)
}

// Querier returns a new Querier on the storage.
func (q apiQueryable) Querier(mint, maxt int64) (storage.Querier, error) {
	primary, err := q.store.Querier(mint, maxt)
	if err != nil {
		return nil, err
	}

	if q.agentID == "" {
		q.agentID = q.agentIDFunc()
	}

	inner := primary

	if q.secondary != nil {
		secondary, err := q.secondary.Querier(mint, maxt)
		if err != nil {
			_ = primary.Close()

			return nil, err
		}

		inner = storage.NewMergeQuerier(
			[]storage.Querier{primary},
			[]storage.Querier{secondary},
			storage.ChainedSeriesMerge,
		)
	}

	return apiQuerier{
		querier: inner,
		agentID: q.agentID,
	}, nil
}

type apiQuerier struct {
	querier storage.Querier
	agentID string
}

// Select returns a set of series that matches the given label matchers.
// A matcher is added to match only the main agent.
func (q apiQuerier) Select(ctx context.Context, sortSeries bool, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	agentMatcher, err := labels.NewMatcher(labels.MatchEqual, types.LabelInstanceUUID, q.agentID)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	matchers = append(matchers, agentMatcher)

	return q.querier.Select(ctx, sortSeries, hints, matchers...)
}

// Close releases the resources of the Querier.
func (q apiQuerier) Close() error {
	return nil
}

// LabelValues is not implemented.
func (q apiQuerier) LabelValues(_ context.Context, name string, hints *storage.LabelHints, matchers ...*labels.Matcher) ([]string, annotations.Annotations, error) {
	_ = name
	_ = hints
	_ = matchers

	return nil, nil, errNotImplemented
}

// LabelNames is not implemented.
func (q apiQuerier) LabelNames(_ context.Context, hints *storage.LabelHints, matchers ...*labels.Matcher) ([]string, annotations.Annotations, error) {
	_ = hints
	_ = matchers

	return nil, nil, errNotImplemented
}
