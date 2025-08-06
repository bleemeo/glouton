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

package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/prometheus/registry/internal/ruler"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	prometheusModel "github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"google.golang.org/protobuf/proto"
)

const defaultGatherTimeout = 10 * time.Second

var (
	errIncorrectType       = errors.New("incorrect type for gathered metric family")
	errGatherOnNilGatherer = errors.New("GatherWithState called on nil gatherer")
)

type queryType int

const (
	// NoProbe is the default value. Probes can be very costly as it involves network calls, hence it is disabled by default.
	NoProbe queryType = iota
	// OnlyProbes specifies we only want data from the probes.
	OnlyProbes
	// All specifies we want all the data, including probes.
	All
	// FromStore use the store (queryable) to get recent stored data.
	FromStore
)

// GatherState is an argument given to gatherers that support it. It allows us to give extra information
// to gatherers. Due to the way such objects are constructed when no argument is supplied (when calling
// Gather() on a GathererWithState, most of the time Gather() will directly call GatherWithState(GatherState{}),
// please make sure that default values are sensible. For example, NoProbe *must* be the default queryType, as
// we do not want queries on /metrics to always probe the collectors by default).
type GatherState struct {
	QueryType queryType
	// FromScrapeLoop tells whether the gathering is done by the periodic scrape loop or for /metrics endpoint
	FromScrapeLoop bool
	T0             time.Time
	NoFilter       bool
	// HintMetricFilter is an optional filter that gather could use to skip metrics that would be filtered later.
	// Nothing is mandatory: the HintMetricFilter could be nil and the gatherer could ignore HintMetricFilter even if non-nil.
	// If used the filter should be applied after any label alteration.
	HintMetricFilter func(lbls labels.Labels) bool
}

func (s GatherState) Now() time.Time {
	if s.T0.IsZero() {
		return time.Now()
	}

	return s.T0
}

// GatherStateFromMap creates a GatherState from a state passed as a map.
func GatherStateFromMap(params map[string][]string) GatherState {
	state := GatherState{T0: time.Now()}

	// TODO: add this in some user-facing documentation
	if _, includeProbes := params["includeMonitors"]; includeProbes {
		state.QueryType = All
	}

	// TODO: add this in some user-facing documentation
	if _, excludeMetrics := params["onlyMonitors"]; excludeMetrics {
		state.QueryType = OnlyProbes
	}

	if _, noFilter := params["noFilter"]; noFilter {
		state.NoFilter = true
	}

	if _, fromStore := params["fromStore"]; fromStore {
		state.QueryType = FromStore
	}

	return state
}

// GathererWithState is a generalization of prometheus.Gather.
type GathererWithState interface {
	GatherWithState(ctx context.Context, state GatherState) ([]*dto.MetricFamily, error)
}

// GathererWithScheduleUpdate is a Gatherer that had a ScheduleUpdate (like Probe gatherer).
// The ScheduleUpdate could be used to trigger an additional gather earlier than default scrape interval.
type GathererWithScheduleUpdate interface {
	SetScheduleUpdate(scheduleUpdate func(runAt time.Time))
}

// GathererWithStateWrapper is a wrapper around GathererWithState that allows to specify a state to forward
// to the wrapped gatherer when the caller does not know about GathererWithState and uses raw Gather().
// The main use case is the /metrics HTTP endpoint, where we want to be able to gather() only some metrics
// (e.g. all metrics/only probes/no probes).
// In the prometheus exporter endpoint, when receiving an request, the (user-provided)
// HTTP handler will:
// - create a new wrapper instance, generate a GatherState accordingly, and call wrapper.setState(newState).
// - pass the wrapper to a new prometheus HTTP handler.
// - when Gather() is called upon the wrapper by prometheus, the wrapper calls GathererWithState(newState)
// on its internal gatherer.
// GatherWithState also contains the metrics allow/deny list in order to sync the metrics on /metric
// with the metrics sent to the bleemeo platform.
type GathererWithStateWrapper struct {
	gatherState GatherState
	gatherer    GathererWithState
	ctx         context.Context //nolint:containedctx
}

// NewGathererWithStateWrapper creates a new wrapper around GathererWithState.
func NewGathererWithStateWrapper(ctx context.Context, g GathererWithState) *GathererWithStateWrapper {
	return &GathererWithStateWrapper{gatherer: g, ctx: ctx}
}

// SetState updates the state the wrapper will provide to its internal gatherer when called.
func (w *GathererWithStateWrapper) SetState(state GatherState) {
	w.gatherState = state
}

// Gather implements prometheus.Gatherer for GathererWithStateWrapper.
func (w *GathererWithStateWrapper) Gather() ([]*dto.MetricFamily, error) {
	res, err := w.gatherer.GatherWithState(w.ctx, w.gatherState)
	if err != nil {
		logger.V(2).Printf("Error during gather on /metrics: %v", err)
	}

	return res, err
}

type gatherModifier func(mfs []*dto.MetricFamily, gatherError error) []*dto.MetricFamily

// wrappedGatherer wraps a gatherer to apply Registry change and apply RegistrationOption.
// For example, it will add provided labels to all metrics and/or change timestamps.
type wrappedGatherer struct {
	labels []*dto.LabelPair
	opt    RegistrationOption
	ruler  *ruler.SimpleRuler
	source prometheus.Gatherer

	l       sync.Mutex
	closed  bool
	running bool
	cond    *sync.Cond
}

func newWrappedGatherer(g prometheus.Gatherer, extraLabels labels.Labels, opt RegistrationOption) *wrappedGatherer {
	lbls := make([]*dto.LabelPair, 0, extraLabels.Len())

	extraLabels.Range(func(l labels.Label) {
		if !strings.HasPrefix(l.Name, prometheusModel.ReservedLabelPrefix) {
			lbls = append(lbls, &dto.LabelPair{
				Name:  &l.Name,
				Value: &l.Value,
			})
		}
	})

	var sruler *ruler.SimpleRuler

	if len(opt.rrules) > 0 {
		sruler = ruler.New(opt.rrules)
	}

	wrap := &wrappedGatherer{
		source: g,
		labels: lbls,
		ruler:  sruler,
		opt:    opt,
	}
	wrap.cond = sync.NewCond(&wrap.l)

	return wrap
}

func dtoLabelToMap(lbls []*dto.LabelPair) map[string]string {
	result := make(map[string]string, len(lbls))
	for _, l := range lbls {
		result[l.GetName()] = l.GetValue()
	}

	return result
}

// Gather implements prometheus.Gather.
func (g *wrappedGatherer) Gather() ([]*dto.MetricFamily, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultGatherTimeout)
	defer cancel()

	return g.GatherWithState(ctx, GatherState{})
}

// GatherWithState implements GathererWithState.
func (g *wrappedGatherer) GatherWithState(ctx context.Context, state GatherState) ([]*dto.MetricFamily, error) {
	g.l.Lock()
	defer g.l.Unlock()

	for g.running {
		g.cond.Wait()
	}

	if g.closed || g.source == nil {
		// Make sure to signal before exiting. If two gorouting were blocked on
		// the condition, we need to make sure the other one get wake-up.
		g.cond.Signal()

		return nil, errGatherOnNilGatherer
	}

	// do not collect non-probes metrics when the user only wants probes
	if _, probe := g.source.(*ProbeGatherer); !probe && state.QueryType == OnlyProbes {
		// Make sure to signal before exiting. If two gorouting were blocked on
		// the condition, we need to make sure the other one get wake-up.
		g.cond.Signal()

		return nil, nil
	}

	var (
		mfs []*dto.MetricFamily
		err error
	)

	now := time.Now().Truncate(time.Second)

	if !state.T0.IsZero() {
		now = state.T0
	}

	g.running = true
	g.l.Unlock()

	if cg, ok := g.source.(GathererWithState); ok {
		mfs, err = cg.GatherWithState(ctx, state)
	} else {
		mfs, err = g.source.Gather()
	}

	g.l.Lock()
	g.running = false
	g.cond.Signal()

	if g.ruler != nil {
		mfs = g.ruler.ApplyRulesMFS(ctx, now, mfs)
	}

	if g.opt.GatherModifier != nil {
		mfs = g.opt.GatherModifier(mfs, err)
	}

	if g.opt.CompatibilityNameItem {
		model.FamiliesToNameAndItem(mfs)
	}

	if len(g.labels) == 0 {
		return mfs, err
	}

	for _, mf := range mfs {
		for i, m := range mf.GetMetric() {
			m.Label = mergeLabelsDTO(m.GetLabel(), g.labels)
			mf.Metric[i] = m
		}
	}

	if !g.opt.HonorTimestamp {
		forcedTimestamp := now.UnixMilli()

		// CallForMetricsEndpoint is currently not implemented and it's
		// always enable on Gatherer (e.g. we always call the Gather() method).
		if g.opt.CallForMetricsEndpoint || true {
			// If the callback is used for all invocation of /metrics,
			// we can use "no timestamp" since metric points will be more recent
			// data.
			forcedTimestamp = 0
		}

		for _, mf := range mfs {
			for _, m := range mf.GetMetric() {
				if forcedTimestamp == 0 {
					m.TimestampMs = nil
				} else {
					m.TimestampMs = proto.Int64(forcedTimestamp)
				}
			}
		}
	}

	return mfs, err
}

// close waits for the current gather to finish and deletes the gatherer.
func (g *wrappedGatherer) close() {
	g.l.Lock()
	defer g.l.Unlock()

	g.closed = true
}

func (g *wrappedGatherer) getSource() prometheus.Gatherer {
	return g.source
}

// mergeLabels merge two sorted list of labels. In case of name conflict, value from b wins.
func mergeLabels(a labels.Labels, b []*dto.LabelPair) labels.Labels {
	result := a.Map()

	for _, l := range b {
		result[l.GetName()] = l.GetValue()
	}

	return labels.FromMap(result)
}

// mergeLabelsDTO merge two sorted list of labels. In case of name conflict, value from b wins.
func mergeLabelsDTO(a []*dto.LabelPair, b []*dto.LabelPair) []*dto.LabelPair {
	result := make([]*dto.LabelPair, 0, len(a)+len(b))
	aIndex := 0

	for _, bLabel := range b {
		for aIndex < len(a) && a[aIndex].GetName() < bLabel.GetName() {
			result = append(result, a[aIndex])
			aIndex++
		}

		if aIndex < len(a) && a[aIndex].GetName() == bLabel.GetName() {
			aIndex++
		}

		result = append(result, bLabel)
	}

	for aIndex < len(a) {
		result = append(result, a[aIndex])
		aIndex++
	}

	return result
}

type sliceGatherer []*dto.MetricFamily

// Gather implements Gatherer.
func (s sliceGatherer) Gather() ([]*dto.MetricFamily, error) {
	return s, nil
}

// mergeMFS take a list of metric families where two entry might be from the same family.
// When two entry have the same family, the type must be the same.
// The help text could be different, only the first will be kept.
func mergeMFS(mfs []*dto.MetricFamily) ([]*dto.MetricFamily, error) {
	metricFamiliesByName := map[string]*dto.MetricFamily{}

	var errs prometheus.MultiError

	for _, mf := range mfs {
		existingMF, exists := metricFamiliesByName[mf.GetName()]

		if exists {
			switch {
			case existingMF.GetType() == mf.GetType():
				// Nothing to do.
			case existingMF.GetType() == dto.MetricType_UNTYPED:
				existingMF.Type = mf.Type
				for i, metric := range existingMF.GetMetric() {
					model.FixType(metric, *mf.GetType().Enum())
					existingMF.Metric[i] = metric
				}
			case mf.GetType() == dto.MetricType_UNTYPED:
				mf.Type = existingMF.Type
				for i, metric := range mf.GetMetric() {
					model.FixType(metric, *existingMF.GetType().Enum())
					mf.Metric[i] = metric
				}
			case existingMF.GetType() != mf.GetType():
				errs = append(errs, fmt.Errorf(
					"%w: %s has type %s but should have %s", errIncorrectType,
					mf.GetName(), mf.GetType(), existingMF.GetType(),
				))

				continue
			}
		} else {
			existingMF = &dto.MetricFamily{}
			existingMF.Name = proto.String(mf.GetName())
			existingMF.Help = proto.String(mf.GetHelp())
			existingMF.Type = mf.Type
			metricFamiliesByName[mf.GetName()] = existingMF
		}

		existingMF.Metric = append(existingMF.GetMetric(), mf.GetMetric()...)
	}

	result := make([]*dto.MetricFamily, 0, len(metricFamiliesByName))

	for _, f := range metricFamiliesByName {
		result = append(result, f)
	}

	return result, errs.MaybeUnwrap()
}
