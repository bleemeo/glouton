package store

import (
	"context"
	"errors"
	"glouton/types"
	"time"

	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
)

var errNotImplemented = errors.New("not implemented")

// Querier returns a storage.Querier to read from memory store.
func (s *Store) Querier(ctx context.Context, mint, maxt int64) (storage.Querier, error) {
	return querier{store: s, mint: mint, maxt: maxt}, nil
}

type querier struct {
	store *Store
	mint  int64
	maxt  int64
}

// Select returns a set of series that matches the given label matchers.
// Caller can specify if it requires returned series to be sorted. Prefer not requiring sorting for better performance.
// It allows passing hints that can help in optimising select, but it's up to implementation how this is used if used at all.
func (q querier) Select(sortSeries bool, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	q.store.lock.Lock()
	defer q.store.lock.Unlock()

	mint := time.Unix(0, q.mint*1e6)
	maxt := time.Unix(0, q.maxt*1e6)

	// TODO: sortSeries is not implemented
	metrics := make([]metric, 0)

outerLoop:
	for _, metric := range q.store.metrics {
		for _, matcher := range matchers {
			if !matcher.Matches(metric.labels[matcher.Name]) {
				continue outerLoop
			}
		}

		metrics = append(metrics, metric)
	}

	return &seriesIter{store: q.store, metrics: metrics, mint: mint, maxt: maxt}
}

// LabelValues returns all potential values for a label name.
// It is not safe to use the strings beyond the lifefime of the querier.
func (q querier) LabelValues(name string) ([]string, storage.Warnings, error) {
	return nil, nil, errNotImplemented
}

// LabelNames returns all the unique label names present in the block in sorted order.
func (q querier) LabelNames() ([]string, storage.Warnings, error) {
	return nil, nil, errNotImplemented
}

// Close releases the resources of the Querier.
func (q querier) Close() error {
	return nil
}

type seriesIter struct {
	store   *Store
	metrics []metric
	current series
	mint    time.Time
	maxt    time.Time
	err     error
}

func (i *seriesIter) Next() bool {
	for len(i.metrics) > 0 {
		metric := i.metrics[0]
		i.metrics = i.metrics[1:]

		points, err := metric.Points(i.mint, i.maxt)
		if err != nil && !errors.Is(err, errDeletedMetric) {
			i.err = err
			return false
		}

		if len(points) > 0 {
			i.current = series{
				labels: metric.labels,
				data:   points,
			}

			return true
		}
	}

	return false
}

func (i *seriesIter) At() storage.Series {
	return i.current
}

func (i *seriesIter) Err() error {
	return i.err
}

func (i *seriesIter) Warnings() storage.Warnings {
	return nil
}

type series struct {
	labels map[string]string
	data   []types.Point
}

func (s series) Labels() labels.Labels {
	return labels.FromMap(s.labels)
}

func (s series) Iterator() chunkenc.Iterator {
	return &seriesSample{
		data:   s.data,
		offset: -1,
	}
}

type seriesSample struct {
	data   []types.Point
	offset int
}

// Next advances the iterator by one.
func (s *seriesSample) Next() bool {
	if s.offset+1 >= len(s.data) {
		return false
	}

	s.offset++

	return true
}

// Seek advances the iterator forward to the first sample with the timestamp equal or greater than t.
// If current sample found by previous `Next` or `Seek` operation already has this property, Seek has no effect.
// Seek returns true, if such sample exists, false otherwise.
// Iterator is exhausted when the Seek returns false.
func (s *seriesSample) Seek(t int64) bool {
	for ; s.offset < len(s.data); s.offset++ {
		if s.data[s.offset].Time.UnixNano()/1e6 >= t {
			return true
		}
	}

	s.offset = len(s.data) - 1

	return false
}

// At returns the current timestamp/value pair.
// Before the iterator has advanced At behaviour is unspecified.
func (s *seriesSample) At() (int64, float64) {
	return s.data[s.offset].Time.UnixNano() / 1e6, s.data[s.offset].Value
}

// Err returns the current error. It should be used only after iterator is
// exhausted, that is `Next` or `Seek` returns false.
func (s *seriesSample) Err() error {
	return nil
}
