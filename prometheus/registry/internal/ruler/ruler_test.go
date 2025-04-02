package ruler

import (
	"testing"
	"time"

	"github.com/bleemeo/glouton/prometheus/matcher"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/rules"
	"google.golang.org/protobuf/proto"
)

func TestApplyRulesMFS(t *testing.T) {
	t0 := time.Date(2024, time.January, 3, 15, 0, 0, 0, time.Local) //nolint: gosmopolitan
	now := t0.Add(5 * time.Minute)                                  // 5min because we have 5 samples 1min apart each

	mfs := []*dto.MetricFamily{
		{
			Name: proto.String("ifInOctets"),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{
							Name:  proto.String("__name__"),
							Value: proto.String("ifInOctets"),
						},
						{
							Name:  proto.String("ifDescr"),
							Value: proto.String("LOOPBACK"),
						},
						{
							Name:  proto.String("ifType"),
							Value: proto.String("softwareLoopback"),
						},
					},
					Untyped:     &dto.Untyped{Value: proto.Float64(712799268)},
					TimestampMs: proto.Int64(t0.Add(1 * time.Minute).UnixMilli()),
				},
				{
					Label: []*dto.LabelPair{
						{
							Name:  proto.String("__name__"),
							Value: proto.String("ifInOctets"),
						},
						{
							Name:  proto.String("ifDescr"),
							Value: proto.String("LOOPBACK"),
						},
						{
							Name:  proto.String("ifType"),
							Value: proto.String("softwareLoopback"),
						},
					},
					Untyped:     &dto.Untyped{Value: proto.Float64(712896866)},
					TimestampMs: proto.Int64(t0.Add(2 * time.Minute).UnixMilli()),
				},
				{
					Label: []*dto.LabelPair{
						{
							Name:  proto.String("__name__"),
							Value: proto.String("ifInOctets"),
						},
						{
							Name:  proto.String("ifDescr"),
							Value: proto.String("LOOPBACK"),
						},
						{
							Name:  proto.String("ifType"),
							Value: proto.String("softwareLoopback"),
						},
					},
					Untyped:     &dto.Untyped{Value: proto.Float64(713012717)},
					TimestampMs: proto.Int64(t0.Add(3 * time.Minute).UnixMilli()),
				},
				{
					Label: []*dto.LabelPair{
						{
							Name:  proto.String("__name__"),
							Value: proto.String("ifInOctets"),
						},
						{
							Name:  proto.String("ifDescr"),
							Value: proto.String("LOOPBACK"),
						},
						{
							Name:  proto.String("ifType"),
							Value: proto.String("softwareLoopback"),
						},
					},
					Untyped:     &dto.Untyped{Value: proto.Float64(713098262)},
					TimestampMs: proto.Int64(t0.Add(4 * time.Minute).UnixMilli()),
				},
				{
					Label: []*dto.LabelPair{
						{
							Name:  proto.String("__name__"),
							Value: proto.String("ifInOctets"),
						},
						{
							Name:  proto.String("ifDescr"),
							Value: proto.String("LOOPBACK"),
						},
						{
							Name:  proto.String("ifType"),
							Value: proto.String("softwareLoopback"),
						},
					},
					Untyped:     &dto.Untyped{Value: proto.Float64(713201880)},
					TimestampMs: proto.Int64(t0.Add(5 * time.Minute).UnixMilli()),
				},
			},
		},
	}
	expectedMfs := []*dto.MetricFamily{
		{
			Name: proto.String("net_bits_recv"),
			Help: proto.String(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{
							Name:  proto.String("ifDescr"),
							Value: proto.String("LOOPBACK"),
						},
						{
							Name:  proto.String("ifType"),
							Value: proto.String("softwareLoopback"),
						},
					},
					Untyped:     &dto.Untyped{Value: proto.Float64(13420.4)},
					TimestampMs: proto.Int64(now.UnixMilli()),
				},
			},
		},
	}

	simpleRules := []types.SimpleRule{
		{
			TargetName:  "net_bits_recv",
			PromQLQuery: "rate(ifInOctets[4m])*8",
		},
	}

	rrules := make([]*rules.RecordingRule, len(simpleRules))

	for i, sr := range simpleRules {
		expr, err := parser.ParseExpr(sr.PromQLQuery)
		if err != nil {
			t.Fatalf("rule %s: %v", sr.TargetName, err)
		}

		rrules[i] = rules.NewRecordingRule(sr.TargetName, expr, nil)
	}

	ruler := New(rrules)
	ctx := t.Context()

	resultMfs := ruler.ApplyRulesMFS(ctx, now, mfs)

	ignoreOpts := cmpopts.IgnoreUnexported(dto.MetricFamily{}, dto.Metric{}, dto.LabelPair{}, dto.Untyped{})
	if diff := cmp.Diff(append(mfs, expectedMfs...), resultMfs, ignoreOpts); diff != "" {
		t.Fatalf("Unexpected result mfs:\n%v", diff)
	}
}

func Test_filterPointsForRules(t *testing.T) {
	points := []types.MetricPoint{
		{
			Labels: map[string]string{
				types.LabelName: "cpu_used",
			},
		},
		{
			Labels: map[string]string{
				types.LabelName: "disk_used",
				types.LabelItem: "/home",
			},
		},
		{
			Labels: map[string]string{
				types.LabelName: "disk_used",
				types.LabelItem: "/srv",
			},
		},
	}

	tests := []struct {
		name     string
		points   []types.MetricPoint
		matchers []matcher.Matchers
		want     []types.MetricPoint
	}{
		{
			name:     "nil-matchers",
			matchers: nil,
			points:   points,
			want:     []types.MetricPoint{},
		},
		{
			name:     "empty-matchers",
			matchers: []matcher.Matchers{},
			points:   points,
			want:     []types.MetricPoint{},
		},
		{
			name: "simple",
			matchers: []matcher.Matchers{
				{
					labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "cpu_used"),
				},
			},
			points: points,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "cpu_used",
					},
				},
			},
		},
		{
			name: "with-labels",
			matchers: []matcher.Matchers{
				{
					labels.MustNewMatcher(labels.MatchEqual, types.LabelName, "disk_used"),
					labels.MustNewMatcher(labels.MatchEqual, types.LabelItem, "/home"),
				},
			},
			points: points,
			want: []types.MetricPoint{
				{
					Labels: map[string]string{
						types.LabelName: "disk_used",
						types.LabelItem: "/home",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			points := make([]types.MetricPoint, len(tt.points))
			copy(points, tt.points)

			got := filterPointsForRules(points, tt.matchers)

			if diff := types.DiffMetricPoints(tt.want, got, false); diff != "" {
				t.Errorf("filterPointsForRules() mismatch (-want +got)\n%s", diff)
			}
		})
	}
}
