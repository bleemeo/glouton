// Copyright 2015-2023 Bleemeo
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
	"glouton/types"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	dto "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/proto"
)

func Test_Host_Port(t *testing.T) {
	url, _ := url.Parse("https://example.com:8080")
	want := "example.com:8080" //nolint:ifshort

	if got := HostPort(url); got != want {
		t.Errorf("An error occurred: expected %s, got %s", want, got)
	}
}

func Test_parserReader(t *testing.T) {
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
								{Name: proto.String("method"), Value: proto.String("post")},
								{Name: proto.String("code"), Value: proto.String("200")},
							},
							Counter: &dto.Counter{
								Value: proto.Float64(1027),
							},
						},
						{
							TimestampMs: proto.Int64(1395066363000),
							Label: []*dto.LabelPair{
								{Name: proto.String("method"), Value: proto.String("post")},
								{Name: proto.String("code"), Value: proto.String("400")},
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
								{Name: proto.String("path"), Value: proto.String("C:\\DIR\\FILE.TXT")},
								{Name: proto.String("error"), Value: proto.String("Cannot find file:\n\"FILE.TXT\"")},
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
							TimestampMs: proto.Int64(-3982045),
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
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.filename, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(filepath.Join("testdata", tt.filename))
			if err != nil {
				t.Fatal(err)
			}

			got, err := parserReader(data)
			if err != nil {
				t.Fatalf("parserReader() error = %v", err)
			}

			if diff := types.DiffMetricFamilies(tt.want, got, false, true); diff != "" {
				t.Errorf("parserReader missmatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Benchmark_parserReader_sample(b *testing.B) {
	data, err := os.ReadFile(filepath.Join("testdata", "sample.txt"))
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		_, err := parserReader(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_parserReader_large(b *testing.B) {
	data, err := os.ReadFile(filepath.Join("testdata", "large.txt"))
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		_, err := parserReader(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}
