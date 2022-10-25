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

package blackbox

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

type localLabelPairs struct {
	name  string
	value string
}

type localMetric struct {
	labels []localLabelPairs
	gauge  float64
}

func (m localMetric) toDtoMetric() *dto.Metric {
	res := &dto.Metric{Label: []*dto.LabelPair{}}

	for _, v := range m.labels {
		v := v

		res.Label = append(res.Label, &dto.LabelPair{Name: &v.name, Value: &v.value})
	}

	res.Gauge = &dto.Gauge{Value: &m.gauge}

	return res
}

type dtoLabelPairs []*dto.LabelPair

func (labels dtoLabelPairs) toLocalLabelPairs() []localLabelPairs {
	res := make([]localLabelPairs, 0, len(labels))

	for _, v := range labels {
		res = append(res, localLabelPairs{name: *v.Name, value: *v.Value})
	}

	return res
}

type localMetricFamily struct {
	name   string
	help   string
	ty     dto.MetricType
	metric []localMetric
}

func TestWriteMFsToChan(t *testing.T) {
	// reserve enought space to push all metrics to it at once, so we can run this test sequentially
	ch := make(chan prometheus.Metric, 50)

	mfsData := []localMetricFamily{
		{name: "probe_dns_lookup_time_seconds", help: "Returns the time taken for probe dns lookup in seconds", ty: dto.MetricType_GAUGE, metric: []localMetric{{gauge: 0.000549313, labels: []localLabelPairs{}}}},
		{name: "probe_http_duration_seconds", help: "Duration of http request by phase, summed over all redirects", ty: dto.MetricType_GAUGE, metric: []localMetric{
			{labels: []localLabelPairs{{name: "additional_label", value: "test"}, {name: "phase", value: "connect"}}, gauge: 0.023523778},
			{labels: []localLabelPairs{{name: "phase", value: "processing"}}, gauge: 2.5},
			{labels: []localLabelPairs{}, gauge: 5.},
		}},
	}

	// build the metric family listing from our test corpus.
	// We are forced to do all these shenanigans as the data model of prometheus relies heavily on string pointers.
	mfs := []*dto.MetricFamily{}

	for _, mfData := range mfsData {
		metrics := []*dto.Metric{}
		for _, metric := range mfData.metric {
			metrics = append(metrics, metric.toDtoMetric())
		}

		mfData := mfData

		mfs = append(mfs, &dto.MetricFamily{Name: &mfData.name, Help: &mfData.help, Type: &mfData.ty, Metric: metrics})
	}

	writeMFsToChan(mfs, ch)
	close(ch)

	for _, mfData := range mfsData {
		for _, metric := range mfData.metric {
			writtenMetric := <-ch

			descLabels := make([]string, 0, len(metric.labels))
			for _, label := range metric.labels {
				descLabels = append(descLabels, label.name)
			}

			// verify that the description is correct
			desc := prometheus.NewDesc(mfData.name, mfData.help, descLabels, map[string]string{})
			if desc.String() != writtenMetric.Desc().String() {
				t.Fatalf("TestWriteMFsToChan() failed checking descriptions, got %s, want %s", writtenMetric.Desc().String(), desc.String())
			}

			// verify the value of the metric, and the label values.
			// This code is brittle as we are acting directly on prometheus internals: prometheus/value.go/constMetric
			metricField := reflect.ValueOf(writtenMetric).Elem().FieldByName("metric")
			writtenMetricDtoInterface := reflect.NewAt(metricField.Type(), unsafe.Pointer(metricField.UnsafeAddr())).Elem().Interface()

			writtenMetricDto, ok := writtenMetricDtoInterface.(*dto.Metric)
			if !ok {
				t.Fatalf("Failed to convert metric %T to *dto.Metric", writtenMetricDtoInterface)
			}

			writtenMetricVal := writtenMetricDto.GetGauge().GetValue()
			if writtenMetricVal != metric.gauge {
				t.Fatalf("TestWriteMFsToChan() failed checking metrics values, got %f, want %f", writtenMetricVal, metric.gauge)
			}

			writtenMetricLabels := (dtoLabelPairs)(writtenMetricDto.GetLabel()).toLocalLabelPairs()
			if !reflect.DeepEqual(writtenMetricLabels, metric.labels) {
				t.Fatalf("TestWriteMFsToChan() failed checking labels values, got %v, want %v", writtenMetricLabels, metric.labels)
			}
		}
	}
}
