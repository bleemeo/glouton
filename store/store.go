// Package store implement a Metric/MetricPoint store.
//
// currently the storage in only in-memory and not persisted.
package store

import (
	"context"
	"reflect"
	"sync"
	"time"

	"agentgo/logger"
	"agentgo/types"
)

// Store implement an interface to retrieve metrics and metric points.
//
// See methods GetMetrics and GetMetricPoints
type Store struct {
	metrics map[int]metric
	points  map[int][]types.PointStatus
	lock    sync.Mutex
}

// New create a return a store. Store should be Close()d before leaving
func New() *Store {
	s := &Store{
		metrics: make(map[int]metric),
		points:  make(map[int][]types.PointStatus),
	}
	return s
}

// Run will run the store until context is cancelled
func (s *Store) Run(ctx context.Context) error {
	for {
		s.run()
		select {
		case <-time.After(300 * time.Second):
		case <-ctx.Done():
			return nil
		}
	}
}

// Close closes the store. Currently noop, but may write to persistence later
func (s *Store) Close() error {
	logger.V(2).Printf("Store closed")
	return nil
}

// DropMetrics delete metrics and they points.
// The provided labels list is an exact match (e.g. {"__name__": "disk_used"} won't delete the metrics for all disk. You need to specify all labels)
func (s *Store) DropMetrics(labelsList []map[string]string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	for i, m := range s.metrics {
		for _, l := range labelsList {
			if reflect.DeepEqual(m.labels, l) {
				delete(s.metrics, i)
				delete(s.points, i)
			}
		}
	}
}

// Metrics return a list of Metric matching given labels filter
func (s *Store) Metrics(filters map[string]string) (result []types.Metric, err error) {
	result = make([]types.Metric, 0)
	s.lock.Lock()
	defer s.lock.Unlock()
	for _, m := range s.metrics {
		if labelsMatch(m.labels, filters, false) {
			result = append(result, m)
		}
	}
	return
}

// Labels returns all label of the metric
func (m metric) Labels() map[string]string {
	labels := make(map[string]string)
	for k, v := range m.labels {
		labels[k] = v
	}
	return labels
}

// Points returns points between the two given time range (boundary are included).
func (m metric) Points(start, end time.Time) (result []types.PointStatus, err error) {
	m.store.lock.Lock()
	defer m.store.lock.Unlock()
	points := m.store.points[m.metricID]
	result = make([]types.PointStatus, 0)
	for _, point := range points {
		pointTimeUTC := point.Time.UTC()
		if !pointTimeUTC.Before(start) && !pointTimeUTC.After(end) {
			result = append(result, point)
		}
	}
	return
}

type metric struct {
	labels   map[string]string
	statusOf int
	store    *Store
	metricID int
}

// Return true if filter match given labels
func labelsMatch(labels, filter map[string]string, exact bool) bool {
	if exact && len(labels) != len(filter) {
		return false
	}
	for k, v := range filter {
		if v2, ok := labels[k]; !ok || v2 != v {
			return false
		}
	}
	return true
}

func (s *Store) run() {
	s.lock.Lock()
	defer s.lock.Unlock()
	deletedPoints := 0
	totalPoints := 0
	metricToDelete := make([]int, 0)
	for metricID := range s.metrics {
		points := s.points[metricID]
		newPoints := make([]types.PointStatus, 0)
		for _, p := range points {
			if time.Since(p.Time) < time.Hour {
				newPoints = append(newPoints, p)
			}
		}
		if len(newPoints) == 0 {
			metricToDelete = append(metricToDelete, metricID)
		} else {
			s.points[metricID] = newPoints
		}
		totalPoints += len(newPoints)
		deletedPoints += len(points) - len(newPoints)
	}
	for _, metricID := range metricToDelete {
		delete(s.metrics, metricID)
		delete(s.points, metricID)
	}
	logger.V(1).Printf("deleted %d points. Total point: %d", deletedPoints, totalPoints)
}

// metricGetOrCreate will return the metric that exactly match given labels.
//
// If the metric does not exists, it's created.
// statusOf is only used at creation.
// The store lock is assumed to be held.
func (s *Store) metricGetOrCreate(labels map[string]string, statusOf int) metric {
	for _, m := range s.metrics {
		if labelsMatch(m.labels, labels, true) {
			return m
		}
	}
	newID := 1
	_, ok := s.metrics[newID]
	for ok {
		newID++
		if newID == 0 {
			panic("too many metric in the store. Unable to find new slot")
		}
		_, ok = s.metrics[newID]
	}
	copyLabels := make(map[string]string)
	for k, v := range labels {
		copyLabels[k] = v
	}
	m := metric{
		labels:   copyLabels,
		store:    s,
		statusOf: statusOf,
		metricID: newID,
	}
	s.metrics[newID] = m
	return m
}

// addPoint appends point for given metric
//
// The store lock is assumed to be held
func (s *Store) addPoint(metricID int, point types.PointStatus) {
	if _, ok := s.points[metricID]; !ok {
		s.points[metricID] = make([]types.PointStatus, 0)
	}
	s.points[metricID] = append(s.points[metricID], point)
}
