package api

import (
	"context"
	"errors"
	"glouton/store"
	"glouton/types"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
)

var errNotImplemented = errors.New("not implemented")

type metricQueryable interface {
	storage.Queryable
	Metrics(filters map[string]string) (result []types.Metric, err error)
}

type apiQueryable struct {
	store       *store.Store
	agentIDFunc func() string
	agentID     string
}

// NewQueryable returns a metricQueryable that only do queries on the main agent.
func NewQueryable(store *store.Store, agentIDFunc func() string) metricQueryable {
	// We have a function to get the agent ID and not directly the agent ID because
	// the agent might not be registered yet.
	q := apiQueryable{
		store:       store,
		agentIDFunc: agentIDFunc,
	}

	return q
}

// Metrics return a list of Metric matching given labels filter.
func (q apiQueryable) Metrics(filters map[string]string) (result []types.Metric, err error) {
	if q.agentID == "" {
		q.agentID = q.agentIDFunc()
	}

	filters[types.LabelInstanceUUID] = q.agentID

	return q.store.Metrics(filters)
}

// Querier returns a new Querier on the storage.
func (q apiQueryable) Querier(ctx context.Context, mint, maxt int64) (storage.Querier, error) {
	querier, err := q.store.Querier(ctx, mint, maxt)
	if err != nil {
		return nil, err
	}

	if q.agentID == "" {
		q.agentID = q.agentIDFunc()
	}

	querierWrapper := apiQuerier{
		querier: querier,
		agentID: q.agentID,
	}

	return querierWrapper, nil
}

type apiQuerier struct {
	querier storage.Querier
	agentID string
}

// Select returns a set of series that matches the given label matchers.
// A matcher is added to match only the main agent.
func (q apiQuerier) Select(sortSeries bool, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	agentMatcher, err := labels.NewMatcher(labels.MatchEqual, types.LabelInstanceUUID, q.agentID)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	matchers = append(matchers, agentMatcher)

	return q.querier.Select(sortSeries, hints, matchers...)
}

// Close releases the resources of the Querier.
func (q apiQuerier) Close() error {
	return nil
}

// LabelValues is not implemented.
func (q apiQuerier) LabelValues(name string, matchers ...*labels.Matcher) ([]string, storage.Warnings, error) {
	return nil, nil, errNotImplemented
}

// LabelNames is not implemented.
func (q apiQuerier) LabelNames(matchers ...*labels.Matcher) ([]string, storage.Warnings, error) {
	return nil, nil, errNotImplemented
}
