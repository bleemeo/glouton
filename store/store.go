// Copyright 2015-2019 Bleemeo
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

// Package store implement a Metric/MetricPoint store.
//
// currently the storage in only in-memory and not persisted.
package store

import (
	"context"
	"errors"
	"glouton/logger"
	"glouton/types"
	"reflect"
	"sync"
	"time"
)

var errDeletedMetric = errors.New("metric was deleted")

// Store implement an interface to retrieve metrics and metric points.
//
// See methods GetMetrics and GetMetricPoints.
type Store struct {
	metrics           map[int]metric
	points            map[int][]types.Point
	notifyCallbacks   map[int]func([]types.MetricPoint)
	resetRuleCallback func()
	lock              sync.Mutex
	notifeeLock       sync.Mutex
	resetRuleLock     sync.Mutex
}

// New create a return a store. Store should be Close()d before leaving.
func New() *Store {
	s := &Store{
		metrics:           make(map[int]metric),
		points:            make(map[int][]types.Point),
		notifyCallbacks:   make(map[int]func([]types.MetricPoint)),
		resetRuleCallback: nil,
	}

	return s
}

// Run will run the store until context is cancelled.
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

// AddNotifiee add a callback that will be notified of all points received
// Note: AddNotifiee should not be called while in the callback.
func (s *Store) AddNotifiee(cb func([]types.MetricPoint)) int {
	s.notifeeLock.Lock()
	defer s.notifeeLock.Unlock()

	id := 1
	_, ok := s.notifyCallbacks[id]

	for ok {
		id++
		if id == 0 {
			panic("too many notifiee in the store. Unable to find new slot")
		}

		_, ok = s.notifyCallbacks[id]
	}

	s.notifyCallbacks[id] = cb

	return id
}

// RemoveNotifiee remove a callback that was notified
// Note: RemoveNotifiee should not be called while in the callback.
// Once RemoveNotifiee() returns, the callbacl won't be called anymore.
func (s *Store) RemoveNotifiee(id int) {
	s.notifeeLock.Lock()
	defer s.notifeeLock.Unlock()

	delete(s.notifyCallbacks, id)
}

// SetResetRuleCallback sets the resetRuleCallbacks.
func (s *Store) SetResetRuleCallback(fc func()) {
	s.resetRuleLock.Lock()
	defer s.resetRuleLock.Unlock()

	s.resetRuleCallback = fc
}

// DropMetrics delete metrics and they points.
// The provided labels list is an exact match (e.g. {"__name__": "disk_used"} won't delete the metrics for all disk. You need to specify all labels).
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

// DropAllMetrics clear the full content of the store.
func (s *Store) DropAllMetrics() {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.metrics = make(map[int]metric)
	s.points = make(map[int][]types.Point)
}

// Metrics return a list of Metric matching given labels filter.
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

// MetricsCount return the count of metrics stored.
func (s *Store) MetricsCount() int {
	s.lock.Lock()
	defer s.lock.Unlock()

	return len(s.metrics)
}

// Labels returns all label of the metric.
func (m metric) Labels() map[string]string {
	labels := make(map[string]string)

	for k, v := range m.labels {
		labels[k] = v
	}

	return labels
}

// Annotations returns all annotations of the metric.
func (m metric) Annotations() types.MetricAnnotations {
	return m.annotations
}

// Points returns points between the two given time range (boundary are included).
func (m metric) Points(start, end time.Time) (result []types.Point, err error) {
	m.store.lock.Lock()
	defer m.store.lock.Unlock()

	if !m.store.metrics[m.metricID].createAt.Equal(m.createAt) {
		return nil, errDeletedMetric
	}

	points := m.store.points[m.metricID]
	result = make([]types.Point, 0)

	for _, point := range points {
		pointTimeUTC := point.Time.UTC()
		if !pointTimeUTC.Before(start) && !pointTimeUTC.After(end) {
			result = append(result, point)
		}
	}

	return
}

type metric struct {
	labels      map[string]string
	annotations types.MetricAnnotations
	store       *Store
	metricID    int
	createAt    time.Time
}

// Return true if filter match given labels.
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

	logger.V(2).Printf("deleted %d points. Total point: %d", deletedPoints, totalPoints)
}

// metricGetOrCreate will return the metric that exactly match given labels.
//
// If the metric does not exists, it's created.
// The store lock is assumed to be held.
// Annotations is always updated with value provided as argument.
func (s *Store) metricGetOrCreate(labels map[string]string, annotations types.MetricAnnotations) (metric, bool) {
	for id, m := range s.metrics {
		if labelsMatch(m.labels, labels, true) {
			m.annotations = annotations
			s.metrics[id] = m

			return m, false
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

	m := metric{
		labels:      labels,
		annotations: annotations,
		store:       s,
		metricID:    newID,
		createAt:    time.Now(),
	}

	s.metrics[newID] = m

	return m, true
}

// PushPoints append new metric points to the store, creating new metric
// if needed.
// The points must not be mutated after this call.
func (s *Store) PushPoints(points []types.MetricPoint) {
	dedupPoints := make([]types.MetricPoint, 0, len(points))
	newMetrics := false

	s.lock.Lock()
	for _, point := range points {
		metric, created := s.metricGetOrCreate(point.Labels, point.Annotations)
		length := len(s.points[metric.metricID])

		if created {
			newMetrics = true
		}

		if length > 0 && s.points[metric.metricID][length-1].Time.Equal(point.Time) {
			continue
		}

		s.points[metric.metricID] = append(s.points[metric.metricID], point.Point)
		dedupPoints = append(dedupPoints, point)
	}

	s.lock.Unlock()
	s.resetRuleLock.Lock()

	if newMetrics && s.resetRuleCallback != nil {
		s.resetRuleCallback()
	}

	s.resetRuleLock.Unlock()
	s.notifeeLock.Lock()

	for _, cb := range s.notifyCallbacks {
		cb(dedupPoints)
	}

	s.notifeeLock.Unlock()
}

type store interface {
	Metrics(filters map[string]string) (result []types.Metric, err error)
	MetricsCount() int
	DropMetrics(labelsList []map[string]string)
	AddNotifiee(func([]types.MetricPoint)) int
	RemoveNotifiee(int)
	PushPoints(points []types.MetricPoint)
}

// FilteredStore is a store wrapper that intercepts all call to pushPoints and execute filters on points.
type FilteredStore struct {
	store                store
	filterCallback       func([]types.MetricPoint) []types.MetricPoint
	filterMetricCallback func([]types.Metric) []types.Metric
}

// NewFilteredStore initializes a new filtered store.
func NewFilteredStore(store store, fc func([]types.MetricPoint) []types.MetricPoint, fmc func([]types.Metric) []types.Metric) *FilteredStore {
	filteredStore := &FilteredStore{
		store:                store,
		filterCallback:       nil,
		filterMetricCallback: nil,
	}

	filteredStore.filterMetricCallback = fmc
	filteredStore.filterCallback = fc

	return filteredStore
}

// PushPoints wraps the store PushPoints function. It precedes the call with filterCallback.
func (s *FilteredStore) PushPoints(points []types.MetricPoint) {
	if s.filterCallback != nil && len(points) > 0 {
		points = s.filterCallback(points)
	}

	s.store.PushPoints(points)
}

func (s *FilteredStore) Metrics(filters map[string]string) (result []types.Metric, err error) {
	res, err := s.store.Metrics(filters)

	res = s.filterMetricCallback(res)

	return res, err
}

func (s *FilteredStore) MetricsCount() int {
	res, err := s.Metrics(map[string]string{})
	if err != nil {
		logger.V(2).Printf("An error occurred while fetching metrics for filtered store: %w", err)
	}

	return len(res)
}

func (s *FilteredStore) DropMetrics(labelsList []map[string]string) {
	s.store.DropMetrics(labelsList)
}

func (s *FilteredStore) AddNotifiee(fc func([]types.MetricPoint)) int {
	return s.store.AddNotifiee(func(mp []types.MetricPoint) {
		res := s.filterCallback(mp)
		fc(res)
	})
}

func (s *FilteredStore) RemoveNotifiee(v int) {
	s.store.RemoveNotifiee(v)
}
