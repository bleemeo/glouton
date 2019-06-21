// Package store implement a Metric/MetricPoint store.
//
// currently the storage in only in-memory and not persisted.
package store

import (
	"context"
	"log"
	"sync"
	"time"

	"agentgo/types"
)

// Store implement an interface to retrive metrics and metric points.
//
// See methods GetMetrics and GetMetricPoints
type Store struct {
	metrics map[int]metric
	points  map[int][]types.Point
	lock    sync.Mutex
}

// New create a return a store. Store should be Close()d before leaving
func New() *Store {
	s := &Store{
		metrics: make(map[int]metric),
		points:  make(map[int][]types.Point),
	}
	return s
}

// Run will run the store until context is cancelled
func (s *Store) Run(ctx context.Context) {
	for {
		s.run()
		select {
		case <-time.After(60 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

// Close closes the store. Currently noop, but may write to persistence later
func (s *Store) Close() {
	log.Println("Store closed")
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
func (m metric) Points(start, end time.Time) (result []types.Point, err error) {
	m.store.lock.Lock()
	defer m.store.lock.Unlock()
	points := m.store.points[m.metricID]
	result = make([]types.Point, 0)
	for _, point := range points {
		if start.Before(point.Time) && point.Time.Before(end) {
			result = append(result, point)
		}
	}
	return
}

type metric struct {
	labels   map[string]string
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
		newPoints := make([]types.Point, 0)
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
	log.Printf("Deleted %d points. Total point: %d", deletedPoints, totalPoints)
}

// metricsExact will return the metric that exactly match given labels.
//
// If the metric does not exists, create it.
// The store lock is assumed to be held.
func (s *Store) metricGetOrCreate(labels map[string]string) metric {
	for _, m := range s.metrics {
		if labelsMatch(m.labels, labels, true) {
			return m
		}
	}
	newID := 1
	_, ok := s.metrics[newID]
	for ok {
		newID = newID + 1
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
		metricID: newID,
	}
	s.metrics[newID] = m
	return m
}

// addPoint appends point for given metric
//
// The store lock is assumed to be held
func (s *Store) addPoint(metricID int, point types.Point) {
	if _, ok := s.points[metricID]; !ok {
		s.points[metricID] = make([]types.Point, 0)
	}
	s.points[metricID] = append(s.points[metricID], point)
}
