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

// Package registry package implement a dynamic collection of metrics sources
//
// It support both pushed metrics (using AddMetricPointFunction) and pulled
// metrics thought Collector or Gatherer
package registry

import (
	"context"
	"errors"
	"glouton/logger"
	"glouton/types"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/pkg/relabel"
)

const (
	pushedPointsCleanupInterval = 5 * time.Minute
	hookRetryDelay              = 2 * time.Minute
	baseJitter                  = 0
	defaultInterval             = 0
)

// RelabelHook is a hook called just before applying relabeling.
// This hook receive the full labels (including meta labels) and is allowed to
// modify/add/delete them. The result (which should still include meta labels) is
// processed by relabeling rules.
// If the hook return retryLater, it means that hook can not processed the labels currently
// and it registry should retry later. Points or gatherer associated will be dropped.
type RelabelHook func(labels map[string]string) (newLabel map[string]string, retryLater bool)

type pushFunction func(points []types.MetricPoint)

// AddMetricPoints implement PointAdder.
func (f pushFunction) PushPoints(points []types.MetricPoint) {
	f(points)
}

type metricFilter interface {
	FilterPoints(points []types.MetricPoint) []types.MetricPoint
	FilterFamilies(f []*dto.MetricFamily) []*dto.MetricFamily
}

// Registry is a dynamic collection of metrics sources.
//
// For the Prometheus metrics source, it mostly a wrapper around prometheus.Gatherers,
// but it allow to attach labels to each Gatherers.
// It also support pushed metrics.
//
// It is used by Glouton for two main purpose:
// * provide metrics on /metrics endpoints. For this gather of metrics is
//   (mostly) done when an HTTP query reach /metrics.
// * provide metrics to be sent to stored in local store + sent to Bleemeo. Here
//   gather of metrics is done periodically.
//
// It may contains two kind of metrics source:
// * Prometheus Gatherer. When adding a Gatherer additional labels could be added
// * "push" callback, which are function that use PushPoints() to add points to
//   the registry buffer. Points in this buffer are send when /metrics is queried.
//   Push callbacks are only called periodically by RunCollection, they are NOT called
//   on query to /metrics.
//
// Points send to local store (which forward them to Bleemeo) are:
// * any point pushed using PushPoints()
// * any points returned by a registered Gatherer when pushPoint option was set
//   when gatherer was registered.
type Registry struct {
	option Option

	l sync.Mutex

	condition       *sync.Cond
	countScrape     int
	countPushPoints int
	blockScrape     bool
	blockPushPoint  bool

	reschedules             []reschedule
	relabelConfigs          []*relabel.Config
	registrations           map[int]*registration
	registyPush             *prometheus.Registry
	internalRegistry        *prometheus.Registry
	pushedPoints            map[string]types.MetricPoint
	pushedPointsExpiration  map[string]time.Time
	lastPushedPointsCleanup time.Time
	currentDelay            time.Duration
	relabelHook             RelabelHook
}

type Option struct {
	PushPoint             types.PointPusher
	FQDN                  string
	GloutonPort           string
	MetricFormat          types.MetricFormat
	BlackboxSentScraperID bool
	Filter                metricFilter
}

type registration struct {
	l                         sync.Mutex
	originalExtraLabels       map[string]string
	originalInterval          time.Duration
	originalJitterSeed        uint64
	stopCallback              func()
	includedInMetricsEndpoint bool
	loop                      *scrapeLoop
	gatherer                  labeledGatherer
	relabelHookSkip           bool
	lastRebalHookRetry        time.Time
}

type reschedule struct {
	id    int
	reg   *registration
	runAt time.Time
}

// This type is used to have another Collecto() method private which only return pushed points.
type pushCollector Registry

var errToManyGatherers = errors.New("too many gatherers in the registry. Unable to find a new slot")

func getDefaultRelabelConfig() []*relabel.Config {
	return []*relabel.Config{
		{
			Action:       relabel.Replace,
			Separator:    ";",
			Regex:        relabel.MustNewRegexp("(.+)"),
			SourceLabels: model.LabelNames{types.LabelMetaBleemeoUUID},
			TargetLabel:  types.LabelInstanceUUID,
			Replacement:  "$1",
		},
		{
			Action:       relabel.Replace,
			Separator:    ";",
			Regex:        relabel.MustNewRegexp("(.+);(.+)"),
			SourceLabels: model.LabelNames{types.LabelMetaGloutonFQDN, types.LabelMetaPort},
			TargetLabel:  types.LabelInstance,
			Replacement:  "$1:$2",
		},
		{
			Action:       relabel.Replace,
			Separator:    ";",
			Regex:        relabel.MustNewRegexp("(.+);(.+);(.+)"),
			SourceLabels: model.LabelNames{types.LabelMetaGloutonFQDN, types.LabelMetaContainerName, types.LabelMetaPort},
			TargetLabel:  types.LabelInstance,
			Replacement:  "$1-$2:$3",
		},
		// when the metric comes from a probe, the 'scraper_uuid' label is the uuid of the agent. But only if scraper_send_uuid is enabled
		{
			Action:       relabel.Replace,
			Separator:    ";",
			Regex:        relabel.MustNewRegexp("(.+);(.+);yes"),
			SourceLabels: model.LabelNames{types.LabelMetaProbeServiceUUID, types.LabelMetaBleemeoUUID, types.LabelMetaSendScraperUUID},
			TargetLabel:  types.LabelScraperUUID,
			Replacement:  "$2",
		},
		// when the metric comes from a probe, the 'scraper' label is the value we usually put in the 'instance' label
		{
			Action:       relabel.Replace,
			Separator:    ";",
			Regex:        relabel.MustNewRegexp("(.+);(.+)"),
			SourceLabels: model.LabelNames{types.LabelMetaProbeServiceUUID, types.LabelInstance},
			TargetLabel:  types.LabelScraper,
			Replacement:  "$2",
		},
		// when the metric comes from a probe and the user specified it in the config file, the 'scraper' label is the user-provided string
		{
			Action:       relabel.Replace,
			Separator:    ";",
			Regex:        relabel.MustNewRegexp("(.+);(.+)"),
			SourceLabels: model.LabelNames{types.LabelMetaProbeServiceUUID, types.LabelMetaProbeScraperName},
			TargetLabel:  types.LabelScraper,
			Replacement:  "$2",
		},
		// when the metric comes from a probe, the 'instance_uuid' label is the uuid of the service watched
		{
			Action:       relabel.Replace,
			Regex:        relabel.MustNewRegexp("(.+)"),
			SourceLabels: model.LabelNames{types.LabelMetaBleemeoTargetAgentUUID},
			TargetLabel:  types.LabelInstanceUUID,
			Replacement:  "$1",
		},
		// when the metric comes from a probe, the 'instance' label is the target URI
		{
			Action:       relabel.Replace,
			Regex:        relabel.MustNewRegexp("(.+)"),
			SourceLabels: model.LabelNames{types.LabelMetaProbeTarget},
			TargetLabel:  types.LabelInstance,
			Replacement:  "$1",
		},
		{
			Action:       relabel.Replace,
			Separator:    ";",
			Regex:        relabel.MustNewRegexp("(.+)"),
			SourceLabels: model.LabelNames{types.LabelMetaSNMPTarget},
			TargetLabel:  types.LabelSNMPTarget,
			Replacement:  "$1",
		},
		{
			Action:       relabel.Replace,
			Separator:    ";",
			Regex:        relabel.MustNewRegexp("(.*)"),
			SourceLabels: model.LabelNames{types.LabelMetaContainerName},
			TargetLabel:  types.LabelContainerName,
			Replacement:  "$1",
		},
		{
			Action:       relabel.Replace,
			Separator:    ";",
			Regex:        relabel.MustNewRegexp("(.*)"),
			SourceLabels: model.LabelNames{types.LabelMetaScrapeJob},
			TargetLabel:  types.LabelScrapeJob,
			Replacement:  "$1",
		},
		{
			Action:       relabel.Replace,
			Separator:    ";",
			Regex:        relabel.MustNewRegexp("(.*)"),
			SourceLabels: model.LabelNames{types.LabelMetaScrapeInstance},
			TargetLabel:  types.LabelScrapeInstance,
			Replacement:  "$1",
		},
	}
}

func New(opt Option) (*Registry, error) {
	return &Registry{
		option: opt,
	}, nil
}

func (r *Registry) init() {
	r.l.Lock()

	if r.registrations != nil {
		r.l.Unlock()

		return
	}

	r.condition = sync.NewCond(&r.l)

	r.registrations = make(map[int]*registration)
	r.registyPush = prometheus.NewRegistry()
	r.internalRegistry = prometheus.NewRegistry()
	r.pushedPoints = make(map[string]types.MetricPoint)
	r.pushedPointsExpiration = make(map[string]time.Time)
	r.currentDelay = 10 * time.Second
	r.relabelConfigs = getDefaultRelabelConfig()

	r.l.Unlock()

	// Gather & Register shouldn't be done with the lock, as is will call
	// Describe and/or Collect which may take the lock

	_ = r.registyPush.Register((*pushCollector)(r))
}

func (r *Registry) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		if ctx.Err() != nil {
			break
		}

		delay := r.checkReschedule(ctx)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
		}
	}

	return ctx.Err()
}

// RegisterPushPointsCallback add a callback that should push points to the registry.
// This callback will be called for each collection period. It's mostly used to
// add Telegraf input (using glouton/collector).
func (r *Registry) RegisterPushPointsCallback(jitterSeed uint64, f func(context.Context, time.Time)) (int, error) {
	r.init()

	r.l.Lock()
	defer r.l.Unlock()

	reg := &registration{
		includedInMetricsEndpoint: false,
		originalJitterSeed:        jitterSeed,
	}
	r.setupGatherer(reg, pushGatherer{fun: f})

	return r.addRegistration(reg, true)
}

// UpdateRelabelHook change the hook used just before relabeling and wait for all pending metrics emission.
// When this function return, it's guaratee that all call to Option.PushPoint will use new labels.
// The hook is assumed to be idempotent, that is for a given labels input the result is the same.
// If the hook want break this idempotence, UpdateRelabelHook() should be re-called to force update of existings Gatherer.
func (r *Registry) UpdateRelabelHook(hook RelabelHook) {
	r.init()

	r.l.Lock()
	defer r.l.Unlock()

	r.blockScrape = true

	// Wait for scrapes to finish since it may sent points with old labels.
	// We use a two step lock (first scrapes, then also pushPoints) because
	// scrapes trigger update of pushed points so while runOnce we can't block
	// pushPoints
	for r.countScrape > 0 {
		r.condition.Wait()
	}

	r.blockPushPoint = true

	// Wait for all pending gorouting that may be sending points with old labels
	for r.countPushPoints > 0 {
		r.condition.Wait()
	}

	r.relabelHook = hook

	// Since the updated Agent ID may change metrics labels, drop pushed points
	r.pushedPoints = make(map[string]types.MetricPoint)
	r.pushedPointsExpiration = make(map[string]time.Time)

	// Update labels of all gatherers
	for _, reg := range r.registrations {
		reg.l.Lock()
		r.setupGatherer(reg, reg.gatherer.source)
		reg.l.Unlock()
	}

	r.blockScrape = false
	r.blockPushPoint = false

	r.condition.Broadcast()
}

// scrapeStart block until scraping is allowed.
func (r *Registry) scrapeStart() {
	r.l.Lock()

	for r.blockScrape {
		r.condition.Wait()
	}

	r.countScrape++
	r.l.Unlock()
}

// scrapeDone must be called for each srapeStart call.
func (r *Registry) scrapeDone() {
	r.l.Lock()
	r.countScrape--
	r.condition.Broadcast()
	r.l.Unlock()
}

// RegisterGatherer add a new gatherer to the list of metric sources.
//
// If pushPoints is true, the gathere will be periodic called and points will be forwarded to r.PushPoint.
// In the case, the period is interval. If interval is 0, the UpdateDelay value is used (default to 10 seconds).
// stopCallback is called when Unregister() is used.
// extraLabels add labels added. If a labels already exists, extraLabels take precedence.
func (r *Registry) RegisterGatherer(jitterSeed uint64, interval time.Duration, gatherer prometheus.Gatherer, stopCallback func(), extraLabels map[string]string, pushPoints bool) (int, error) {
	r.init()
	r.l.Lock()
	defer r.l.Unlock()

	reg := &registration{
		originalExtraLabels: extraLabels,
		originalJitterSeed:  jitterSeed,
		originalInterval:    interval,
		stopCallback:        stopCallback,
	}
	r.setupGatherer(reg, gatherer)

	return r.addRegistration(reg, pushPoints)
}

func (r *Registry) addRegistration(reg *registration, startLoop bool) (int, error) {
	id := 1

	_, ok := r.registrations[id]
	for ok {
		id++
		if id == 0 {
			return 0, errToManyGatherers
		}

		_, ok = r.registrations[id]
	}

	r.registrations[id] = reg

	if startLoop {
		if g, ok := reg.gatherer.source.(GathererWithScheduleUpdate); ok {
			g.SetScheduleUpdate(func(runAt time.Time) {
				r.scheduleUpdate(id, reg, runAt)
			})
		}

		reg.loop = startScrapeLoop(
			context.Background(),
			r.currentDelay,
			r.currentDelay*9/10,
			reg.originalJitterSeed,
			func(ctx context.Context, t0 time.Time) {
				r.scrapeStart()
				r.scrape(ctx, t0, reg)
				r.scrapeDone()
			},
		)
	}

	return id, nil
}

func (r *Registry) scheduleUpdate(id int, reg *registration, runAt time.Time) {
	r.l.Lock()
	defer r.l.Unlock()

	if reg2, ok := r.registrations[id]; !ok || reg2 != reg {
		return
	}

	r.reschedules = append(r.reschedules, reschedule{
		id:    id,
		reg:   reg,
		runAt: runAt,
	})

	sort.Slice(r.reschedules, func(i, j int) bool {
		return r.reschedules[i].runAt.Before(r.reschedules[j].runAt)
	})
}

func (r *Registry) checkReschedule(ctx context.Context) time.Duration {
	r.l.Lock()
	defer r.l.Unlock()

	firstInFuture := -1
	now := time.Now()

	for i, value := range r.reschedules {
		if value.runAt.After(now) {
			firstInFuture = i

			break
		}

		if reg2, ok := r.registrations[value.id]; !ok || reg2 != value.reg {
			continue
		}

		reg := value.reg

		go func() {
			ctx, cancel := context.WithTimeout(ctx, defaultGatherTimeout)
			defer cancel()

			r.scrape(ctx, now.Truncate(time.Second), reg)
		}()
	}

	if firstInFuture == -1 {
		r.reschedules = nil

		return 30 * time.Second
	}

	if firstInFuture > 0 {
		initialLength := len(r.reschedules)

		copy(r.reschedules[:initialLength-firstInFuture], r.reschedules[firstInFuture:])
		r.reschedules = r.reschedules[:initialLength-firstInFuture]
	}

	delta := time.Until(r.reschedules[0].runAt)
	if delta < time.Second {
		delta = time.Second
	}

	return delta
}

// Unregister remove a Gatherer or PushPointCallback from the list of metric sources.
func (r *Registry) Unregister(id int) bool {
	r.init()
	r.l.Lock()
	defer r.l.Unlock()

	reg, ok := r.registrations[id]

	if !ok {
		return false
	}

	if reg.loop != nil {
		r.l.Unlock()
		reg.loop.stop()
		r.l.Lock()
	}

	// Remove reference to original gatherer first, because some gatherer
	// stopCallback will rely on runtime.GC() to cleanup resource.
	delete(r.registrations, id)

	reg.gatherer.source = nil

	if reg.stopCallback != nil {
		reg.stopCallback()
	}

	return true
}

// Gather implements prometheus.Gatherer.
func (r *Registry) Gather() ([]*dto.MetricFamily, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultGatherTimeout)
	defer cancel()

	return r.GatherWithState(ctx, GatherState{})
}

// GatherWithState implements GathererGatherWithState.
func (r *Registry) GatherWithState(ctx context.Context, state GatherState) ([]*dto.MetricFamily, error) {
	r.init()
	r.l.Lock()

	gatherers := make(Gatherers, 0, len(r.registrations)+1)

	for _, reg := range r.registrations {
		if reg.relabelHookSkip {
			continue
		}

		gatherers = append(gatherers, reg.gatherer)
	}

	gatherers = append(gatherers, NonProbeGatherer{G: r.registyPush})

	r.l.Unlock()

	mfs, err := gatherers.GatherWithState(ctx, state)

	return mfs, err
}

type prefixLogger string

func (l prefixLogger) Println(v ...interface{}) {
	all := make([]interface{}, 0, len(v)+1)
	all = append(all, l)
	all = append(all, v...)

	logger.V(1).Println(all...)
}

// AddDefaultCollector adds the following collectors:
// GoCollector and ProcessCollector like the prometheus.DefaultRegisterer
// Internal registry which contains all glouton metrics.
func (r *Registry) AddDefaultCollector() {
	r.init()

	r.internalRegistry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	r.internalRegistry.MustRegister(collectors.NewGoCollector())

	_, _ = r.RegisterGatherer(baseJitter, defaultInterval, r.internalRegistry, nil, nil, r.option.MetricFormat == types.MetricFormatPrometheus)
}

// Exporter return an HTTP exporter.
func (r *Registry) Exporter() http.Handler {
	reg := prometheus.NewRegistry()
	handler := promhttp.InstrumentMetricHandler(reg, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		wrapper := NewGathererWithStateWrapper(req.Context(), r, r.option.Filter)

		state := GatherStateFromMap(req.URL.Query())

		wrapper.SetState(state)

		promhttp.HandlerFor(wrapper, promhttp.HandlerOpts{
			ErrorHandling: promhttp.ContinueOnError,
			ErrorLog:      prefixLogger("/metrics endpoint:"),
		}).ServeHTTP(w, req)
	}))
	_, _ = r.RegisterGatherer(baseJitter, defaultInterval, reg, nil, nil, r.option.MetricFormat == types.MetricFormatPrometheus)

	return handler
}

// WithTTL return a AddMetricPointFunction with TTL on pushed points.
func (r *Registry) WithTTL(ttl time.Duration) types.PointPusher {
	r.init()

	return pushFunction(func(points []types.MetricPoint) {
		r.pushPoint(points, ttl)
	})
}

// UpdateDelay change the delay between metric gather.
func (r *Registry) UpdateDelay(delay time.Duration) {
	r.init()
	r.l.Lock()
	defer r.l.Unlock()

	if r.currentDelay == delay {
		return
	}

	logger.V(2).Printf("Change metric collector delay to %v", delay)

	r.currentDelay = delay

	for _, reg := range r.registrations {
		if reg.originalInterval != 0 {
			continue
		}

		if reg.loop == nil {
			continue
		}

		r.l.Unlock()
		reg.loop.stop()
		r.l.Lock()

		reg.loop = startScrapeLoop(
			context.Background(),
			r.currentDelay,
			r.currentDelay*9/10,
			reg.originalJitterSeed,
			func(ctx context.Context, t0 time.Time) {
				r.scrapeStart()
				r.scrape(ctx, t0, reg)
				r.scrapeDone()
			},
		)
	}
}

func (r *Registry) scrape(ctx context.Context, t0 time.Time, reg *registration) {
	reg.l.Lock()
	defer reg.l.Unlock()

	if reg.relabelHookSkip && time.Since(reg.lastRebalHookRetry) > hookRetryDelay {
		r.l.Lock()
		r.setupGatherer(reg, reg.gatherer.source)
		r.l.Unlock()
	}

	if reg.relabelHookSkip {
		return
	}

	points, err := reg.gatherer.GatherPoints(ctx, t0, GatherState{QueryType: All, FromScrapeLoop: true, T0: t0})
	if err != nil {
		if len(points) == 0 {
			logger.Printf("Gather of metrics failed: %v", err)
		} else {
			// When there is points, log at lower level because we known that some gatherer always
			// fail on some setup. node_exporter may sent "node_rapl_package_joules_total" duplicated.
			logger.V(1).Printf("Gather of metrics failed, some metrics may be missing: %v", err)
		}
	}

	if len(points) > 0 && r.option.PushPoint != nil {
		r.option.PushPoint.PushPoints(points)
	}
}

func familiesToMetricPoints(now time.Time, families []*dto.MetricFamily) []types.MetricPoint {
	samples, err := expfmt.ExtractSamples(
		&expfmt.DecodeOptions{Timestamp: model.TimeFromUnixNano(now.UnixNano())},
		families...,
	)
	if err != nil {
		logger.Printf("Conversion of metrics failed, some metrics may be missing: %v", err)
	}

	result := make([]types.MetricPoint, len(samples))

	for i, sample := range samples {
		labels := make(map[string]string, len(sample.Metric))

		for k, v := range sample.Metric {
			labels[string(k)] = string(v)
		}

		result[i] = types.MetricPoint{
			Labels: labels,
			Point: types.Point{
				Time:  sample.Timestamp.Time(),
				Value: float64(sample.Value),
			},
		}
	}

	return result
}

// pushPoint add a new point to the list of pushed point with a specified TTL.
// As for AddMetricPointFunction, points should not be mutated after the call.
func (r *Registry) pushPoint(points []types.MetricPoint, ttl time.Duration) {
	r.l.Lock()

	for r.blockPushPoint {
		r.condition.Wait()
	}

	r.countPushPoints++

	now := time.Now()
	deadline := now.Add(ttl)

	n := 0

	for _, point := range points {
		var skip bool

		if r.option.MetricFormat == types.MetricFormatBleemeo {
			newLabelsMap := map[string]string{
				types.LabelName: point.Labels[types.LabelName],
			}

			if point.Annotations.BleemeoItem != "" {
				newLabelsMap[types.LabelItem] = point.Annotations.BleemeoItem
			}

			point.Labels = newLabelsMap
		} else {
			point.Labels = r.addMetaLabels(point.Labels)

			if r.relabelHook != nil {
				point.Labels, skip = r.relabelHook(point.Labels)
			}

			newLabels, _ := r.applyRelabel(point.Labels)
			point.Labels = newLabels.Map()
		}

		if !skip {
			key := types.LabelsToText(point.Labels)
			points[n] = point
			r.pushedPoints[key] = points[n]
			r.pushedPointsExpiration[key] = deadline
			n++
		}
	}

	if now.Sub(r.lastPushedPointsCleanup) > pushedPointsCleanupInterval {
		r.lastPushedPointsCleanup = now
		for key, expiration := range r.pushedPointsExpiration {
			if now.After(expiration) {
				delete(r.pushedPoints, key)
				delete(r.pushedPointsExpiration, key)
			}
		}
	}

	r.l.Unlock()

	if r.option.PushPoint != nil {
		r.option.PushPoint.PushPoints(points)
	}

	r.l.Lock()
	r.countPushPoints--
	r.condition.Broadcast()
	r.l.Unlock()
}

func (r *Registry) addMetaLabels(input map[string]string) map[string]string {
	result := make(map[string]string)
	for k, v := range input {
		result[k] = v
	}

	result[types.LabelMetaGloutonFQDN] = r.option.FQDN
	result[types.LabelMetaGloutonPort] = r.option.GloutonPort

	servicePort := result[types.LabelMetaServicePort]
	if servicePort == "" {
		servicePort = r.option.GloutonPort
	}

	result[types.LabelMetaPort] = servicePort

	if r.option.BlackboxSentScraperID {
		result[types.LabelMetaSendScraperUUID] = "yes"
	}

	return result
}

func (r *Registry) applyRelabel(input map[string]string) (labels.Labels, types.MetricAnnotations) {
	promLabels := labels.FromMap(input)

	annotations := types.MetricAnnotations{
		ServiceName: promLabels.Get(types.LabelMetaServiceName),
		ContainerID: promLabels.Get(types.LabelMetaContainerID),
	}

	// annotate the metric if it comes from a bleemeo target (probe, snmp)
	agentID := promLabels.Get(types.LabelMetaBleemeoTargetAgentUUID)
	if agentID != "" {
		annotations.BleemeoAgentID = agentID
	}

	if snmpTarget := promLabels.Get(types.LabelMetaSNMPTarget); snmpTarget != "" {
		annotations.SNMPTarget = snmpTarget
	}

	promLabels = relabel.Process(
		promLabels,
		r.relabelConfigs...,
	)

	result := make(labels.Labels, 0, len(promLabels))

	for _, l := range promLabels {
		if l.Name != types.LabelName && strings.HasPrefix(l.Name, model.ReservedLabelPrefix) {
			continue
		}

		if l.Value == "" {
			continue
		}

		result = append(result, l)
	}

	sort.Sort(result)

	return result, annotations
}

func (r *Registry) setupGatherer(reg *registration, source prometheus.Gatherer) {
	extraLabels := r.addMetaLabels(reg.originalExtraLabels)

	reg.relabelHookSkip = false

	if r.relabelHook != nil {
		extraLabels, reg.relabelHookSkip = r.relabelHook(extraLabels)
		reg.lastRebalHookRetry = time.Now()
	}

	promLabels, annotations := r.applyRelabel(extraLabels)
	g := newLabeledGatherer(source, promLabels, annotations)
	reg.gatherer = g
}

// Describe implement prometheus.Collector.
func (c *pushCollector) Describe(chan<- *prometheus.Desc) {
}

// Collect collect pushed points.
func (c *pushCollector) Collect(ch chan<- prometheus.Metric) {
	c.l.Lock()
	defer c.l.Unlock()

	now := time.Now()
	replacer := strings.NewReplacer(".", "_")

	c.lastPushedPointsCleanup = now

	for key, p := range c.pushedPoints {
		expiration := c.pushedPointsExpiration[key]
		if now.After(expiration) {
			delete(c.pushedPoints, key)
			delete(c.pushedPointsExpiration, key)

			continue
		}

		labelKeys := make([]string, 0)
		labelValues := make([]string, 0)

		for l, v := range p.Labels {
			if l != "__name__" {
				if !model.IsValidMetricName(model.LabelValue(l)) {
					l = replacer.Replace(l)
					if !model.IsValidMetricName(model.LabelValue(l)) {
						logger.V(2).Printf("label %#v is ignored since invalid for Prometheus", l)

						continue
					}
				}

				labelKeys = append(labelKeys, l)
				labelValues = append(labelValues, v)
			}
		}

		promMetric, err := prometheus.NewConstMetric(
			prometheus.NewDesc(p.Labels["__name__"], "", labelKeys, nil),
			prometheus.UntypedValue,
			p.Value,
			labelValues...,
		)
		if err != nil {
			logger.V(2).Printf("Ignoring metric %s due to %v", p.Labels["__name__"], err)

			continue
		}

		ch <- prometheus.NewMetricWithTimestamp(p.Time, promMetric)
	}
}
