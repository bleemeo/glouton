// Copyright 2015-2022 Bleemeo
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
	"encoding/json"
	"errors"
	"fmt"
	"glouton/logger"
	gloutonModel "glouton/prometheus/model"
	"glouton/prometheus/registry/internal/renamer"
	"glouton/types"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/relabel"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/rules"
	"github.com/prometheus/prometheus/storage"
)

const (
	pushedPointsCleanupInterval = 5 * time.Minute
	hookRetryDelay              = 2 * time.Minute
	relabelTimeout              = 20 * time.Second
	baseJitter                  = 0
	defaultInterval             = 0
)

// RelabelHook is a hook called just before applying relabeling.
// This hook receive the full labels (including meta labels) and is allowed to
// modify/add/delete them. The result (which should still include meta labels) is
// processed by relabeling rules.
// If the hook return retryLater, it means that hook can not processed the labels currently
// and it registry should retry later. Points or gatherer associated will be dropped.
type RelabelHook func(ctx context.Context, labels map[string]string) (newLabel map[string]string, retryLater bool)

var errInvalidName = errors.New("invalid metric name or label name")

type pushFunction func(ctx context.Context, points []types.MetricPoint)

// AddMetricPoints implement PointAdder.
func (f pushFunction) PushPoints(ctx context.Context, points []types.MetricPoint) {
	f(ctx, points)
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
//   - provide metrics on /metrics endpoints. For this gather of metrics is
//     (mostly) done when an HTTP query reach /metrics.
//   - provide metrics to be sent to stored in local store + sent to Bleemeo. Here
//     gather of metrics is done periodically.
//
// It may contains two kind of metrics source:
//   - Prometheus Gatherer. When adding a Gatherer additional labels could be added
//   - "push" callback, which are function that use PushPoints() to add points to
//     the registry buffer. Points in this buffer are send when /metrics is queried.
//     Push callbacks are only called periodically by RunCollection, they are NOT called
//     on query to /metrics.
//
// Points send to local store (which forward them to Bleemeo) are:
//   - any point pushed using PushPoints()
//   - any points returned by a registered Gatherer when pushPoint option was set
//     when gatherer was registered.
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
	internalRegistry        *prometheus.Registry
	pushedPoints            map[string]types.MetricPoint
	pushedPointsExpiration  map[string]time.Time
	lastPushedPointsCleanup time.Time
	currentDelay            time.Duration
	relabelHook             RelabelHook
	renamer                 *renamer.Renamer
}

type Option struct {
	PushPoint             types.PointPusher
	ThresholdHandler      ThresholdHandler
	Queryable             storage.Queryable
	FQDN                  string
	GloutonPort           string
	MetricFormat          types.MetricFormat
	BlackboxSendScraperID bool
	Filter                metricFilter
}

type RegistrationOption struct {
	Description  string
	JitterSeed   uint64
	Interval     time.Duration
	MinInterval  time.Duration
	Timeout      time.Duration
	StopCallback func()
	// ExtraLabels are labels added. If a labels already exists, extraLabels take precedence.
	ExtraLabels map[string]string
	// NoLabelsAlteration disable (most) alteration of labels. It don't apply to PushPoints.
	// Meta labels (starting with __) are still dropped and (if applicable) converted to annotations.
	NoLabelsAlteration bool
	// DisablePeriodicGather skip the periodic calls which forward gathered points to r.PushPoint.
	// The periodic call use the Interval. When Interval is 0, the dynamic interval set by UpdateDelay is used.
	DisablePeriodicGather bool
	Rules                 []SimpleRule
	rrules                []*rules.RecordingRule
}

type AppenderRegistrationOption struct {
	// CallForMetricsEndpoint indicate whether the callback must be called for /metrics or
	// cached result from last periodic is used.
	CallForMetricsEndpoint bool
	// HonorTimestamp indicate whether timestamp given to Appender are used or if a timestamp
	// decided by the Registry is used. Using the timestamp of the registry is preferred as its more stable.
	HonorTimestamp bool
}

type AppenderCallback interface {
	// Collect collects point and write them into Appender. The appender must not be used once Collect returned.
	// If you omit to Commit() on the appender, it will be automatically done when Collect return without error.
	Collect(ctx context.Context, app storage.Appender) error
}

type ThresholdHandler interface {
	ApplyThresholds(points []types.MetricPoint) (newPoints []types.MetricPoint, statusPoints []types.MetricPoint)
}

func (opt *RegistrationOption) buildRules() error {
	rrules := make([]*rules.RecordingRule, 0, len(opt.Rules))

	for _, r := range opt.Rules {
		expr, err := parser.ParseExpr(r.PromQLQuery)
		if err != nil {
			return err
		}

		rr := rules.NewRecordingRule(r.TargetName, expr, nil)
		rrules = append(rrules, rr)
	}

	opt.rrules = rrules

	return nil
}

// SimpleRule is a PromQL run on output from the Gatherer.
// It's similar to a recording rule, but it's not able to use historical data and can
// only works on latest point (so no rate, avg_over_time, ...).
type SimpleRule struct {
	TargetName  string
	PromQLQuery string
}

func (opt *RegistrationOption) String() string {
	hasStop := "without stop callback"
	if opt.StopCallback != nil {
		hasStop = "with stop callback"
	}

	return fmt.Sprintf(
		"\"%s\" with labels %v; interval=%v, seed=%d, timeout=%v, %s",
		opt.Description, opt.ExtraLabels, opt.Interval, opt.JitterSeed, opt.Timeout, hasStop,
	)
}

type registration struct {
	l                  sync.Mutex
	option             RegistrationOption
	loop               *scrapeLoop
	lastScrape         time.Time
	lastScrapeDuration time.Duration
	gatherer           *labeledGatherer
	annotations        types.MetricAnnotations
	relabelHookSkip    bool
	lastRebalHookRetry time.Time
}

type reschedule struct {
	ID    int
	Reg   *registration
	RunAt time.Time
}

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
		// when the metric comes from a probe, the 'scraper' label is the value we usually put in the 'instance' label without port
		{
			Action:       relabel.Replace,
			Separator:    ";",
			Regex:        relabel.MustNewRegexp("(.+);([^:]+)(:\\d+)?"),
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
	reg := &Registry{
		option: opt,
	}

	reg.init()

	return reg, nil
}

func (r *Registry) init() {
	r.l.Lock()

	if r.registrations != nil {
		r.l.Unlock()

		return
	}

	r.condition = sync.NewCond(&r.l)

	r.registrations = make(map[int]*registration)
	r.internalRegistry = prometheus.NewRegistry()
	r.pushedPoints = make(map[string]types.MetricPoint)
	r.pushedPointsExpiration = make(map[string]time.Time)
	r.currentDelay = 10 * time.Second
	r.relabelConfigs = getDefaultRelabelConfig()
	r.renamer = renamer.LoadRules(renamer.GetDefaultRules())

	r.l.Unlock()
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

	// Stop all scrape loops.
	for id := range r.registrations {
		r.Unregister(id)
	}

	return ctx.Err()
}

// RegisterPushPointsCallback add a callback that should push points to the registry.
// This callback will be called for each collection period. It's mostly used to
// add Telegraf input (using glouton/collector).
// Deprecated: use RegisterAppenderCallback.
// Note: before being able to drop pushpoint & registerpushpoint, we likely need:
//   - support for "GathererWithScheduleUpdate-like" on RegisterAppenderCallback (needed by service check, when they trigger check on TCP close)
//   - support for conversion of all annotation to meta-label and vise-vera (model/convert.go)
func (r *Registry) RegisterPushPointsCallback(opt RegistrationOption, f func(context.Context, time.Time)) (int, error) {
	return r.registerPushPointsCallback(opt, f)
}

func (r *Registry) registerPushPointsCallback(opt RegistrationOption, f func(context.Context, time.Time)) (int, error) {
	r.init()

	if err := opt.buildRules(); err != nil {
		return 0, err
	}

	r.l.Lock()
	defer r.l.Unlock()

	reg := &registration{option: opt}
	r.setupGatherer(reg, pushGatherer{fun: f})

	return r.addRegistration(reg)
}

// RegisterAppenderCallback add a callback that use an Appender to write points to the registry.
func (r *Registry) RegisterAppenderCallback(
	opt RegistrationOption,
	appOpt AppenderRegistrationOption,
	cb AppenderCallback,
) (int, error) {
	r.init()

	if err := opt.buildRules(); err != nil {
		return 0, err
	}

	r.l.Lock()
	defer r.l.Unlock()

	reg := &registration{option: opt}
	r.setupGatherer(reg, &appenderGatherer{cb: cb, opt: appOpt})

	return r.addRegistration(reg)
}

// RegisterInput uses a Telegraph input to write points to the registry.
func (r *Registry) RegisterInput(
	opt RegistrationOption,
	input telegraf.Input,
) (int, error) {
	r.init()

	if err := opt.buildRules(); err != nil {
		return 0, err
	}

	// Initialize the input.
	if si, ok := input.(telegraf.Initializer); ok {
		if err := si.Init(); err != nil {
			return 0, err
		}
	}

	// Start the input.
	if si, ok := input.(telegraf.ServiceInput); ok {
		if err := si.Start(nil); err != nil {
			return 0, err
		}
	}

	previousStopCallback := opt.StopCallback

	// Add stop callback to stop the input.
	opt.StopCallback = func() {
		if si, ok := input.(telegraf.ServiceInput); ok {
			si.Stop()
		}

		// Keep previous stop callback.
		if previousStopCallback != nil {
			previousStopCallback()
		}
	}

	r.l.Lock()
	defer r.l.Unlock()

	reg := &registration{
		option: opt,
	}
	r.setupGatherer(reg, newInputGatherer(input))

	return r.addRegistration(reg)
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
		r.setupGatherer(reg, reg.gatherer.getSource())
		reg.l.Unlock()
	}

	r.blockScrape = false
	r.blockPushPoint = false

	r.condition.Broadcast()
}

func (r *Registry) DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("metrics.txt")
	if err != nil {
		return err
	}

	if err := r.writeMetrics(ctx, file, false); err != nil {
		return err
	}

	file, err = archive.Create("metrics-filtered.txt")
	if err != nil {
		return err
	}

	if err := r.writeMetrics(ctx, file, true); err != nil {
		return err
	}

	file, err = archive.Create("metrics-self.txt")
	if err != nil {
		return err
	}

	if err := r.writeMetricsSelf(file); err != nil {
		return err
	}

	if err := r.diagnosticState(archive); err != nil {
		return err
	}

	r.l.Lock()
	defer r.l.Unlock()

	file, err = archive.Create("scrape-loop.txt")
	if err != nil {
		return err
	}

	type regWithID struct {
		*registration
		id int
	}

	var (
		loopRegistration   []regWithID
		noloopRegistration []regWithID
	)

	for id, reg := range r.registrations {
		if reg.loop != nil {
			loopRegistration = append(loopRegistration, regWithID{id: id, registration: reg})
		} else {
			noloopRegistration = append(noloopRegistration, regWithID{id: id, registration: reg})
		}
	}

	sort.Slice(loopRegistration, func(i, j int) bool {
		return loopRegistration[i].id < loopRegistration[j].id
	})

	sort.Slice(noloopRegistration, func(i, j int) bool {
		return noloopRegistration[i].id < noloopRegistration[j].id
	})

	fmt.Fprintf(file, "# %d collector registered, %d with a scrape-loop.\n", len(r.registrations), len(loopRegistration))
	fmt.Fprintf(file, "# %d collectors with loop active:\n", len(loopRegistration))

	for _, reg := range loopRegistration {
		reg.l.Lock()

		fmt.Fprintf(file, "id=%d, lastRun=%v (duration=%v, interval=%v,)\n", reg.id, reg.lastScrape, reg.lastScrapeDuration, reg.loop.interval)
		fmt.Fprintf(file, "    %s\n", reg.option.String())
		fmt.Fprintf(file, "    label used: %v\n", dtoLabelToMap(reg.gatherer.labels))

		reg.l.Unlock()
	}

	fmt.Fprintf(file, "\n# %d collectors with no active loop (only on /metrics):\n", len(noloopRegistration))

	for _, reg := range noloopRegistration {
		reg.l.Lock()

		fmt.Fprintf(file, "id=%d, lastRun=%v (duration=%v)\n", reg.id, reg.lastScrape, reg.lastScrapeDuration)
		fmt.Fprintf(file, "    %s\n", reg.option.String())

		reg.l.Unlock()
	}

	return nil
}

func (r *Registry) diagnosticState(archive types.ArchiveWriter) error {
	file, err := archive.Create("metrics-registry-state.json")
	if err != nil {
		return err
	}

	r.l.Lock()

	obj := struct {
		Option                  Option
		CountScrape             int
		CountPushPoints         int
		BlockScrape             bool
		BlockPushPoint          bool
		Reschedules             []reschedule
		LastPushedPointsCleanup time.Time
		CurrentDelaySeconds     float64
		PushedPointsCount       int
	}{
		Option:                  r.option,
		CountScrape:             r.countScrape,
		CountPushPoints:         r.countPushPoints,
		BlockScrape:             r.blockScrape,
		BlockPushPoint:          r.blockPushPoint,
		Reschedules:             r.reschedules,
		LastPushedPointsCleanup: r.lastPushedPointsCleanup,
		CurrentDelaySeconds:     r.currentDelay.Seconds(),
		PushedPointsCount:       len(r.pushedPoints),
	}

	defer r.l.Unlock()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

func (r *Registry) writeMetrics(ctx context.Context, file io.Writer, filter bool) error {
	result, err := r.GatherWithState(ctx, GatherState{QueryType: FromStore, NoFilter: !filter})
	if err != nil {
		return err
	}

	enc := expfmt.NewEncoder(file, expfmt.FmtOpenMetrics)
	for _, mf := range result {
		if err := enc.Encode(mf); err != nil {
			return err
		}
	}

	return nil
}

func (r *Registry) writeMetricsSelf(file io.Writer) error {
	result, err := r.internalRegistry.Gather()
	if err != nil {
		return err
	}

	enc := expfmt.NewEncoder(file, expfmt.FmtOpenMetrics)
	for _, mf := range result {
		if err := enc.Encode(mf); err != nil {
			return err
		}
	}

	return nil
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
func (r *Registry) RegisterGatherer(opt RegistrationOption, gatherer prometheus.Gatherer) (int, error) {
	r.init()

	if err := opt.buildRules(); err != nil {
		return 0, err
	}

	r.l.Lock()
	defer r.l.Unlock()

	reg := &registration{
		option: opt,
	}
	r.setupGatherer(reg, gatherer)

	return r.addRegistration(reg)
}

func (r *Registry) addRegistration(reg *registration) (int, error) {
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

	if !reg.option.DisablePeriodicGather {
		if g, ok := reg.gatherer.getSource().(GathererWithScheduleUpdate); ok {
			g.SetScheduleUpdate(func(runAt time.Time) {
				r.scheduleUpdate(id, reg, runAt)
			})
		}

		r.restartScrapeLoop(reg)
	}

	return id, nil
}

// restartScrapeLoop start a scrapeLoop for this registration after stop previous loop if it exists.
// r.lock must be hold before calling this method.
func (r *Registry) restartScrapeLoop(reg *registration) {
	if reg.loop != nil {
		r.l.Unlock()
		reg.loop.stop()
		r.l.Lock()
	}

	interval := reg.option.Interval
	if interval == 0 {
		interval = r.currentDelay
	}

	if reg.option.MinInterval != 0 && interval < reg.option.MinInterval {
		interval = reg.option.MinInterval
	}

	timeout := interval * 8 / 10
	if reg.option.Timeout != 0 && reg.option.Timeout < interval {
		timeout = reg.option.Timeout
	}

	reg.loop = startScrapeLoop(
		interval,
		timeout,
		reg.option.JitterSeed,
		func(ctx context.Context, t0 time.Time) {
			r.scrapeFromLoop(ctx, t0, reg)
		},
		reg.option.Description,
	)
}

func (r *Registry) ScheduleScrape(id int, runAt time.Time) {
	r.l.Lock()
	reg := r.registrations[id]
	r.l.Unlock()

	if reg == nil {
		return
	}

	r.scheduleUpdate(id, reg, runAt)
}

// scheduleUpdate updates the next run of the gatherer.
func (r *Registry) scheduleUpdate(id int, reg *registration, runAt time.Time) {
	// Run the actual update in another goroutine and return instantly to make
	// sure taking the registry lock doesn't cause a deadlock.
	go func() {
		defer types.ProcessPanic()

		r.l.Lock()
		defer r.l.Unlock()

		if reg2, ok := r.registrations[id]; !ok || reg2 != reg {
			return
		}

		r.reschedules = append(r.reschedules, reschedule{
			ID:    id,
			Reg:   reg,
			RunAt: runAt,
		})

		sort.Slice(r.reschedules, func(i, j int) bool {
			return r.reschedules[i].RunAt.Before(r.reschedules[j].RunAt)
		})
	}()
}

func (r *Registry) checkReschedule(ctx context.Context) time.Duration {
	r.l.Lock()
	defer r.l.Unlock()

	firstInFuture := -1
	now := time.Now()

	for i, value := range r.reschedules {
		if value.RunAt.After(now) {
			firstInFuture = i

			break
		}

		if reg2, ok := r.registrations[value.ID]; !ok || reg2 != value.Reg {
			continue
		}

		reg := value.Reg

		go func() {
			defer types.ProcessPanic()

			ctx, cancel := context.WithTimeout(ctx, defaultGatherTimeout)
			defer cancel()

			r.scrapeFromLoop(ctx, now.Truncate(time.Second), reg)
		}()
	}

	if firstInFuture == -1 {
		r.reschedules = nil

		return 10 * time.Second
	}

	if firstInFuture > 0 {
		initialLength := len(r.reschedules)

		copy(r.reschedules[:initialLength-firstInFuture], r.reschedules[firstInFuture:])
		r.reschedules = r.reschedules[:initialLength-firstInFuture]
	}

	delta := time.Until(r.reschedules[0].RunAt)
	if delta < time.Second {
		delta = time.Second
	}

	return delta
}

// Unregister remove a Gatherer or PushPointCallback from the list of metric sources.
func (r *Registry) Unregister(id int) bool {
	r.init()
	r.l.Lock()

	reg, ok := r.registrations[id]

	if !ok {
		r.l.Unlock()

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

	r.l.Unlock()

	reg.gatherer.close()

	if reg.option.StopCallback != nil {
		reg.option.StopCallback()
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

	if state.QueryType == FromStore {
		if r.option.Queryable == nil {
			return nil, nil
		}

		filter := r.option.Filter

		if state.NoFilter {
			filter = nil
		}

		return gatherFromQueryable(ctx, r.option.Queryable, filter)
	}

	r.l.Lock()

	var (
		mfs   []*dto.MetricFamily
		errs  prometheus.MultiError
		mutex sync.Mutex
	)

	wg := sync.WaitGroup{}

	for _, reg := range r.registrations {
		reg := reg

		wg.Add(1)

		go func() {
			defer types.ProcessPanic()
			defer wg.Done()

			scrapedMFS, _, err := r.scrape(ctx, state, reg)

			// Apply thresholds.
			scrapedPoints := gloutonModel.FamiliesToMetricPoints(time.Time{}, scrapedMFS)

			var statusPoints []types.MetricPoint

			if r.option.ThresholdHandler != nil {
				_, statusPoints = r.option.ThresholdHandler.ApplyThresholds(scrapedPoints)
			}

			statusMFS := gloutonModel.MetricPointsToFamilies(statusPoints)

			mutex.Lock()
			defer mutex.Unlock()

			if err != nil {
				errs = append(errs, err)
			}

			mfs = append(mfs, scrapedMFS...)
			mfs = append(mfs, statusMFS...)
		}()
	}

	r.l.Unlock()

	if state.QueryType != OnlyProbes {
		pts := r.getPushedPoints()
		tmp := gloutonModel.MetricPointsToFamilies(pts)

		mutex.Lock()

		mfs = append(mfs, tmp...)

		mutex.Unlock()
	}

	wg.Wait()

	mfs, err := mergeMFS(mfs)
	if err != nil {
		errs = append(errs, err)
	}

	// Remove meta labels to prevent the Prometheus gatherer
	// from deleting metrics using reserved labels.
	mfs = removeMetaLabels(mfs)

	// Use prometheus.Gatherers because it will:
	// * Remove (and complain) about possible duplicate of metric
	// * sort output
	// * maybe other sanity check :)

	promGatherers := prometheus.Gatherers{
		sliceGatherer(mfs),
	}

	sortedResult, err := promGatherers.Gather()
	if err != nil {
		if multiErr, ok := err.(prometheus.MultiError); ok {
			errs = append(errs, multiErr...)
		} else {
			errs = append(errs, err)
		}
	}

	return sortedResult, errs.MaybeUnwrap()
}

func gatherFromQueryable(ctx context.Context, queryable storage.Queryable, filter metricFilter) ([]*dto.MetricFamily, error) {
	var result []*dto.MetricFamily

	now := time.Now()
	mint := now.Add(-5 * time.Minute)

	querier, err := queryable.Querier(ctx, mint.UnixMilli(), now.UnixMilli())
	if err != nil {
		return nil, err
	}

	series := querier.Select(true, nil)
	for series.Next() {
		lbls := series.At().Labels()
		iter := series.At().Iterator()

		name := lbls.Get(types.LabelName)
		if len(result) == 0 || result[len(result)-1].GetName() != name {
			result = append(result, &dto.MetricFamily{
				Name: &name,
				Type: dto.MetricType_GAUGE.Enum(),
			})
		}

		dtoLabels := make([]*dto.LabelPair, 0, len(lbls)-1)

		for _, l := range lbls {
			l := l

			if l.Name == types.LabelName {
				continue
			}

			dtoLabels = append(dtoLabels, &dto.LabelPair{Name: &l.Name, Value: &l.Value})
		}

		var lastValue float64

		for iter.Next() {
			_, lastValue = iter.At()
		}

		if iter.Err() != nil {
			return result, iter.Err()
		}

		metric := &dto.Metric{
			Label: dtoLabels,
			Gauge: &dto.Gauge{Value: &lastValue},
		}

		result[len(result)-1].Metric = append(result[len(result)-1].Metric, metric)
	}

	if filter != nil {
		result = filter.FilterFamilies(result)
	}

	return result, series.Err()
}

// removeMetaLabels removes all meta labels from the metric family.
func removeMetaLabels(mfs []*dto.MetricFamily) []*dto.MetricFamily {
	for _, mf := range mfs {
		for _, metric := range mf.GetMetric() {
			nextFreeIndex := 0

			for _, label := range metric.GetLabel() {
				// Keep only non reserved labels. The deletion is done in place.
				if !strings.HasPrefix(label.GetName(), types.ReservedLabelPrefix) {
					metric.Label[nextFreeIndex] = label
					nextFreeIndex++
				}
			}

			// Prevent memory leak by erasing truncated values.
			for j := nextFreeIndex; j < len(metric.Label); j++ {
				metric.Label[j] = nil
			}

			metric.Label = metric.Label[:nextFreeIndex]
		}
	}

	return mfs
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

	_, _ = r.RegisterGatherer(
		RegistrationOption{
			Description:           "go & process collector",
			JitterSeed:            baseJitter,
			Interval:              defaultInterval,
			DisablePeriodicGather: r.option.MetricFormat != types.MetricFormatPrometheus,
		},
		r.internalRegistry,
	)
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
	_, _ = r.RegisterGatherer(
		RegistrationOption{
			Description:           "/metrics collector",
			JitterSeed:            baseJitter,
			Interval:              defaultInterval,
			DisablePeriodicGather: r.option.MetricFormat != types.MetricFormatPrometheus,
		},
		reg,
	)

	return handler
}

// WithTTL return a AddMetricPointFunction with TTL on pushed points.
func (r *Registry) WithTTL(ttl time.Duration) types.PointPusher {
	r.init()

	return pushFunction(func(ctx context.Context, points []types.MetricPoint) {
		r.pushPoint(ctx, points, ttl, r.option.MetricFormat)
	})
}

// WithTTLAndFormat return a AddMetricPointFunction with TTL on pushed points and a metric format.
// The returned function bypasses the metric format contained in the registry options.
func (r *Registry) WithTTLAndFormat(ttl time.Duration, format types.MetricFormat) types.PointPusher {
	r.init()

	return pushFunction(func(ctx context.Context, points []types.MetricPoint) {
		r.pushPoint(ctx, points, ttl, format)
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
		reg := reg

		if reg.option.Interval != 0 {
			continue
		}

		if reg.option.DisablePeriodicGather {
			continue
		}

		r.restartScrapeLoop(reg)
	}
}

// InternalRunScrape run a scrape/gathering on given registration id (from RegisterGatherer & co).
// Points gatherer are processed at if a periodic gather occurred.
// This should only be used in test.
func (r *Registry) InternalRunScrape(ctx context.Context, t0 time.Time, id int) {
	r.l.Lock()

	reg, ok := r.registrations[id]

	r.l.Unlock()

	if ok {
		r.scrapeFromLoop(ctx, t0, reg)
	}
}

func (r *Registry) scrapeFromLoop(ctx context.Context, t0 time.Time, reg *registration) {
	r.scrapeStart()
	defer r.scrapeDone()

	mfs, duration, err := r.scrape(ctx, GatherState{QueryType: All, FromScrapeLoop: true, T0: t0}, reg)
	if err != nil {
		if len(mfs) == 0 {
			logger.V(1).Printf("Gather of metrics failed on %s: %v", reg.option.Description, err)
		} else {
			// When there is points, log at lower level because we known that some gatherer always
			// fail on some setup. node_exporter may sent "node_rapl_package_joules_total" duplicated.
			logger.V(1).Printf("Gather of metrics failed on %s, some metrics may be missing: %v", reg.option.Description, err)
		}
	}

	points := gloutonModel.FamiliesToMetricPoints(t0, mfs)

	if (reg.annotations != types.MetricAnnotations{}) {
		for i := range points {
			points[i].Annotations = points[i].Annotations.Merge(reg.annotations)
		}
	}

	reg.l.Lock()
	reg.lastScrape = t0
	reg.lastScrapeDuration = duration
	reg.l.Unlock()

	if len(points) > 0 && r.option.PushPoint != nil {
		if r.option.ThresholdHandler != nil {
			var statusPoints []types.MetricPoint

			points, statusPoints = r.option.ThresholdHandler.ApplyThresholds(points)
			points = append(points, statusPoints...)
		}

		r.option.PushPoint.PushPoints(ctx, points)
	}
}

func (r *Registry) scrape(ctx context.Context, state GatherState, reg *registration) ([]*dto.MetricFamily, time.Duration, error) {
	r.l.Lock()
	reg.l.Lock()

	if reg.relabelHookSkip && time.Since(reg.lastRebalHookRetry) > hookRetryDelay {
		r.setupGatherer(reg, reg.gatherer.getSource())
	}

	r.l.Unlock()

	if reg.relabelHookSkip {
		reg.l.Unlock()

		return nil, 0, nil
	}

	gatherMethod := reg.gatherer.GatherWithState

	reg.l.Unlock()

	start := time.Now()

	mfs, err := gatherMethod(ctx, state)

	if !reg.option.NoLabelsAlteration {
		mfs = r.renamer.RenameMFS(mfs)
	}

	return mfs, time.Since(start), err
}

// pushPoint add a new point to the list of pushed point with a specified TTL.
// As for AddMetricPointFunction, points should not be mutated after the call.
func (r *Registry) pushPoint(ctx context.Context, points []types.MetricPoint, ttl time.Duration, format types.MetricFormat) {
	r.l.Lock()

	for r.blockPushPoint {
		r.condition.Wait()
	}

	r.countPushPoints++

	now := time.Now()
	deadline := now.Add(ttl)

	n := 0

	for _, point := range points {
		var (
			err  error
			skip bool
		)

		point.Labels, err = fixLabels(point.Labels)
		if err != nil {
			logger.V(2).Printf("Ignoring metric %v: %v", point.Labels, err)

			continue
		}

		if format == types.MetricFormatBleemeo {
			newLabelsMap := map[string]string{
				types.LabelName: point.Labels[types.LabelName],
			}

			if point.Annotations.BleemeoItem != "" {
				newLabelsMap[types.LabelItem] = point.Annotations.BleemeoItem
			}

			point.Labels = newLabelsMap

			if r.relabelHook != nil {
				ctx, cancel := context.WithTimeout(ctx, relabelTimeout)
				point.Labels, skip = r.relabelHook(ctx, point.Labels)

				cancel()
			}

			newLabels, _ := r.applyRelabel(point.Labels)
			point.Labels = newLabels.Map()
		} else {
			point.Labels = r.addMetaLabels(point.Labels)

			if r.relabelHook != nil {
				ctx, cancel := context.WithTimeout(ctx, relabelTimeout)
				point.Labels, skip = r.relabelHook(ctx, point.Labels)

				cancel()
			}

			newLabels, annotations2 := r.applyRelabel(point.Labels)
			point.Labels = newLabels.Map()
			point.Annotations = point.Annotations.Merge(annotations2)
		}

		if !skip {
			points[n] = point
			n++
		}
	}

	points = points[:n]
	points = r.renamer.Rename(points)

	// Apply the thresholds after the relabel hook to get the instance UUID in the labels.
	if r.option.ThresholdHandler != nil {
		var statusPoints []types.MetricPoint

		points, statusPoints = r.option.ThresholdHandler.ApplyThresholds(points)
		points = append(points, statusPoints...)
	}

	for _, point := range points {
		key := types.LabelsToText(point.Labels)
		r.pushedPoints[key] = point
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

	if r.option.PushPoint != nil {
		r.option.PushPoint.PushPoints(ctx, points)
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

	if r.option.BlackboxSendScraperID {
		result[types.LabelMetaSendScraperUUID] = "yes"
	}

	return result
}

func (r *Registry) applyRelabel(input map[string]string) (labels.Labels, types.MetricAnnotations) {
	promLabels := labels.FromMap(input)
	annotations := gloutonModel.MetaLabelsToAnnotation(promLabels)
	promLabels = relabel.Process(
		promLabels,
		r.relabelConfigs...,
	)

	promLabels = gloutonModel.DropMetaLabels(promLabels)

	return promLabels, annotations
}

func (r *Registry) setupGatherer(reg *registration, source prometheus.Gatherer) {
	var (
		promLabels  labels.Labels
		annotations types.MetricAnnotations
	)

	if !reg.option.NoLabelsAlteration {
		extraLabels := r.addMetaLabels(reg.option.ExtraLabels)

		reg.relabelHookSkip = false

		if r.relabelHook != nil {
			ctxTimeout, cancel := context.WithTimeout(context.Background(), relabelTimeout)
			defer cancel()

			extraLabels, reg.relabelHookSkip = r.relabelHook(ctxTimeout, extraLabels)
			reg.lastRebalHookRetry = time.Now()
		}

		promLabels, annotations = r.applyRelabel(extraLabels)
	}

	g := newLabeledGatherer(source, promLabels, reg.option.rrules)
	reg.annotations = annotations
	reg.gatherer = g
}

// Collect collect pushed points.
func (r *Registry) getPushedPoints() []types.MetricPoint {
	r.l.Lock()
	defer r.l.Unlock()

	now := time.Now()

	r.lastPushedPointsCleanup = now

	pts := make([]types.MetricPoint, 0, len(r.pushedPoints))

	for key, p := range r.pushedPoints {
		expiration := r.pushedPointsExpiration[key]
		if now.After(expiration) {
			delete(r.pushedPoints, key)
			delete(r.pushedPointsExpiration, key)

			continue
		}

		pts = append(pts, p)
	}

	return pts
}

func fixLabels(lbls map[string]string) (map[string]string, error) {
	replacer := strings.NewReplacer(".", "_", "-", "_")

	for l, v := range lbls {
		if l == types.LabelName {
			if !model.IsValidMetricName(model.LabelValue(v)) {
				v = replacer.Replace(v)

				if !model.IsValidMetricName(model.LabelValue(v)) {
					return nil, fmt.Errorf("%w: %v", errInvalidName, v)
				}

				lbls[types.LabelName] = v
			}
		} else {
			if !model.LabelName(l).IsValid() {
				newL := replacer.Replace(l)
				if !model.LabelName(newL).IsValid() {
					return nil, fmt.Errorf("%w: %v", errInvalidName, l)
				}

				delete(lbls, l)
				lbls[l] = v
			}
		}
	}

	return lbls, nil
}
