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

package scrapper

import (
	"bytes"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/bleemeo/glouton/types"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	prometheusModel "github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"google.golang.org/protobuf/proto"
)

func Test_Host_Port(t *testing.T) {
	url, _ := url.Parse("https://example.com:8080")
	want := "example.com:8080"

	if got := HostPort(url); got != want {
		t.Errorf("An error occurred: expected %s, got %s", want, got)
	}
}

// parserReaderReference is the previous implementation used.
func parserReaderReference(data []byte, filter func(lbls labels.Labels) bool) ([]*dto.MetricFamily, error) {
	// filter isn't used by TextToMetricFamilies
	_ = filter

	parser := expfmt.NewTextParser(prometheusModel.LegacyValidation)

	resultMap, err := parser.TextToMetricFamilies(bytes.NewReader(data))
	if err != nil {
		return nil, TargetError{
			DecodeErr: err,
		}
	}

	result := make([]*dto.MetricFamily, 0, len(resultMap))

	for _, family := range resultMap {
		result = append(result, family)
	}

	return result, nil
}

func Test_parserReader(t *testing.T) { //nolint:maintidx
	t.Parallel()

	tests := []struct {
		filename string
		want     []*dto.MetricFamily
	}{
		{
			filename: "sample.txt",
			want: []*dto.MetricFamily{
				{
					Name: proto.String("http_requests_total"),
					Help: proto.String("The total number of HTTP requests."),
					Type: dto.MetricType_COUNTER.Enum(),
					Metric: []*dto.Metric{
						{
							TimestampMs: proto.Int64(1395066363000),
							Label: []*dto.LabelPair{
								{Name: proto.String("code"), Value: proto.String("200")},
								{Name: proto.String("method"), Value: proto.String("post")},
							},
							Counter: &dto.Counter{
								Value: proto.Float64(1027),
							},
						},
						{
							TimestampMs: proto.Int64(1395066363000),
							Label: []*dto.LabelPair{
								{Name: proto.String("code"), Value: proto.String("400")},
								{Name: proto.String("method"), Value: proto.String("post")},
							},
							Counter: &dto.Counter{
								Value: proto.Float64(3),
							},
						},
					},
				},
				{
					Name: proto.String("msdos_file_access_time_seconds"),
					Help: nil,
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							TimestampMs: nil,
							Label: []*dto.LabelPair{
								{Name: proto.String("error"), Value: proto.String("Cannot find file:\n\"FILE.TXT\"")},
								{Name: proto.String("path"), Value: proto.String("C:\\DIR\\FILE.TXT")},
							},
							Untyped: &dto.Untyped{
								Value: proto.Float64(1.458255915e9),
							},
						},
					},
				},
				{
					Name: proto.String("metric_without_timestamp_and_labels"),
					Help: nil,
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							TimestampMs: nil,
							Untyped: &dto.Untyped{
								Value: proto.Float64(12.47),
							},
						},
					},
				},
				{
					Name: proto.String("something_weird"),
					Help: nil,
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							TimestampMs: proto.Int64(3982045),
							Label: []*dto.LabelPair{
								{Name: proto.String("problem"), Value: proto.String("division by zero")},
							},
							Untyped: &dto.Untyped{
								Value: proto.Float64(math.Inf(1)),
							},
						},
					},
				},
				{
					Name: proto.String("http_request_duration_seconds"),
					Help: proto.String("A histogram of the request duration."),
					Type: dto.MetricType_HISTOGRAM.Enum(),
					Metric: []*dto.Metric{
						{
							TimestampMs: nil,
							Histogram: &dto.Histogram{
								SampleCount: proto.Uint64(144320),
								SampleSum:   proto.Float64(53423),
								Bucket: []*dto.Bucket{
									{
										CumulativeCount: proto.Uint64(24054),
										UpperBound:      proto.Float64(0.05),
									},
									{
										CumulativeCount: proto.Uint64(33444),
										UpperBound:      proto.Float64(0.1),
									},
									{
										CumulativeCount: proto.Uint64(100392),
										UpperBound:      proto.Float64(0.2),
									},
									{
										CumulativeCount: proto.Uint64(129389),
										UpperBound:      proto.Float64(0.5),
									},
									{
										CumulativeCount: proto.Uint64(133988),
										UpperBound:      proto.Float64(1),
									},
									{
										CumulativeCount: proto.Uint64(144320),
										UpperBound:      proto.Float64(math.Inf(1)),
									},
								},
							},
						},
					},
				},
				{
					Name: proto.String("rpc_duration_seconds"),
					Help: proto.String("A summary of the RPC duration in seconds."),
					Type: dto.MetricType_SUMMARY.Enum(),
					Metric: []*dto.Metric{
						{
							TimestampMs: nil,
							Summary: &dto.Summary{
								SampleCount: proto.Uint64(2693),
								SampleSum:   proto.Float64(1.7560473e+07),
								Quantile: []*dto.Quantile{
									{
										Quantile: proto.Float64(0.01),
										Value:    proto.Float64(3102),
									},
									{
										Quantile: proto.Float64(0.05),
										Value:    proto.Float64(3272),
									},
									{
										Quantile: proto.Float64(0.5),
										Value:    proto.Float64(4773),
									},
									{
										Quantile: proto.Float64(0.9),
										Value:    proto.Float64(9001),
									},
									{
										Quantile: proto.Float64(0.99),
										Value:    proto.Float64(76656),
									},
								},
							},
						},
					},
				},
			},
		},
		{
			filename: "sample2.txt",
			want: []*dto.MetricFamily{
				{
					Name: proto.String("metric1"),
					Help: proto.String("Help text of metric1"),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							TimestampMs: nil,
							Label: []*dto.LabelPair{
								{Name: proto.String("occurrence"), Value: proto.String("1")},
							},
							Untyped: &dto.Untyped{
								Value: proto.Float64(1),
							},
						},
						{
							TimestampMs: nil,
							Label: []*dto.LabelPair{
								{Name: proto.String("occurrence"), Value: proto.String("2")},
							},
							Untyped: &dto.Untyped{
								Value: proto.Float64(1.2),
							},
						},
					},
				},
				{
					Name: proto.String("metric2"),
					Help: proto.String("Help text of metric2 with TYPE"),
					Type: dto.MetricType_GAUGE.Enum(),
					Metric: []*dto.Metric{
						{
							TimestampMs: nil,
							Label: []*dto.LabelPair{
								{Name: proto.String("occurrence"), Value: proto.String("1")},
							},
							Gauge: &dto.Gauge{
								Value: proto.Float64(2),
							},
						},
						{
							TimestampMs: nil,
							Label: []*dto.LabelPair{
								{Name: proto.String("occurrence"), Value: proto.String("2")},
							},
							Gauge: &dto.Gauge{
								Value: proto.Float64(2.2),
							},
						},
					},
				},
				{
					Name: proto.String("metric3"),
					Help: proto.String("late help for metric3"),
					Type: dto.MetricType_UNTYPED.Enum(),
					Metric: []*dto.Metric{
						{
							TimestampMs: nil,
							Label: []*dto.LabelPair{
								{Name: proto.String("occurrence"), Value: proto.String("1")},
							},
							Untyped: &dto.Untyped{
								Value: proto.Float64(3),
							},
						},
						{
							TimestampMs: nil,
							Label: []*dto.LabelPair{
								{Name: proto.String("occurrence"), Value: proto.String("2")},
							},
							Untyped: &dto.Untyped{
								Value: proto.Float64(3.2),
							},
						},
					},
				},
				{
					Name: proto.String("metric4"),
					Help: proto.String("help of metric4"),
					Type: dto.MetricType_COUNTER.Enum(),
					Metric: []*dto.Metric{
						{
							TimestampMs: nil,
							Counter: &dto.Counter{
								Value: proto.Float64(4),
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(filepath.Join("testdata", tt.filename))
			if err != nil {
				t.Fatal(err)
			}

			var got []*dto.MetricFamily

			got, err = parserReader(data, nil)
			if err != nil {
				t.Fatalf("parserReader() error = %v", err)
			}

			want := tt.want

			referenceGot, err := parserReaderReference(data, nil)
			if err != nil {
				t.Fatal(err)
			}

			// Our parser don't support histogram & summary
			want = flattenHistogramAndSummary(want)
			referenceGot = flattenHistogramAndSummary(referenceGot)

			// parser.TextToMetricFamilies does not sort labels
			sortLabels(referenceGot)

			if diff := types.DiffMetricFamilies(want, got, false, true); diff != "" {
				t.Errorf("parserReader missmatch (-want +got):\n%s", diff)
			}

			if diff := types.DiffMetricFamilies(referenceGot, got, false, true); diff != "" {
				t.Errorf("parserReader missmatch with reference (-want +got):\n%s", diff)
			}
		})
	}
}

func sortLabels(mfs []*dto.MetricFamily) {
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			sort.Slice(m.Label, func(i, j int) bool { //nolint:protogetter
				return m.Label[i].GetName() < m.Label[j].GetName() //nolint:protogetter
			})
		}
	}
}

// flattenHistogramAndSummary convert histogram & summary to untype. This function is rather hackish and work only on limited
// value (mosty have a simple histogram and single summary value).
func flattenHistogramAndSummary(mfs []*dto.MetricFamily) []*dto.MetricFamily {
	// When native histogram aren't supported, flatten them
	flatMFS := make([]*dto.MetricFamily, 0, len(mfs))

	for _, mf := range mfs {
		if mf.GetType().String() != dto.MetricType_HISTOGRAM.String() && mf.GetType().String() != dto.MetricType_SUMMARY.String() {
			flatMFS = append(flatMFS, mf)

			continue
		}

		metric := mf.GetMetric()[0]

		if mf.GetType().String() == dto.MetricType_HISTOGRAM.String() {
			flatMFS = append(flatMFS, &dto.MetricFamily{
				Name: proto.String(mf.GetName() + "_sum"),
				Help: nil,
				Type: dto.MetricType_UNTYPED.Enum(),
				Metric: []*dto.Metric{
					{
						Label: metric.GetLabel(),
						Untyped: &dto.Untyped{
							Value: proto.Float64(metric.GetHistogram().GetSampleSum()),
						},
					},
				},
			})
			flatMFS = append(flatMFS, &dto.MetricFamily{
				Name: proto.String(mf.GetName() + "_count"),
				Help: nil,
				Type: dto.MetricType_UNTYPED.Enum(),
				Metric: []*dto.Metric{
					{
						Label: metric.GetLabel(),
						Untyped: &dto.Untyped{
							Value: proto.Float64(float64(metric.GetHistogram().GetSampleCount())),
						},
					},
				},
			})

			bucketMetrics := make([]*dto.Metric, 0, len(metric.GetHistogram().GetBucket()))
			for _, b := range metric.GetHistogram().GetBucket() {
				bucketMetrics = append(bucketMetrics, &dto.Metric{
					Label: append(
						metric.GetLabel(),
						&dto.LabelPair{
							Name:  proto.String("le"),
							Value: proto.String(formatFloat(b.GetUpperBound())),
						},
					),
					Untyped: &dto.Untyped{
						Value: proto.Float64(float64(b.GetCumulativeCount())),
					},
				})
			}

			flatMFS = append(flatMFS, &dto.MetricFamily{
				Name:   proto.String(mf.GetName() + "_bucket"),
				Help:   nil,
				Type:   dto.MetricType_UNTYPED.Enum(),
				Metric: bucketMetrics,
			})
		}

		if mf.GetType().String() == dto.MetricType_SUMMARY.String() {
			flatMFS = append(flatMFS, &dto.MetricFamily{
				Name: proto.String(mf.GetName() + "_sum"),
				Help: nil,
				Type: dto.MetricType_UNTYPED.Enum(),
				Metric: []*dto.Metric{
					{
						Label: metric.GetLabel(),
						Untyped: &dto.Untyped{
							Value: proto.Float64(metric.GetSummary().GetSampleSum()),
						},
					},
				},
			})
			flatMFS = append(flatMFS, &dto.MetricFamily{
				Name: proto.String(mf.GetName() + "_count"),
				Help: nil,
				Type: dto.MetricType_UNTYPED.Enum(),
				Metric: []*dto.Metric{
					{
						Label: metric.GetLabel(),
						Untyped: &dto.Untyped{
							Value: proto.Float64(float64(metric.GetSummary().GetSampleCount())),
						},
					},
				},
			})

			quantileMetrics := make([]*dto.Metric, 0, len(metric.GetSummary().GetQuantile()))
			for _, q := range metric.GetSummary().GetQuantile() {
				quantileMetrics = append(quantileMetrics, &dto.Metric{
					Label: append(
						metric.GetLabel(),
						&dto.LabelPair{
							Name:  proto.String("quantile"),
							Value: proto.String(fmt.Sprint(q.GetQuantile())),
						},
					),
					Untyped: &dto.Untyped{
						Value: proto.Float64(q.GetValue()),
					},
				})
			}

			flatMFS = append(flatMFS, &dto.MetricFamily{
				Name: proto.String(mf.GetName()),
				// summary quantile kept the help because the name is unchanged.
				// This test code is made to match what parser does. But we don't really care
				// about help or type, only name, labels & value matter.
				Help:   proto.String(mf.GetHelp()),
				Type:   dto.MetricType_UNTYPED.Enum(),
				Metric: quantileMetrics,
			})
		}
	}

	sortLabels(flatMFS)

	return flatMFS
}

func formatFloat(f float64) string {
	res := strconv.FormatFloat(f, 'f', -1, 64)
	if math.Round(f) != f || math.IsInf(f, 1) || math.IsInf(f, -1) {
		return res
	}

	return res + ".0"
}

func Benchmark_parserReader(b *testing.B) {
	for _, filename := range []string{"sample.txt", "large.txt"} {
		for _, useRef := range []bool{true, false} {
			name := filename

			if useRef {
				name += "-ref-impl"
			}

			b.Run(name, func(b *testing.B) {
				data, err := os.ReadFile(filepath.Join("testdata", filename))
				if err != nil {
					b.Fatal(err)
				}

				b.ResetTimer()

				for b.Loop() {
					if useRef {
						_, err = parserReaderReference(data, nil)
					} else {
						_, err = parserReader(data, nil)
					}

					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
