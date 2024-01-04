package ruler

import (
	"context"
	"glouton/types"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/rules"
	"google.golang.org/protobuf/proto"
)

func TestApplyRulesMFS(t *testing.T) {
	t0 := time.Date(2024, time.January, 3, 15, 0, 0, 0, time.Local)
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
					Untyped: &dto.Untyped{Value: proto.Float64(13420.4)},
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
	ctx := context.Background()
	now := t0.Add(5 * time.Minute) // 5min because we have 5 samples 1min apart each

	resultMfs := ruler.ApplyRulesMFS(ctx, now, mfs)

	ignoreOpts := cmpopts.IgnoreUnexported(dto.MetricFamily{}, dto.Metric{}, dto.LabelPair{}, dto.Untyped{})
	if diff := cmp.Diff(append(mfs, expectedMfs...), resultMfs, ignoreOpts); diff != "" {
		t.Fatalf("Unexpected result mfs:\n%v", diff)
	}
}
