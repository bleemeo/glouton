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
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/pkg/relabel"
)

const (
	pushedPointsCleanupInterval = 5 * time.Minute
)

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
	Option

	l sync.Mutex

	pushUpdates     []func(time.Time)
	condition       *sync.Cond
	countRunOnce    int
	countPushPoints int
	blockRunOnce    bool
	blockPushPoint  bool

	metricLegacyGatherTime     prometheus.Gauge
	metricGatherBackgroundTime prometheus.Summary
	metricGatherExporterTime   prometheus.Summary
	relabelConfigs             []*relabel.Config
	registrations              map[int]registration
	registyPush                *prometheus.Registry
	internalRegistry           *prometheus.Registry
	pushedPoints               map[string]types.MetricPoint
	pushedPointsExpiration     map[string]time.Time
	lastPushedPointsCleanup    time.Time
	currentDelay               time.Duration
	updateDelayC               chan interface{}
	RulesCallback              func()
}

type Option struct {
	PushPoint             types.PointPusher
	FQDN                  string
	GloutonPort           string
	BleemeoAgentID        string
	MetricFormat          types.MetricFormat
	BlackboxSentScraperID bool
	Filter                metricFilter
}

type registration struct {
	originalExtraLabels map[string]string
	stopCallback        func()
	pushPoints          bool
	gatherer            labeledGatherer
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
			SourceLabels: model.LabelNames{types.LabelMetaProbeAgentUUID},
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

func (r *Registry) init() {
	r.l.Lock()

	if r.registrations != nil {
		r.l.Unlock()
		return
	}

	r.condition = sync.NewCond(&r.l)

	r.registrations = make(map[int]registration)
	r.registyPush = prometheus.NewRegistry()
	r.internalRegistry = prometheus.NewRegistry()
	r.pushedPoints = make(map[string]types.MetricPoint)
	r.pushedPointsExpiration = make(map[string]time.Time)
	r.currentDelay = 10 * time.Second
	r.updateDelayC = make(chan interface{})

	if r.MetricFormat == types.MetricFormatBleemeo {
		r.metricLegacyGatherTime = prometheus.NewGauge(prometheus.GaugeOpts{
			Help:      "Time of last metrics gather in seconds",
			Namespace: "",
			Subsystem: "",
			Name:      "agent_gather_time",
		})

		r.internalRegistry.MustRegister(r.metricLegacyGatherTime)
	} else if r.MetricFormat == types.MetricFormatPrometheus {
		r.metricGatherBackgroundTime = prometheus.NewSummary(prometheus.SummaryOpts{
			Help:      "Total metrics gathering time in seconds (either triggered by the /metrics exporter or the scheduled background task)",
			Namespace: "glouton",
			Subsystem: "gatherer",
			Name:      "execution_seconds",
			ConstLabels: prometheus.Labels{
				"trigger": "background",
			},
		})
		r.metricGatherExporterTime = prometheus.NewSummary(prometheus.SummaryOpts{
			Help:      "Total metrics gathering time in seconds (either triggered by the /metrics exporter or the scheduled background task)",
			Namespace: "glouton",
			Subsystem: "gatherer",
			Name:      "execution_seconds",
			ConstLabels: prometheus.Labels{
				"trigger": "exporter",
			},
		})

		r.internalRegistry.MustRegister(r.metricGatherBackgroundTime)
		r.internalRegistry.MustRegister(r.metricGatherExporterTime)
	}

	r.relabelConfigs = getDefaultRelabelConfig()

	r.l.Unlock()

	// Gather & Register shouldn't be done with the lock, as is will call
	// Describe and/or Collect which may take the lock

	_ = r.registyPush.Register((*pushCollector)(r))
}

// AddPushPointsCallback add a callback that should push points to the registry.
// This callback will be called for each collection period. It's mostly used to
// add Telegraf input (using glouton/collector).
func (r *Registry) AddPushPointsCallback(f func(time.Time)) {
	r.init()

	r.l.Lock()
	defer r.l.Unlock()

	r.pushUpdates = append(r.pushUpdates, f)
}

// UpdateBleemeoAgentID change the BleemeoAgentID and wait for all pending metrics emission.
// When this function return, it's guaratee that all call to r.PushPoint will use new labels.
func (r *Registry) UpdateBleemeoAgentID(ctx context.Context, agentID string) {
	r.init()

	r.l.Lock()
	defer r.l.Unlock()

	r.blockRunOnce = true

	// Wait for runOnce to finish since it may sent points with old labels.
	// We use a two step lock (first runOnce, then also pushPoints) because
	// runOnce trigger update of pushed points so while runOnce we can't block
	// pushPoints
	for r.countRunOnce > 0 {
		r.condition.Wait()
	}

	r.blockPushPoint = true

	// Wait for all pending gorouting that may be sending points with old labels
	for r.countPushPoints > 0 {
		r.condition.Wait()
	}

	r.BleemeoAgentID = agentID

	// Since the updated Agent ID may change metrics labels, drop pushed points
	r.pushedPoints = make(map[string]types.MetricPoint)
	r.pushedPointsExpiration = make(map[string]time.Time)

	// Update labels of all gatherers
	for id, reg := range r.registrations {
		reg := reg
		r.setupGatherer(&reg, reg.gatherer.source)
		r.registrations[id] = reg
	}

	r.blockRunOnce = false
	r.blockPushPoint = false

	r.condition.Broadcast()
}

// RegisterGatherer add a new gatherer to the list of metric sources.
//
// If pushPoints is true, the periodic gather run by RunCollection will forward points to
// r.PushPoint.
// stopCallback is called when UnregisterGatherer() is used.
// extraLabels add labels added. If a labels already exists, extraLabels take precedence.
func (r *Registry) RegisterGatherer(gatherer prometheus.Gatherer, stopCallback func(), extraLabels map[string]string, pushPoints bool) (int, error) {
	r.init()
	r.l.Lock()
	defer r.l.Unlock()

	id := 1

	_, ok := r.registrations[id]
	for ok {
		id++
		if id == 0 {
			return 0, errToManyGatherers
		}

		_, ok = r.registrations[id]
	}

	reg := registration{
		originalExtraLabels: extraLabels,
		stopCallback:        stopCallback,
		pushPoints:          pushPoints,
	}
	r.setupGatherer(&reg, gatherer)

	r.registrations[id] = reg

	return id, nil
}

// UnregisterGatherer remove a collector from the list of metric sources.
func (r *Registry) UnregisterGatherer(id int) bool {
	r.init()
	r.l.Lock()
	defer r.l.Unlock()

	reg, ok := r.registrations[id]

	if !ok {
		return false
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
	return r.GatherWithState(GatherState{})
}

// GatherWithState implements GathererGatherWithState.
func (r *Registry) GatherWithState(state GatherState) ([]*dto.MetricFamily, error) {
	r.init()
	r.l.Lock()

	gatherers := make(Gatherers, 0, len(r.registrations)+1)

	for _, reg := range r.registrations {
		gatherers = append(gatherers, reg.gatherer)
	}

	gatherers = append(gatherers, NonProbeGatherer{G: r.registyPush})

	r.l.Unlock()

	t0 := time.Now().Truncate(time.Millisecond)
	mfs, err := gatherers.GatherWithState(state)

	if r.metricGatherExporterTime != nil {
		r.metricGatherExporterTime.Observe(time.Since(t0).Seconds())
	}

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

	r.internalRegistry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	r.internalRegistry.MustRegister(prometheus.NewGoCollector())

	_, _ = r.RegisterGatherer(r.internalRegistry, nil, nil, r.MetricFormat == types.MetricFormatPrometheus)
}

// Exporter return an HTTP exporter.
func (r *Registry) Exporter() http.Handler {
	reg := prometheus.NewRegistry()
	handler := promhttp.InstrumentMetricHandler(reg, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		wrapper := NewGathererWithStateWrapper(r, r.Filter)

		state := GatherStateFromMap(req.URL.Query())
		// queries on /metrics will always be performed immediately, as we do not want to miss metrics run perodically
		state.NoTick = true

		wrapper.SetState(state)

		promhttp.HandlerFor(wrapper, promhttp.HandlerOpts{
			ErrorHandling: promhttp.ContinueOnError,
			ErrorLog:      prefixLogger("/metrics endpoint:"),
		}).ServeHTTP(w, req)
	}))
	_, _ = r.RegisterGatherer(reg, nil, nil, r.MetricFormat == types.MetricFormatPrometheus)

	return handler
}

// WithTTL return a AddMetricPointFunction with TTL on pushed points.
func (r *Registry) WithTTL(ttl time.Duration) types.PointPusher {
	r.init()

	return pushFunction(func(points []types.MetricPoint) {
		r.pushPoint(points, ttl)
	})
}

// RunCollection runs collection of all collector & gatherer at regular interval.
// The interval could be updated by call to UpdateDelay.
func (r *Registry) RunCollection(ctx context.Context) error {
	r.init()

	for ctx.Err() == nil {
		r.run(ctx)
	}

	return nil
}

// UpdateDelay change the delay between metric gather.
func (r *Registry) UpdateDelay(delay time.Duration) {
	r.init()
	r.l.Lock()

	if r.currentDelay == delay {
		r.l.Unlock()
		return
	}

	r.currentDelay = delay

	r.l.Unlock()

	logger.V(2).Printf("Change metric collector delay to %v", delay)

	r.updateDelayC <- nil
}

func (r *Registry) run(ctx context.Context) {
	r.l.Lock()
	currentDelay := r.currentDelay
	r.l.Unlock()

	sleepToAlign(currentDelay)

	ticker := time.NewTicker(currentDelay)
	defer ticker.Stop()

	for {
		r.runOnce()

		select {
		case <-r.updateDelayC:
			return
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (r *Registry) updatePushedPoints(t0 time.Time) {
	r.l.Lock()
	funcs := r.pushUpdates
	r.l.Unlock()

	var wg sync.WaitGroup

	wg.Add(len(funcs))

	for _, f := range funcs {
		f := f

		go func() {
			defer wg.Done()
			f(t0)
		}()
	}

	wg.Wait()
}

func (r *Registry) runOnce() time.Duration {
	r.l.Lock()

	for r.blockRunOnce {
		r.condition.Wait()
	}

	r.countRunOnce++

	gatherers := make([]labeledGatherer, 0, len(r.registrations))

	for _, reg := range r.registrations {
		if reg.pushPoints {
			gatherers = append(gatherers, reg.gatherer)
		}
	}

	r.l.Unlock()

	t0 := time.Now().Truncate(time.Millisecond)

	r.updatePushedPoints(t0)

	var points []types.MetricPoint

	var err error

	points, err = labeledGatherers(gatherers).GatherPoints(t0, GatherState{QueryType: All})
	if err != nil {
		if len(points) == 0 {
			logger.Printf("Gather of metrics failed: %v", err)
		} else {
			// When there is points, log at lower level because we known that some gatherer always
			// fail on some setup. node_exporter may sent "node_rapl_package_joules_total" duplicated.
			logger.V(1).Printf("Gather of metrics failed, some metrics may be missing: %v", err)
		}
	}

	gatherTime := time.Since(t0)

	if r.metricLegacyGatherTime != nil {
		r.metricLegacyGatherTime.Set(gatherTime.Seconds())
	} else {
		r.metricGatherBackgroundTime.Observe(gatherTime.Seconds())
	}

	if r.MetricFormat == types.MetricFormatBleemeo {
		var metric dto.Metric

		err := r.metricLegacyGatherTime.Write(&metric)
		if err != nil {
			logger.Printf("Gather of metrics failed, some metrics may be missing: %v", err)
		} else {
			value := metric.GetGauge().GetValue()
			points = append(points, types.MetricPoint{
				Point:  types.Point{Time: t0, Value: value},
				Labels: map[string]string{types.LabelName: "agent_gather_time"},
			})
		}
	}

	if len(points) > 0 {
		r.PushPoint.PushPoints(points)
	}

	if r.RulesCallback != nil {
		r.RulesCallback()
	}

	r.l.Lock()
	r.countRunOnce--
	r.condition.Broadcast()
	r.l.Unlock()

	return gatherTime
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

// sleep such are time.Now() is aligned on a multiple of interval.
func sleepToAlign(interval time.Duration) {
	now := time.Now()
	previousMultiple := now.Truncate(interval)

	if previousMultiple == now {
		return
	}

	nextMultiple := previousMultiple.Add(interval)

	time.Sleep(nextMultiple.Sub(now))
}

// pushPoint add a new point to the list of pushed point with a specified TTL.
// As for AddMetricPointFunction, points should not be mutated after the call.
func (r *Registry) pushPoint(points []types.MetricPoint, ttl time.Duration) {
	if len(points) > 0 {
		points = r.Filter.FilterPoints(points)
	}

	r.l.Lock()

	for r.blockPushPoint {
		r.condition.Wait()
	}

	r.countPushPoints++

	now := time.Now()
	deadline := now.Add(ttl)

	for i, point := range points {
		var newLabelsMap map[string]string

		if r.MetricFormat == types.MetricFormatBleemeo {
			newLabelsMap = map[string]string{
				types.LabelName: point.Labels[types.LabelName],
			}

			if point.Annotations.BleemeoItem != "" {
				newLabelsMap[types.LabelItem] = point.Annotations.BleemeoItem
			}
		} else {
			extraLabels := r.addMetaLabels(point.Labels)
			newLabels, _ := r.applyRelabel(extraLabels)
			newLabelsMap = newLabels.Map()
		}

		key := types.LabelsToText(newLabelsMap)
		points[i].Labels = newLabelsMap
		r.pushedPoints[key] = points[i]
		r.pushedPointsExpiration[key] = deadline
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

	if r.PushPoint != nil {
		r.PushPoint.PushPoints(points)
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

	result[types.LabelMetaGloutonFQDN] = r.FQDN
	result[types.LabelMetaGloutonPort] = r.GloutonPort

	if r.BleemeoAgentID != "" {
		result[types.LabelMetaBleemeoUUID] = r.BleemeoAgentID
		if r.BlackboxSentScraperID {
			result[types.LabelMetaSendScraperUUID] = "yes"
		}
	}

	servicePort := result[types.LabelMetaServicePort]
	if servicePort == "" {
		servicePort = r.GloutonPort
	}

	result[types.LabelMetaPort] = servicePort

	return result
}

func (r *Registry) applyRelabel(input map[string]string) (labels.Labels, types.MetricAnnotations) {
	promLabels := labels.FromMap(input)

	annotations := types.MetricAnnotations{
		ServiceName: promLabels.Get(types.LabelMetaServiceName),
		ContainerID: promLabels.Get(types.LabelMetaContainerID),
	}

	// annotate the metric if it comes from a probe
	agentID := promLabels.Get(types.LabelMetaProbeAgentUUID)
	if agentID != "" {
		annotations.BleemeoAgentID = agentID
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
	promLabels, annotations := r.applyRelabel(extraLabels)
	g := newLabeledGatherer(source, promLabels, annotations)
	reg.gatherer = g
}

// Describe implement prometheus.Collector.
func (c *pushCollector) Describe(chan<- *prometheus.Desc) {
}

// Collect collect non-pushed points from all registered collectors.
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
