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

// Package registry package implement a dynamic collection of metrics sources
//
// It support both pushed metrics (using AddMetricPointFunction) and pulled
// metrics thought Collector or Gatherer
package registry

import (
	"context"
	"glouton/logger"
	"glouton/types"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

const (
	pushedPointsCleanupInterval = 5 * time.Minute
)

type pushFunction func(points []types.MetricPoint)

// AddMetricPoints implement PointAdder
func (f pushFunction) PushPoints(points []types.MetricPoint) {
	f(points)
}

// Registry is a dynamic collection of metrics sources.
//
// For the Prometheus metrics source, it just a wrapper around prometheus.Gatherers,
// but is also support pushed metrics.
type Registry struct {
	PushPoint types.PointPusher

	l                       sync.Mutex
	collectors              []prometheus.Collector
	gatherersPull           Gatherers
	registyPull             *prometheus.Registry
	registyPush             *prometheus.Registry
	pushedPoints            map[string]types.MetricPoint
	pushedPointsExpiration  map[string]time.Time
	lastPushedPointsCleanup time.Time
	currentDelay            time.Duration
	updateDelayC            chan interface{}
}

// This type is used to have another Collecto() method private which only return pulled points
type pullCollector Registry

// This type is used to have another Collecto() method private which only return pushed points
type pushCollector Registry

func (r *Registry) init() {
	r.l.Lock()

	if r.registyPull != nil {
		r.l.Unlock()
		return
	}

	r.registyPull = prometheus.NewRegistry()
	r.gatherersPull = append(r.gatherersPull, r.registyPull)
	r.registyPush = prometheus.NewRegistry()
	r.pushedPoints = make(map[string]types.MetricPoint)
	r.pushedPointsExpiration = make(map[string]time.Time)
	r.currentDelay = 10 * time.Second
	r.updateDelayC = make(chan interface{})

	r.l.Unlock()

	// Gather & Register shouldn't be done with the lock, as is will call
	// Describe and/or Collect which may take the lock

	_ = r.registyPush.Register((*pushCollector)(r))
	_ = r.registyPull.Register((*pullCollector)(r))
}

// AddDefaultCollector add GoCollector and ProcessCollector like the prometheus.DefaultRegisterer
func (r *Registry) AddDefaultCollector() {
	r.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	r.MustRegister(prometheus.NewGoCollector())
}

// AddNodeExporter add a node_exporter to collector
func (r *Registry) AddNodeExporter(option NodeExporterOption) error {
	collector, err := nodeExporterCollector(option)
	if err != nil {
		return err
	}
	err = r.Register(collector)
	return err
}

// Register add a new collector to the list of metric sources.
func (r *Registry) Register(collector prometheus.Collector) error {
	r.init()
	r.l.Lock()
	defer r.l.Unlock()

	for _, c := range r.collectors {
		if c == collector {
			return prometheus.AlreadyRegisteredError{
				ExistingCollector: c,
				NewCollector:      collector,
			}
		}
	}

	r.collectors = append(r.collectors, collector)
	return nil
}

// RegisterGatherer add a new gatherer to the list of metric sources.
func (r *Registry) RegisterGatherer(gatherer prometheus.Gatherer) {
	r.init()
	r.l.Lock()
	defer r.l.Unlock()

	r.gatherersPull = append(r.gatherersPull, gatherer)
}

// MustRegister add a new collector to the list of metric sources.
func (r *Registry) MustRegister(collectors ...prometheus.Collector) {
	for _, c := range collectors {
		err := r.Register(c)
		if err != nil {
			panic(err)
		}
	}
}

// Unregister remove a collector from the list of metric sources.
func (r *Registry) Unregister(collector prometheus.Collector) bool {
	r.l.Lock()
	defer r.l.Unlock()

	for i, c := range r.collectors {
		if c == collector {
			r.collectors[i] = r.collectors[len(r.collectors)-1]
			r.collectors[len(r.collectors)-1] = nil
			r.collectors = r.collectors[:len(r.collectors)-1]
			return true
		}
	}
	return false
}

type prefixLogger string

func (l prefixLogger) Println(v ...interface{}) {
	all := make([]interface{}, 0, len(v)+1)
	all = append(all, l)
	all = append(all, v...)
	logger.V(1).Println(all...)
}

// Exporter return an HTTP exporter
func (r *Registry) Exporter() http.Handler {
	return promhttp.InstrumentMetricHandler(
		r,
		promhttp.HandlerFor(r, promhttp.HandlerOpts{
			ErrorHandling: promhttp.ContinueOnError,
			ErrorLog:      prefixLogger("/metrics endpoint:"),
		}),
	)
}

// WithTTL return a AddMetricPointFunction with TTL on pushed points.
func (r *Registry) WithTTL(ttl time.Duration) types.PointPusher {
	r.init()
	return pushFunction(func(points []types.MetricPoint) {
		r.pushPoint(points, ttl)
	})
}

// RunCollection runs collection of all collector & gatherer at regular interval.
// The interval could be updated by call to UpdateDelay
func (r *Registry) RunCollection(ctx context.Context) error {
	r.init()

	for ctx.Err() == nil {
		r.run(ctx)
	}
	return nil
}

// UpdateDelay change the delay between metric gather
func (r *Registry) UpdateDelay(delay time.Duration) {
	r.init()

	r.l.Lock()
	if r.currentDelay == delay {
		r.l.Unlock()
		return
	}
	r.currentDelay = delay
	r.l.Unlock()
	logger.V(2).Printf("Change metric collector delay to %v", delay)
	r.updateDelayC <- nil
}

func (r *Registry) run(ctx context.Context) {
	r.l.Lock()
	currentDelay := r.currentDelay
	r.l.Unlock()

	sleepToAlign(currentDelay)
	ticker := time.NewTicker(currentDelay)
	defer ticker.Stop()
	for {
		r.runOnce()
		select {
		case <-r.updateDelayC:
			return
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (r *Registry) runOnce() {
	families, err := r.gatherersPull.Gather()
	if err != nil {
		logger.Printf("Gather of metrics failed, some metrics may be missing: %v", err)
	}
	points := familiesToMetricPoints(families)
	r.PushPoint.PushPoints(points)
}

func familiesToMetricPoints(families []*dto.MetricFamily) []types.MetricPoint {
	samples, err := expfmt.ExtractSamples(
		&expfmt.DecodeOptions{Timestamp: model.Now()},
		families...,
	)
	if err != nil {
		logger.Printf("Conversion of metrics failed, some metrics may be missing: %v", err)
	}
	result := make([]types.MetricPoint, len(samples))
	for i, sample := range samples {
		labels := make(map[string]string, len(sample.Metric))
		for k, v := range sample.Metric {
			labels[string(k)] = string(v)
		}

		result[i] = types.MetricPoint{
			Labels: labels,
			Point: types.Point{
				Time:  sample.Timestamp.Time(),
				Value: float64(sample.Value),
			},
		}
	}
	return result
}

// sleep such are time.Now() is aligned on a multiple of interval
func sleepToAlign(interval time.Duration) {
	now := time.Now()
	previousMultiple := now.Truncate(interval)
	if previousMultiple == now {
		return
	}
	nextMultiple := previousMultiple.Add(interval)
	time.Sleep(nextMultiple.Sub(now))
}

// pushPoint add a new point to the list of pushed point with a specified TTL.
// As for AddMetricPointFunction, points should not be mutated after the call
func (r *Registry) pushPoint(points []types.MetricPoint, ttl time.Duration) {
	r.l.Lock()

	now := time.Now()
	deadline := now.Add(ttl)

	for _, point := range points {
		key := types.LabelsToText(point.Labels)
		r.pushedPoints[key] = point
		r.pushedPointsExpiration[key] = deadline
	}

	if now.Sub(r.lastPushedPointsCleanup) > pushedPointsCleanupInterval {
		r.lastPushedPointsCleanup = now
		for key, expiration := range r.pushedPointsExpiration {
			if now.After(expiration) {
				delete(r.pushedPoints, key)
				delete(r.pushedPointsExpiration, key)
			}
		}
	}

	r.l.Unlock()

	if r.PushPoint != nil {
		r.PushPoint.PushPoints(points)
	}
}

// Gather gathers all metric sources, including push metric source
func (r *Registry) Gather() ([]*dto.MetricFamily, error) {
	r.l.Lock()

	gatherers := make(Gatherers, len(r.gatherersPull)+1)

	for i, g := range r.gatherersPull {
		gatherers[i] = g
	}

	gatherers[len(gatherers)-1] = r.registyPush

	r.l.Unlock()

	return gatherers.Gather()
}

// Describe implement prometheus.Collector
func (c *pullCollector) Describe(chan<- *prometheus.Desc) {
}

// Collect collect non-pushed points from all registered collectors
func (c *pullCollector) Collect(ch chan<- prometheus.Metric) {
	c.l.Lock()

	collectorsCopy := make([]prometheus.Collector, len(c.collectors))
	copy(collectorsCopy, c.collectors)
	c.l.Unlock()

	var wg sync.WaitGroup

	for _, collector := range collectorsCopy {
		collector := collector
		wg.Add(1)
		go func() {
			defer wg.Done()
			collector.Collect(ch)
		}()
	}
	wg.Wait()
}

// Describe implement prometheus.Collector
func (c *pushCollector) Describe(chan<- *prometheus.Desc) {
}

// Collect collect non-pushed points from all registered collectors
func (c *pushCollector) Collect(ch chan<- prometheus.Metric) {
	c.l.Lock()
	defer c.l.Unlock()

	now := time.Now()

	c.lastPushedPointsCleanup = now

	for key, p := range c.pushedPoints {
		expiration := c.pushedPointsExpiration[key]
		if now.After(expiration) {
			delete(c.pushedPoints, key)
			delete(c.pushedPointsExpiration, key)
			continue
		}
		labelKeys := make([]string, 0)
		labelValues := make([]string, 0)
		for l, v := range p.Labels {
			if l != "__name__" {
				labelKeys = append(labelKeys, l)
				labelValues = append(labelValues, v)
			}
		}
		ch <- prometheus.NewMetricWithTimestamp(p.Time, prometheus.MustNewConstMetric(
			prometheus.NewDesc(p.Labels["__name__"], "", labelKeys, nil),
			prometheus.UntypedValue,
			p.Value,
			labelValues...,
		))
	}
}
