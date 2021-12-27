package ruler

import (
	"context"
	"glouton/logger"
	"glouton/prometheus/model"
	"glouton/store"
	"glouton/types"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/go-kit/log"
	"github.com/gogo/protobuf/proto"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/rules"
)

// SimpleRuler is a ruler that run Prometheus rules but only on one vector.
// This means we can't use any function working over time (like rate, avg_over_time, ...).
type SimpleRuler struct {
	l     sync.Mutex
	st    *store.Store
	query rules.QueryFunc
	rules []*rules.RecordingRule
}

func New(input []*rules.RecordingRule) *SimpleRuler {
	promLogger := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	engine := promql.NewEngine(promql.EngineOpts{
		Logger:             log.With(promLogger, "component", "query engine"),
		Reg:                nil,
		MaxSamples:         50000000,
		Timeout:            2 * time.Minute,
		ActiveQueryTracker: nil,
		LookbackDelta:      5 * time.Minute,
	})

	st := store.New(10 * time.Minute)

	return &SimpleRuler{
		st:    st,
		query: rules.EngineQueryFunc(engine, st),
		rules: input,
	}
}

func (r *SimpleRuler) ApplyRules(ctx context.Context, now time.Time, points []types.MetricPoint) []types.MetricPoint {
	r.l.Lock()
	defer r.l.Unlock()

	r.st.DropAllMetrics()
	r.st.PushPoints(ctx, points)

	for _, rule := range r.rules {
		vector, err := rule.Eval(ctx, now, r.query, nil)
		if err != nil {
			logger.V(2).Printf("rule %v failed: %v", rule.Query().String(), err)

			continue
		}

		for _, sample := range vector {
			points = append(points, types.MetricPoint{
				Point:  types.Point{Value: sample.V, Time: time.UnixMilli(sample.T)},
				Labels: sample.Metric.Map(),
			})
		}
	}

	return points
}

func (r *SimpleRuler) ApplyRulesMFS(ctx context.Context, now time.Time, mfs []*dto.MetricFamily) []*dto.MetricFamily {
	nameToIndex := make(map[string]int, len(mfs))

	for i, mf := range mfs {
		nameToIndex[mf.GetName()] = i
	}

	points := model.FamiliesToMetricPoints(now, mfs)

	r.l.Lock()
	defer r.l.Unlock()

	r.st.DropAllMetrics()
	r.st.PushPoints(ctx, points)

	for _, rule := range r.rules {
		vector, err := rule.Eval(ctx, now, r.query, nil)
		if err != nil {
			logger.V(2).Printf("rule %v failed: %v", rule.Query().String(), err)

			continue
		}

		for _, sample := range vector {
			name := sample.Metric.Get(types.LabelName)
			idx, ok := nameToIndex[name]

			if !ok {
				mfs = append(mfs, &dto.MetricFamily{
					Name: proto.String(name),
					Type: dto.MetricType_GAUGE.Enum(),
					Help: proto.String(""),
				})
				idx = len(mfs) - 1

				nameToIndex[name] = idx
			}

			lbls := make([]*dto.LabelPair, 0, len(sample.Metric)-1)

			for _, l := range sample.Metric {
				if l.Name == types.LabelName {
					continue
				}

				lbls = append(lbls, &dto.LabelPair{
					Name:  proto.String(l.Name),
					Value: proto.String(l.Value),
				})
			}

			mfs[idx].Metric = append(mfs[idx].Metric, &dto.Metric{
				Label: lbls,
				Gauge: &dto.Gauge{Value: proto.Float64(sample.V)},
			})
		}
	}

	sort.Slice(mfs, func(i, j int) bool {
		return mfs[i].GetName() < mfs[j].GetName()
	})

	return mfs
}
