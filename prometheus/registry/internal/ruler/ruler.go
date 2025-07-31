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

package ruler

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/matcher"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/store"
	"github.com/bleemeo/glouton/types"
	"github.com/prometheus/prometheus/model/labels"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/rules"
	"google.golang.org/protobuf/proto"
)

// Points older than pointsMaxAge are removed from the store.
const pointsMaxAge = 5 * time.Minute

// SimpleRuler is a ruler that run Prometheus rules.
type SimpleRuler struct {
	l        sync.Mutex
	st       *store.Store
	query    rules.QueryFunc
	rules    []*rules.RecordingRule
	matchers []matcher.Matchers
}

func New(input []*rules.RecordingRule) *SimpleRuler {
	matchers := make([]matcher.Matchers, 0, len(input))

	for _, rr := range input {
		matchers = append(matchers, matcher.MatchersFromQuery(rr.Query())...)
	}

	engine := promql.NewEngine(promql.EngineOpts{
		Logger:             logger.NewSlog().With("component", "query engine"),
		Reg:                nil,
		MaxSamples:         50000000,
		Timeout:            2 * time.Minute,
		ActiveQueryTracker: nil,
		LookbackDelta:      5 * time.Minute,
	})

	st := store.New("store for ruler", pointsMaxAge, pointsMaxAge)

	return &SimpleRuler{
		st:       st,
		query:    rules.EngineQueryFunc(engine, st),
		rules:    input,
		matchers: matchers,
	}
}

// filterPointsForRules only allow points used by one of the queries. Input points is modified.
func filterPointsForRules(points []types.MetricPoint, matchers []matcher.Matchers) []types.MetricPoint {
	i := 0

	for _, point := range points {
		if matcher.MatchesAny(point.Labels, matchers) {
			points[i] = point
			i++

			continue
		}
	}

	points = points[:i]

	return points
}

// ApplyRulesMFS applies the rules of this ruler and returns the input metric families with the new points.
// The returns metric families are not sorted.
func (r *SimpleRuler) ApplyRulesMFS(ctx context.Context, now time.Time, mfs []*dto.MetricFamily) []*dto.MetricFamily {
	if len(r.rules) == 0 {
		return mfs
	}

	nameToIndex := make(map[string]int, len(mfs))

	for i, mf := range mfs {
		nameToIndex[mf.GetName()] = i
	}

	points := model.FamiliesToMetricPoints(now, mfs, true)

	r.l.Lock()
	defer r.l.Unlock()

	r.st.RunOnce()

	points = filterPointsForRules(points, r.matchers)
	r.st.PushPoints(ctx, points)

	for _, rule := range r.rules {
		vector, err := rule.Eval(ctx, 0, now, r.query, nil, 100)
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
					Type: dto.MetricType_UNTYPED.Enum(),
					Help: proto.String(""),
				})
				idx = len(mfs) - 1

				nameToIndex[name] = idx
			}

			lbls := make([]*dto.LabelPair, 0, sample.Metric.Len()-1)

			sample.Metric.Range(func(l labels.Label) {
				if l.Name == types.LabelName {
					return
				}

				lbls = append(lbls, &dto.LabelPair{
					Name:  proto.String(l.Name),
					Value: proto.String(l.Value),
				})
			})

			mfs[idx].Metric = append(mfs[idx].GetMetric(), &dto.Metric{
				Label:       lbls,
				TimestampMs: proto.Int64(now.UnixMilli()),
				Untyped:     &dto.Untyped{Value: proto.Float64(sample.F)},
			})
		}
	}

	sort.Slice(mfs, func(i, j int) bool {
		return mfs[i].GetName() < mfs[j].GetName()
	})

	return mfs
}
