// nolint: scopelint
package registry

import (
	"glouton/types"
	"reflect"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func Test_mergeLabels(t *testing.T) {
	var (
		strName       = "name"
		strValue      = "value"
		strDC         = "dc"
		strUSEast     = "us-east"
		strMountPoint = "mountpoint"
		strHome       = "/home"
		strRoot       = "/"
	)

	type args struct {
		a []*dto.LabelPair
		b []*dto.LabelPair
	}

	tests := []struct {
		name string
		args args
		want []*dto.LabelPair
	}{
		{
			name: "second-empty",
			args: args{
				a: []*dto.LabelPair{
					{Name: &strName, Value: &strValue},
				},
			},
			want: []*dto.LabelPair{
				{Name: &strName, Value: &strValue},
			},
		},
		{
			name: "first-empty",
			args: args{
				b: []*dto.LabelPair{
					{Name: &strName, Value: &strValue},
				},
			},
			want: []*dto.LabelPair{
				{Name: &strName, Value: &strValue},
			},
		},
		{
			name: "no-conflict",
			args: args{
				a: []*dto.LabelPair{
					// Don't forget that labels must be sorted by name
					{Name: &strMountPoint, Value: &strHome},
					{Name: &strName, Value: &strValue},
				},
				b: []*dto.LabelPair{
					{Name: &strDC, Value: &strUSEast},
				},
			},
			want: []*dto.LabelPair{
				{Name: &strDC, Value: &strUSEast},
				{Name: &strMountPoint, Value: &strHome},
				{Name: &strName, Value: &strValue},
			},
		},
		{
			name: "conflict",
			args: args{
				a: []*dto.LabelPair{
					{Name: &strMountPoint, Value: &strHome},
					{Name: &strName, Value: &strValue},
				},
				b: []*dto.LabelPair{
					{Name: &strDC, Value: &strUSEast},
					{Name: &strMountPoint, Value: &strRoot},
				},
			},
			want: []*dto.LabelPair{
				{Name: &strDC, Value: &strUSEast},
				{Name: &strMountPoint, Value: &strRoot},
				{Name: &strName, Value: &strValue},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeLabels(tt.args.a, tt.args.b); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mergeLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_labeledGatherer_GatherPoints(t *testing.T) {
	var (
		strMetric     = "up"
		strMountpoint = "mountpoint"
		strHome       = "/home"
		strJob        = "job"
		strValue      = "value"
		floatValue1   = 42.0
		floatValue2   = 1337.0
		timestampMS   = time.Date(2020, 2, 28, 13, 54, 48, 0, time.UTC).UnixNano() / 1e6
	)

	g := &fakeGatherer{
		name: "fake 1",
		response: []*dto.MetricFamily{
			{
				Name: &strMetric,
				Type: dto.MetricType_COUNTER.Enum(),
				Metric: []*dto.Metric{
					{
						Label: []*dto.LabelPair{
							{Name: &strMountpoint, Value: &strHome},
						},
						TimestampMs: &timestampMS,
						Counter:     &dto.Counter{Value: &floatValue1},
					},
					{
						Label:       []*dto.LabelPair{},
						TimestampMs: &timestampMS,
						Counter:     &dto.Counter{Value: &floatValue2},
					},
				},
			},
		},
	}

	type fields struct {
		source      prometheus.Gatherer
		labels      []*dto.LabelPair
		annotations types.MetricAnnotations
	}

	tests := []struct {
		name    string
		fields  fields
		want    []types.MetricPoint
		wantErr bool
	}{
		{
			name: "no-change",
			fields: fields{
				source:      g,
				annotations: types.MetricAnnotations{},
				labels:      nil,
			},
			want: []types.MetricPoint{
				{
					Point:       types.Point{Time: time.Unix(0, timestampMS*1e6), Value: floatValue1},
					Annotations: types.MetricAnnotations{},
					Labels: map[string]string{
						types.LabelName: "up",
						strMountpoint:   strHome,
					},
				},
				{
					Point:       types.Point{Time: time.Unix(0, timestampMS*1e6), Value: floatValue2},
					Annotations: types.MetricAnnotations{},
					Labels: map[string]string{
						types.LabelName: "up",
					},
				},
			},
		},
		{
			name: "change",
			fields: fields{
				source: g,
				annotations: types.MetricAnnotations{
					ServiceName: "service-name",
				},
				labels: []*dto.LabelPair{
					{Name: &strJob, Value: &strValue},
				},
			},
			want: []types.MetricPoint{
				{
					Point: types.Point{Time: time.Unix(0, timestampMS*1e6), Value: floatValue1},
					Annotations: types.MetricAnnotations{
						ServiceName: "service-name",
					},
					Labels: map[string]string{
						types.LabelName: "up",
						strJob:          strValue,
						strMountpoint:   strHome,
					},
				},
				{
					Point: types.Point{Time: time.Unix(0, timestampMS*1e6), Value: floatValue2},
					Annotations: types.MetricAnnotations{
						ServiceName: "service-name",
					},
					Labels: map[string]string{
						types.LabelName: "up",
						strJob:          strValue,
					},
				},
			},
		},
		{
			name: "change-conflict",
			fields: fields{
				source: g,
				annotations: types.MetricAnnotations{
					ServiceName: "service-name",
				},
				labels: []*dto.LabelPair{
					{Name: &strMountpoint, Value: &strJob},
				},
			},
			want: []types.MetricPoint{
				{
					Point: types.Point{Time: time.Unix(0, timestampMS*1e6), Value: floatValue1},
					Annotations: types.MetricAnnotations{
						ServiceName: "service-name",
					},
					Labels: map[string]string{
						types.LabelName: "up",
						strMountpoint:   strJob,
					},
				},
				{
					Point: types.Point{Time: time.Unix(0, timestampMS*1e6), Value: floatValue2},
					Annotations: types.MetricAnnotations{
						ServiceName: "service-name",
					},
					Labels: map[string]string{
						types.LabelName: "up",
						strMountpoint:   strJob,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := labeledGatherer{
				source:      tt.fields.source,
				labels:      tt.fields.labels,
				annotations: tt.fields.annotations,
			}

			got, err := g.GatherPoints()
			if (err != nil) != tt.wantErr {
				t.Errorf("labeledGatherer.GatherPoints() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("labeledGatherer.GatherPoints() = %v, want %v", got, tt.want)
			}
		})
	}
}
