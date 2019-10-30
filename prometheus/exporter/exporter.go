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

package exporter

import (
	"glouton/logger"
	"glouton/types"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/node_exporter/collector"
	"gopkg.in/alecthomas/kingpin.v2"
)

// Exporter is a Promtheus export that export metrics from Glouton
type Exporter struct {
	db storeInterface

	registry  *prometheus.Registry
	gatherers prometheus.Gatherers
	handler   http.Handler
}

type storeInterface interface {
	Metrics(filters map[string]string) (result []types.Metric, err error)
}

// New return a new exporter
func New(db storeInterface, gatherer prometheus.Gatherer) *Exporter {

	e := &Exporter{
		db: db,
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		e,
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)
	addNodeExporter(reg)
	e.registry = reg
	if gatherer != nil {
		e.gatherers = prometheus.Gatherers{reg, gatherer}
	} else {
		e.gatherers = prometheus.Gatherers{reg}
	}
	e.handler = promhttp.InstrumentMetricHandler(reg, promhttp.HandlerFor(e.gatherers, promhttp.HandlerOpts{}))

	return e
}

func (e Exporter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	e.handler.ServeHTTP(w, req)
}

func addNodeExporter(reg prometheus.Registerer) {
	if _, err := kingpin.CommandLine.Parse(nil); err != nil {
		logger.Printf("Failed to initialize kingpin (used by Prometheus node_exporter): %v", err)
	}
	collector, err := collector.NewNodeCollector()
	if err != nil {
		logger.Printf("Failed to create node_exporter: %v", err)
	}
	err = reg.Register(collector)
	if err != nil {
		logger.Printf("Failed to register node_exporter: %v", err)
	}
}

// Return the most recent point. ok is false if no point are found
func getLastPoint(m types.Metric) (point types.Point, ok bool) {
	points, err := m.Points(time.Now().Add(-5*time.Minute), time.Now())
	if err != nil {
		return
	}
	for _, p := range points {
		ok = true
		if p.Time.After(point.Time) {
			point = p.Point
		}
	}
	return
}

// Describe implment Describe of a Prometheus collector
func (e Exporter) Describe(chan<- *prometheus.Desc) {
}

// Collect implment Collect of a Prometheus collector
func (e Exporter) Collect(ch chan<- prometheus.Metric) {
	metrics, err := e.db.Metrics(nil)
	if err != nil {
		return
	}
	for _, m := range metrics {
		if p, ok := getLastPoint(m); ok {
			labels := make([]string, 0)
			labelValues := make([]string, 0)
			for l, v := range m.Labels() {
				if l != "__name__" {
					labels = append(labels, l)
					labelValues = append(labelValues, v)
				}
			}
			ch <- prometheus.NewMetricWithTimestamp(p.Time, prometheus.MustNewConstMetric(
				prometheus.NewDesc(m.Labels()["__name__"], "", labels, nil),
				prometheus.UntypedValue,
				p.Value,
				labelValues...,
			))
		}
	}
}
