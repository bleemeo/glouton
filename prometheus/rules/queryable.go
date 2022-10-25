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

package rules

import (
	"context"
	"sync/atomic"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
)

type countingQueryable struct {
	queryable storage.Queryable
	count     int32
}

type filteringQueryable struct {
	queryable     storage.Queryable
	forcedMatcher []*labels.Matcher
}

func newCoutingQueryable(q storage.Queryable) *countingQueryable {
	return &countingQueryable{
		queryable: q,
	}
}

func newFilterQueryable(q storage.Queryable, lbls map[string]string) (*filteringQueryable, error) {
	focedMatcher := make([]*labels.Matcher, 0, len(lbls))

	for k, v := range lbls {
		m, err := labels.NewMatcher(labels.MatchEqual, k, v)
		if err != nil {
			return nil, err
		}

		focedMatcher = append(focedMatcher, m)
	}

	return &filteringQueryable{
		queryable:     q,
		forcedMatcher: focedMatcher,
	}, nil
}

func (q *countingQueryable) Querier(ctx context.Context, mint, maxt int64) (storage.Querier, error) {
	result, err := q.queryable.Querier(ctx, mint, maxt)
	if err != nil {
		return nil, err
	}

	return countingQuerier{
		Querier: result,
		count:   &q.count,
	}, nil
}

func (q *countingQueryable) Count() int32 {
	return atomic.LoadInt32(&q.count)
}

type countingQuerier struct {
	storage.Querier
	count *int32
}

func (q countingQuerier) Select(sortSeries bool, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	result := q.Querier.Select(sortSeries, hints, matchers...)

	return wrapSeriesSet{
		SeriesSet: result,
		count:     q.count,
	}
}

type wrapSeriesSet struct {
	storage.SeriesSet
	count *int32
}

func (s wrapSeriesSet) At() storage.Series {
	result := s.SeriesSet.At()

	return wrapSeries{
		Series: result,
		count:  s.count,
	}
}

type wrapSeries struct {
	storage.Series
	count *int32
}

func (s wrapSeries) Iterator() chunkenc.Iterator {
	result := s.Series.Iterator()

	return wrapIterator{
		Iterator: result,
		count:    s.count,
	}
}

type wrapIterator struct {
	chunkenc.Iterator
	count *int32
}

func (i wrapIterator) At() (int64, float64) {
	atomic.AddInt32(i.count, 1)

	return i.Iterator.At()
}

func (q *filteringQueryable) Querier(ctx context.Context, mint, maxt int64) (storage.Querier, error) {
	result, err := q.queryable.Querier(ctx, mint, maxt)
	if err != nil {
		return nil, err
	}

	return filteringQuerier{
		Querier:       result,
		forcedMatcher: q.forcedMatcher,
	}, nil
}

type filteringQuerier struct {
	storage.Querier
	forcedMatcher []*labels.Matcher
}

func (q filteringQuerier) Select(sortSeries bool, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	matchers = append(matchers, q.forcedMatcher...)
	result := q.Querier.Select(sortSeries, hints, matchers...)

	return result
}
