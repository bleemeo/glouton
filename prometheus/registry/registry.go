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
	"io"
	"maps"
	"net/http"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/delay"
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/logger"
	gloutonModel "github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/archivewriter"

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
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/prometheus/prometheus/util/gate"
)

const (
	pushedPointsCleanupInterval = 5 * time.Minute
	hookRetryDelay              = 2 * time.Minute
	relabelTimeout              = 20 * time.Second
	baseJitter                  = 0
	maxLastScrape               = 10
	gloutonMinimalInterval      = 10 * time.Second
	scrapeErrLogMinInterval     = 5 * time.Minute
)

// RelabelHook is a hook called just before applying relabeling.
// This hook receive the full labels (including meta labels) and is allowed to
// modify/add/delete them. The result (which should still include meta labels) is
// processed by relabeling rules.
// If the hook return retryLater, it means that hook can not processed the labels currently
// and it registry should retry later. Points or gatherer associated will be dropped.
type RelabelHook func(ctx context.Context, labels map[string]string) (newLabel map[string]string, retryLater bool)

// UpdateDelayHook returns the minimal gathering interval
// for the given labels. For example, according to the Bleemeo plan.
type UpdateDelayHook func(labels map[string]string) (minInterval time.Duration, retryLater bool)

var (
	errInvalidName = errors.New("invalid metric name or label name")
	ErrBadArgument = errors.New("bad argument")
)

type diagnosticer interface {
	DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error
}

// pushFunction implement types.PointPusher.
type pushFunction func(ctx context.Context, points []types.MetricPoint)

func (f pushFunction) PushPoints(ctx context.Context, points []types.MetricPoint) {
	f(ctx, points)
}

type metricFilter interface {
	FilterPoints(points []types.MetricPoint, allowNeededByRules bool) []types.MetricPoint
	FilterFamilies(f []*dto.MetricFamily, allowNeededByRules bool) []*dto.MetricFamily
	IsMetricAllowed(lbls labels.Labels, allowNeededByRules bool) bool
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

	condition               *sync.Cond
	countScrape             int
	countPushPoints         int
	blockPushPoint          bool
	tooSlowConsecutiveError int
	running                 bool
	startStopLock           sync.Mutex

	reschedules             []reschedule
	relabelConfigs          []*relabel.Config
	registrations           map[int]*registration
	internalRegistry        *prometheus.Registry
	pushedPoints            map[string]types.MetricPoint
	pushedPointsExpiration  map[string]time.Time
	lastPushedPointsCleanup time.Time
	relabelHook             RelabelHook
	updateDelayHook         UpdateDelayHook
}

type Option struct {
	PushPoint             types.PointPusher
	ThresholdHandler      ThresholdHandler
	Queryable             storage.Queryable
	FQDN                  string
	GloutonPort           string
	BlackboxSendScraperID bool
	Filter                metricFilter
	SecretInputsGate      *gate.Gate
	ShutdownDeadline      time.Duration
}

type RegistrationOption struct {
	Description  string
	JitterSeed   uint64
	MinInterval  time.Duration
	Timeout      time.Duration
	StopCallback func() `json:"-"`
	// ExtraLabels are labels added. If a labels already exists, extraLabels takes precedence.
	ExtraLabels map[string]string
	// NoLabelsAlteration disable (most) alteration of labels. It doesn't apply to PushPoints.
	// Meta labels (starting with __) are still dropped and (if applicable) converted to annotations.
	NoLabelsAlteration bool
	// CompatibilityNameItem enable renaming metrics labels to just name + item (from Annotations.BleemeoItem).
	// This should eventually be dropped once all metrics are produced with right name + item directly rather than using
	// Annotations.BleemeoItem. This compatibility was mostly needed when Bleemeo didn't supported labels and only had
	// name + item. If dropped, we should be careful to don't change existing metrics.
	// Note: it doesn't impact labels from ExtraLabels (including relabeling processing), it only drop labels
	// from each points from the gatherer itself, the ExtraLabels after relabeling (a.k.a "LabelUsed" in diagnostic) are still
	// added.
	CompatibilityNameItem bool
	// ApplyDynamicRelabel controls whether the metrics should go through the relabel hook.
	ApplyDynamicRelabel bool
	Rules               []types.SimpleRule
	// GatherModifier is a function that can modify the gather result (add/modify/delete). It is called after Rules
	// are applied, just before ExtraLabels is done.
	// It could be nil to skip this step.
	GatherModifier gatherModifier `json:"-"`
	// IsEssential tells whether the corresponding gatherer is considered as 'essential' regarding the agent dashboard.
	// When all 'essentials' gatherers are stuck, Glouton kills himself (see Registry.HealthCheck).
	IsEssential bool
	// AcceptAllowedMetricsOnly will only keep metrics allowed at ends of Gather(), so the
	// metric not allowed by allow_list (or metric denied) will be dropped. Metrics that are
	// needed by SimpleRule will still be allowed.
	// Currently (until Registry.renamer is dropped), this shouldn't be activated on SNMP gatherer.
	AcceptAllowedMetricsOnly bool
	// HonorTimestamp indicate whether timestamp associated with each metric point is used or if a timestamp
	// decided by the Registry is used. Using the timestamp of the registry is preferred as its more stable.
	// If you need mixed timestamp decided by the Registry and timestamp associated with some points, use a
	// zero time (time.Time{}, Unix epoc or nil) on point that need to use the Registry's timestamp.
	// This is currently only implemented for AppenderCallback.
	HonorTimestamp bool
	// CallForMetricsEndpoint indicate whether the callback must be called for /metrics or
	// cached result from last periodic collection is used.
	CallForMetricsEndpoint bool
	// If labels has a container name (in the meta-labels), use it when building the "instance" label
	InstanceUseContainerName bool
	rrules                   []*rules.RecordingRule
}

type registrationType int

const (
	registrationGatherer           registrationType = iota
	registrationAppenderCallback   registrationType = iota
	registrationInput              registrationType = iota
	registrationPushPointsCallback registrationType = iota
)

func (rt registrationType) String() string {
	switch rt {
	case registrationGatherer:
		return "RegisterGatherer"
	case registrationAppenderCallback:
		return "RegisterAppenderCallback"
	case registrationInput:
		return "RegisterInput"
	case registrationPushPointsCallback:
		return "RegisterPushPointsCallback"
	default:
		return "unknown"
	}
}

type AppenderCallback interface {
	// Collect collects point and write them into Appender. The appender must not be used once Collect returned.
	// If you omit to Commit() on the appender, it will be automatically done when Collect return without error.
	CollectWithState(ctx context.Context, state GatherState, app storage.Appender) error
}

type AppenderFunc func(ctx context.Context, state GatherState, app storage.Appender) error

func (af AppenderFunc) CollectWithState(ctx context.Context, state GatherState, app storage.Appender) error {
	return af(ctx, state, app)
}

type ThresholdHandler interface {
	ApplyThresholds(points []types.MetricPoint) (newPoints []types.MetricPoint, statusPoints []types.MetricPoint)
}

func (opt *RegistrationOption) buildRules() error {
	rrules := make([]*rules.RecordingRule, 0, len(opt.Rules))

	for i, r := range opt.Rules {
		expr, err := parser.ParseExpr(r.PromQLQuery)
		if err != nil {
			return fmt.Errorf("rule %s (n°%d): %w", r.TargetName, i+1, err)
		}

		rr := rules.NewRecordingRule(r.TargetName, expr, nil)
		rrules = append(rrules, rr)
	}

	opt.rrules = rrules

	return nil
}

func (opt *RegistrationOption) String() string {
	hasStop := "without stop callback"
	if opt.StopCallback != nil {
		hasStop = "with stop callback"
	}

	return fmt.Sprintf(
		"%q with labels %v; min_interval=%v, seed=%d, timeout=%v, %s",
		opt.Description, opt.ExtraLabels, opt.MinInterval, opt.JitterSeed, opt.Timeout, hasStop,
	)
}

type scrapeRun struct {
	ScrapeAt           time.Time
	ScrapeDuration     time.Duration
	ScrapedPointsCount int
	Error              error
}

func (s scrapeRun) MarshalJSON() ([]byte, error) {
	tmp := struct {
		ScrapeAt           time.Time
		ScrapeDuration     string
		ScrapedPointsCount int
		Error              string `json:",omitempty"`
	}{
		ScrapeAt:           s.ScrapeAt,
		ScrapeDuration:     s.ScrapeDuration.String(),
		ScrapedPointsCount: s.ScrapedPointsCount,
	}

	if s.Error != nil {
		tmp.Error = s.Error.Error()
	}

	return json.Marshal(tmp)
}

type registration struct {
	l                    sync.Mutex
	regType              registrationType
	option               RegistrationOption
	addedAt              time.Time
	removalRequested     bool
	restartInProgress    bool
	runOnStart           bool
	loop                 *scrapeLoop
	lastScrapes          []scrapeRun
	gatherer             *wrappedGatherer
	annotations          types.MetricAnnotations
	labelsWithMeta       map[string]string
	relabelHookSkip      bool
	lastRelabelHookRetry time.Time
	hookMinimalInterval  time.Duration
	interval             time.Duration
	lastErrorLogAt       time.Time
}

// RunNow will trigger a run of the scrapeLoop. If the registry isn't running,
// the run will be delayed until the registry is started. In other case it will be run
// as quickly as possible.
// Only registration using the periodic gather are handler, other are ignored.
func (reg *registration) RunNow() {
	reg.l.Lock()
	defer reg.l.Unlock()

	if reg.loop == nil {
		reg.runOnStart = true
	} else {
		reg.loop.Trigger()
	}
}

type reschedule struct {
	ID    int
	Reg   *registration
	RunAt time.Time
}

var errTooManyGatherers = errors.New("too many gatherers in the registry. Unable to find a new slot")

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
			Regex:        relabel.MustNewRegexp("(.+)"),
			SourceLabels: model.LabelNames{types.LabelMetaServiceUUID},
			TargetLabel:  types.LabelServiceUUID,
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
			Regex:        relabel.MustNewRegexp("(.+);(.+);(.+);yes"),
			SourceLabels: model.LabelNames{types.LabelMetaGloutonFQDN, types.LabelMetaContainerName, types.LabelMetaPort, types.LabelMetaInstanceUseContainerName},
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
		// and 'service_uuid' also.
		{
			Action:       relabel.Replace,
			Regex:        relabel.MustNewRegexp("(.+)"),
			SourceLabels: model.LabelNames{types.LabelMetaBleemeoTargetAgentUUID},
			TargetLabel:  types.LabelInstanceUUID,
			Replacement:  "$1",
		},
		{
			Action:       relabel.Replace,
			Regex:        relabel.MustNewRegexp("(.+)"),
			SourceLabels: model.LabelNames{types.LabelMetaProbeServiceUUID},
			TargetLabel:  types.LabelServiceUUID,
			Replacement:  "$1",
		},
		// when the metric comes from a probe, the 'instance' label is the target URI
		{
			Action:       relabel.Replace,
			Regex:        relabel.MustNewRegexp("(.+)"),
			SourceLabels: model.LabelNames{types.LabelMetaBleemeoTargetAgent},
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
	if opt.ShutdownDeadline == 0 {
		opt.ShutdownDeadline = time.Minute
	}

	reg := &Registry{
		option: opt,
	}

	reg.init()

	return reg, nil
}

func (r *Registry) init() {
	r.l.Lock()
	defer r.l.Unlock()

	if r.registrations != nil {
		return
	}

	r.condition = sync.NewCond(&r.l)
	r.registrations = make(map[int]*registration)
	r.internalRegistry = prometheus.NewRegistry()
	r.pushedPoints = make(map[string]types.MetricPoint)
	r.pushedPointsExpiration = make(map[string]time.Time)
	r.relabelConfigs = getDefaultRelabelConfig()
}

func (r *Registry) Run(ctx context.Context) error {
	r.startLoops()

	for ctx.Err() == nil {
		if ctx.Err() != nil {
			break
		}

		delay := r.checkReschedule()
		select {
		case <-time.After(delay):
		case <-ctx.Done():
		}
	}

	// Stop all scrape loops.
	r.stopAllLoops()

	// And unregister them to call Close() method on gatherer
	for id := range r.registrations {
		r.Unregister(id)
	}

	return ctx.Err()
}

// stopAllLoops, run the action while loops are stopped then restart them.
// You can NOT stop or start loops from the actionDuringStop.
func (r *Registry) restartLoops(actionDuringStop func()) {
	r.startStopLock.Lock()
	defer r.startStopLock.Unlock()

	r.l.Lock()
	needToStart := r.running
	r.l.Unlock()

	r.stopAllLoopsInner()

	actionDuringStop()

	if needToStart {
		r.startLoopsInner()
	}
}

func (r *Registry) startLoops() {
	r.startStopLock.Lock()
	defer r.startStopLock.Unlock()

	r.startLoopsInner()
}

func (r *Registry) startLoopsInner() {
	r.l.Lock()

	r.running = true

	regToStart := make([]*registration, 0, len(r.registrations))

	for _, reg := range r.registrations {
		regToStart = append(regToStart, reg)
	}

	r.l.Unlock()

	for _, reg := range regToStart {
		r.restartScrapeLoop(reg)
	}
}

// stopAllLoops make sure ALL loops are stopped (and disable starting new loop).
// You will need to call startLoops() to restart all loops including re-enable start of new loop.
// r.l lock must NOT be held.
func (r *Registry) stopAllLoops() {
	r.startStopLock.Lock()
	defer r.startStopLock.Unlock()

	r.stopAllLoopsInner()
}

func (r *Registry) stopAllLoopsInner() {
	r.l.Lock()

	r.running = false

	runningLoops := make([]*scrapeLoop, 0, len(r.registrations))

	for _, reg := range r.registrations {
		reg.l.Lock()

		if reg.loop == nil {
			reg.l.Unlock()

			continue
		}

		reg.loop.stopNoWait()
		runningLoops = append(runningLoops, reg.loop)

		reg.loop = nil

		reg.l.Unlock()
	}

	// release the lock to call stop(). This allow Gatherer that need to call Registry() (e.g. pushpoint callback)
	// to don't block on a deadlock.
	// We also need to don't hold reg.l lock, because to complete, scrapeFromLoop will acquire the reg.l lock.
	r.l.Unlock()

	for _, loop := range runningLoops {
		loop.stop()
	}
}

// RegisterGatherer add a new gatherer to the list of metric sources.
func (r *Registry) RegisterGatherer(opt RegistrationOption, gatherer prometheus.Gatherer) (int, error) {
	if err := opt.buildRules(); err != nil {
		return 0, err
	}

	r.l.Lock()
	defer r.l.Unlock()

	reg := &registration{
		option:  opt,
		regType: registrationGatherer,
	}
	r.setupGatherer(reg, gatherer)

	return r.addRegistration(reg)
}

// RegisterPushPointsCallback add a callback that should push points to the registry.
// This callback will be called for each collection period. It's mostly used to
// add Telegraf input (using glouton/collector).
// Deprecated: use RegisterAppenderCallback.
// Note: before being able to drop pushpoint & registerpushpoint, we likely need:
//   - support for "GathererWithScheduleUpdate-like" on RegisterAppenderCallback (needed by service check, when they trigger check on TCP close)
//   - support for conversion of all annotation to meta-label and vise-vera (model/convert.go)
//   - support for TTL ?
func (r *Registry) RegisterPushPointsCallback(opt RegistrationOption, f pushGatherFunction) (int, error) {
	return r.registerPushPointsCallback(opt, f)
}

func (r *Registry) registerPushPointsCallback(opt RegistrationOption, f pushGatherFunction) (int, error) {
	if !opt.HonorTimestamp {
		return 0, fmt.Errorf("%w: PushPoint will always HonorTimestamp", errors.ErrUnsupported)
	}

	if err := opt.buildRules(); err != nil {
		return 0, err
	}

	r.l.Lock()
	defer r.l.Unlock()

	reg := &registration{
		option:  opt,
		regType: registrationPushPointsCallback,
	}
	r.setupGatherer(reg, &pushGatherer{fun: f})

	return r.addRegistration(reg)
}

// RegisterAppenderCallback add a callback that use an Appender to write points to the registry.
func (r *Registry) RegisterAppenderCallback(
	opt RegistrationOption,
	cb AppenderCallback,
) (int, error) {
	if err := opt.buildRules(); err != nil {
		return 0, err
	}

	r.l.Lock()
	defer r.l.Unlock()

	// appenderGatherer take care of implementing HonorTimestamp, don't
	// re-do it in wrappedGatherer.
	appOpt := opt
	opt.HonorTimestamp = true

	reg := &registration{
		option:  opt,
		regType: registrationAppenderCallback,
	}
	r.setupGatherer(reg, &appenderGatherer{cb: cb, opt: appOpt})

	return r.addRegistration(reg)
}

// RegisterInput uses a Telegraph input to write points to the registry.
func (r *Registry) RegisterInput(
	opt RegistrationOption,
	input telegraf.Input,
) (int, error) {
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
		option:  opt,
		regType: registrationInput,
	}
	r.setupGatherer(reg, newInputGatherer(input))

	return r.addRegistration(reg)
}

// UpdateRegistrationHooks change the hook used just before relabeling and wait for all pending metrics emission.
// When this function return, it's guaratee that all call to Option.PushPoint will use new labels.
// The hook is assumed to be idempotent, that is for a given labels input the result is the same.
// If the hook want break this idempotence, UpdateRegistrationHooks() should be re-called to force update of existings Gatherer.
func (r *Registry) UpdateRegistrationHooks(relabelHook RelabelHook, updateDelayHook UpdateDelayHook, registerSNMPGatherersHook func()) {
	r.restartLoops(func() { r.updateHooks(relabelHook, updateDelayHook, registerSNMPGatherersHook) })
}

func (r *Registry) updateHooks(relabelHook RelabelHook, updateDelayHook UpdateDelayHook, registerSNMPGatherersHook func()) {
	r.l.Lock()
	defer r.l.Unlock()

	r.blockPushPoint = true

	// Wait for all pending goroutines that may be sending points with old labels
	for r.countPushPoints > 0 {
		r.condition.Wait()
	}

	r.relabelHook = relabelHook
	r.updateDelayHook = updateDelayHook

	// Since the updated Agent ID may change metrics labels, drop pushed points
	r.pushedPoints = make(map[string]types.MetricPoint)
	r.pushedPointsExpiration = make(map[string]time.Time)

	// Update labels of all gatherers
	for _, reg := range r.registrations {
		reg.l.Lock()
		r.setupGatherer(reg, reg.gatherer.getSource())
		reg.l.Unlock()
	}

	if registerSNMPGatherersHook != nil {
		r.l.Unlock()
		registerSNMPGatherersHook()
		r.l.Lock()
	}

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

	return r.diagnosticScrapeLoop(ctx, archive)
}

func (r *Registry) diagnosticState(archive types.ArchiveWriter) error {
	file, err := archive.Create("metrics-registry-state.json")
	if err != nil {
		return err
	}

	r.l.Lock()
	defer r.l.Unlock()

	obj := struct {
		Option                  Option
		CountScrape             int
		CountPushPoints         int
		BlockPushPoint          bool
		Reschedules             []reschedule
		LastPushedPointsCleanup time.Time
		PushedPointsCount       int
		TooSlowConsecutiveError int
	}{
		Option:                  r.option,
		CountScrape:             r.countScrape,
		CountPushPoints:         r.countPushPoints,
		BlockPushPoint:          r.blockPushPoint,
		Reschedules:             r.reschedules,
		LastPushedPointsCleanup: r.lastPushedPointsCleanup,
		PushedPointsCount:       len(r.pushedPoints),
		TooSlowConsecutiveError: r.tooSlowConsecutiveError,
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

// HealthCheck perform some health check and log any issue found.
// This method could panic when health conditions are bad for too long in order to cause a Glouton restart.
func (r *Registry) HealthCheck() {
	r.l.Lock()
	defer r.l.Unlock()

	// This flag will be set to false if at least one essential input is not stuck.
	shouldPanic := true

	for _, reg := range r.registrations {
		reg.l.Lock()

		if reg.loop != nil {
			lastScrape := reg.addedAt

			if len(reg.lastScrapes) > 0 {
				lastScrape = reg.lastScrapes[len(reg.lastScrapes)-1].ScrapeAt
			}

			if time.Since(lastScrape) > 5*reg.loop.interval {
				logger.Printf("Metrics collections is too slow for collector %s. Last run at %s and should run every %s",
					reg.option.Description,
					lastScrape.Format(time.RFC3339),
					reg.loop.interval.String(),
				)

				if reg.option.IsEssential && (time.Since(lastScrape) < 10*reg.loop.interval || time.Since(lastScrape) < time.Hour) {
					// This essential gatherer is not considered as stuck; don't panic for now.
					shouldPanic = false
				}
			} else if reg.option.IsEssential {
				// This essential gatherer is not stuck at all.
				shouldPanic = false
			}
		}

		reg.l.Unlock()
	}

	if shouldPanic {
		r.tooSlowConsecutiveError++

		if r.tooSlowConsecutiveError >= 3 {
			const panicMessage = "Metrics collections is blocked for all essential collectors. Glouton seems unhealthy, killing myself."

			logger.Printf(panicMessage)

			// We don't know how big the buffer needs to be to collect
			// all the goroutines. Use 2MB buffer which hopefully is enough
			buffer := make([]byte, 1<<21)

			n := runtime.Stack(buffer, true)
			logger.Printf("%s", string(buffer[:n]))

			panic(panicMessage)
		}
	} else {
		r.tooSlowConsecutiveError = 0
	}
}

func (r *Registry) diagnosticScrapeLoop(ctx context.Context, archive types.ArchiveWriter) error {
	r.l.Lock()
	defer r.l.Unlock()

	activeFile, err := archive.Create("scrape-loop-active.json")
	if err != nil {
		return err
	}

	type unexportableOption struct {
		HasStopCallback   bool
		HasGatherModifier bool
	}

	type loopInfo struct {
		ID                  int
		Description         string
		AddedAt             time.Time
		LastScrape          []scrapeRun
		RegInterval         string
		HookMinimalInterval string
		ScrapeInterval      string
		Option              RegistrationOption
		RegistrationType    string
		UnexportableOption  unexportableOption
		LabelUsed           map[string]string
		LabelsWithMeta      map[string]string
		RelabelHookSkip     bool
		RelabelHookLastTry  time.Time
		subDiagnostic       func(ctx context.Context, archive types.ArchiveWriter) error
	}

	activeResult := []loopInfo{}
	inactiveResult := []loopInfo{}

	for id, reg := range r.registrations {
		reg.l.Lock()

		info := loopInfo{
			ID:                  id,
			Description:         reg.option.Description,
			AddedAt:             reg.addedAt,
			LastScrape:          slices.Clone(reg.lastScrapes),
			RegInterval:         reg.interval.String(),
			HookMinimalInterval: reg.hookMinimalInterval.String(),
			Option:              reg.option,
			RegistrationType:    reg.regType.String(),
			LabelUsed:           dtoLabelToMap(reg.gatherer.labels),
			LabelsWithMeta:      reg.labelsWithMeta,
			RelabelHookSkip:     reg.relabelHookSkip,
			RelabelHookLastTry:  reg.lastRelabelHookRetry,
		}

		if reg.option.StopCallback != nil {
			info.UnexportableOption.HasStopCallback = true
		}

		if reg.option.GatherModifier != nil {
			info.UnexportableOption.HasGatherModifier = true
		}

		if diag, ok := reg.gatherer.source.(diagnosticer); ok {
			info.subDiagnostic = diag.DiagnosticArchive
		}

		if reg.loop != nil {
			info.ScrapeInterval = reg.loop.interval.String()
			activeResult = append(activeResult, info)
		} else {
			inactiveResult = append(inactiveResult, info)
		}

		reg.l.Unlock()
	}

	sort.Slice(activeResult, func(i, j int) bool {
		return activeResult[i].ID < activeResult[j].ID
	})

	sort.Slice(inactiveResult, func(i, j int) bool {
		return inactiveResult[i].ID < inactiveResult[j].ID
	})

	enc := json.NewEncoder(activeFile)
	enc.SetIndent("", "  ")

	if err := enc.Encode(activeResult); err != nil {
		return err
	}

	inactiveFile, err := archive.Create("scrape-loop-only-slash-metrics.json")
	if err != nil {
		return err
	}

	enc = json.NewEncoder(inactiveFile)
	enc.SetIndent("", "  ")

	if err := enc.Encode(inactiveResult); err != nil {
		return err
	}

	for _, info := range activeResult {
		if info.subDiagnostic == nil {
			continue
		}

		subArchive := archivewriter.NewPrefixWriter(fmt.Sprintf("gatherer-%d/", info.ID), archive)

		if err := info.subDiagnostic(ctx, subArchive); err != nil {
			return err
		}
	}

	return nil
}

func (r *Registry) writeMetrics(ctx context.Context, file io.Writer, filter bool) error {
	result, err := r.GatherWithState(ctx, GatherState{QueryType: FromStore, NoFilter: !filter})
	if err != nil {
		return err
	}

	enc := expfmt.NewEncoder(file, expfmt.NewFormat(expfmt.TypeOpenMetrics))
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

	enc := expfmt.NewEncoder(file, expfmt.NewFormat(expfmt.TypeOpenMetrics))
	for _, mf := range result {
		if err := enc.Encode(mf); err != nil {
			return err
		}
	}

	return nil
}

// addRegistration assume r.l lock is held.
func (r *Registry) addRegistration(reg *registration) (int, error) {
	id := 1

	_, ok := r.registrations[id]
	for ok {
		id++
		if id == 0 {
			return 0, errTooManyGatherers
		}

		_, ok = r.registrations[id]
	}

	reg.addedAt = time.Now()

	r.registrations[id] = reg

	if g, ok := reg.gatherer.getSource().(GathererWithScheduleUpdate); ok {
		g.SetScheduleUpdate(func(runAt time.Time) {
			r.scheduleUpdate(id, reg, runAt)
		})
	}

	if r.running {
		r.restartScrapeLoop(reg)
	}

	return id, nil
}

// restartScrapeLoop start a scrapeLoop for this registration after stop previous loop if it exists.
// reg.l must NOT be held.
// r.l lock should NOT be held or a deadlock could occur during stop.
func (r *Registry) restartScrapeLoop(reg *registration) {
	reg.l.Lock()
	defer reg.l.Unlock()

	if reg.removalRequested {
		return
	}

	if reg.restartInProgress {
		return
	}

	reg.restartInProgress = true

	if reg.loop != nil {
		loop := reg.loop
		reg.loop = nil

		reg.l.Unlock()
		loop.stop()
		reg.l.Lock()
	}

	timeout := reg.interval * 8 / 10
	if reg.option.Timeout != 0 && reg.option.Timeout < reg.interval {
		timeout = reg.option.Timeout
	}

	reg.loop = startScrapeLoop(
		reg.interval,
		timeout,
		reg.option.JitterSeed,
		func(ctx context.Context, loopCtx context.Context, t0 time.Time) {
			r.scrapeFromLoop(ctx, loopCtx, t0, reg)
		},
		reg.option.Description,
		reg.runOnStart,
	)
	reg.runOnStart = false
	reg.restartInProgress = false
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
		defer crashreport.ProcessPanic()

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

func (r *Registry) checkReschedule() time.Duration {
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
		reg.RunNow()
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
	r.l.Lock()
	reg, ok := r.registrations[id]

	// Remove reference to original gatherer first, because some gatherer
	// stopCallback will rely on runtime.GC() to cleanup resource.
	delete(r.registrations, id)

	r.l.Unlock()

	if !ok {
		return false
	}

	reg.l.Lock()

	reg.removalRequested = true
	loop := reg.loop
	reg.loop = nil

	reg.l.Unlock()

	if loop != nil {
		loop.stop()
	}

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

	return r.GatherWithState(ctx, GatherState{T0: time.Now()})
}

// GatherWithState implements GathererGatherWithState.
func (r *Registry) GatherWithState(ctx context.Context, state GatherState) ([]*dto.MetricFamily, error) {
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
		wg.Add(1)

		go func() {
			defer crashreport.ProcessPanic()
			defer wg.Done()

			scrapedMFS, _, err := r.scrape(ctx, state, reg)

			// Don't drop the meta labels here, they are needed for relabeling.
			scrapedPoints := gloutonModel.FamiliesToMetricPoints(time.Time{}, scrapedMFS, !reg.option.ApplyDynamicRelabel)

			if reg.option.ApplyDynamicRelabel {
				scrapedPoints = r.relabelPoints(ctx, scrapedPoints)
			}

			// Apply the thresholds after relabeling to get the instance UUID in the labels.
			var statusPoints []types.MetricPoint

			if r.option.ThresholdHandler != nil {
				_, statusPoints = r.option.ThresholdHandler.ApplyThresholds(scrapedPoints)
			}

			scrapedPoints = append(scrapedPoints, statusPoints...)
			allMFS := gloutonModel.MetricPointsToFamilies(scrapedPoints)

			mutex.Lock()
			defer mutex.Unlock()

			if err != nil {
				errs = append(errs, err)
			}

			mfs = append(mfs, allMFS...)
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

	if !state.NoFilter && r.option.Filter != nil {
		mfs = r.option.Filter.FilterFamilies(mfs, false)
	}

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

	querier, err := queryable.Querier(mint.UnixMilli(), now.UnixMilli())
	if err != nil {
		return nil, err
	}

	series := querier.Select(ctx, true, nil)
	for series.Next() {
		lbls := series.At().Labels()
		iter := series.At().Iterator(nil)

		name := lbls.Get(types.LabelName)
		if len(result) == 0 || result[len(result)-1].GetName() != name {
			result = append(result, &dto.MetricFamily{
				Name: &name,
				Type: dto.MetricType_GAUGE.Enum(),
			})
		}

		dtoLabels := make([]*dto.LabelPair, 0, len(lbls)-1)

		for _, l := range lbls {
			if l.Name == types.LabelName {
				continue
			}

			dtoLabels = append(dtoLabels, &dto.LabelPair{Name: &l.Name, Value: &l.Value})
		}

		var lastValue float64

		for iter.Next() == chunkenc.ValFloat {
			_, lastValue = iter.At()
		}

		if iter.Err() != nil {
			return result, iter.Err()
		}

		metric := &dto.Metric{
			Label: dtoLabels,
			Gauge: &dto.Gauge{Value: &lastValue},
		}

		result[len(result)-1].Metric = append(result[len(result)-1].GetMetric(), metric)
	}

	if filter != nil {
		result = filter.FilterFamilies(result, false)
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
			for j := nextFreeIndex; j < len(metric.GetLabel()); j++ {
				metric.Label[j] = nil
			}

			metric.Label = metric.GetLabel()[:nextFreeIndex]
		}
	}

	return mfs
}

type prefixLogger string

func (l prefixLogger) Println(v ...any) {
	all := make([]any, 0, len(v)+1)
	all = append(all, l)
	all = append(all, v...)

	logger.V(1).Println(all...)
}

// AddDefaultCollector adds the following collectors:
// GoCollector and ProcessCollector like the prometheus.DefaultRegisterer
// Internal registry which contains all glouton metrics.
func (r *Registry) AddDefaultCollector() {
	r.internalRegistry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	r.internalRegistry.MustRegister(collectors.NewGoCollector())

	_, _ = r.RegisterGatherer(
		RegistrationOption{
			Description: "go & process collector",
			JitterSeed:  baseJitter,
		},
		r.internalRegistry,
	)
}

// Exporter return an HTTP exporter.
func (r *Registry) Exporter() http.Handler {
	reg := prometheus.NewRegistry()
	handler := promhttp.InstrumentMetricHandler(reg, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		wrapper := NewGathererWithStateWrapper(req.Context(), r)

		state := GatherStateFromMap(req.URL.Query())

		wrapper.SetState(state)

		promhttp.HandlerFor(wrapper, promhttp.HandlerOpts{
			ErrorHandling: promhttp.ContinueOnError,
			ErrorLog:      prefixLogger("/metrics endpoint:"),
		}).ServeHTTP(w, req)
	}))
	_, _ = r.RegisterGatherer(
		RegistrationOption{
			Description: "/metrics collector",
			JitterSeed:  baseJitter,
		},
		reg,
	)

	return handler
}

// WithTTL return a AddMetricPointFunction with TTL on pushed points.
func (r *Registry) WithTTL(ttl time.Duration) types.PointPusher {
	return pushFunction(func(ctx context.Context, points []types.MetricPoint) {
		r.pushPoint(ctx, points, ttl)
	})
}

// InternalRunScrape run a scrape/gathering on given registration id (from RegisterGatherer & co).
// Points gatherer are processed at if a periodic gather occurred.
// This should only be used in test.
func (r *Registry) InternalRunScrape(ctx context.Context, loopCtx context.Context, t0 time.Time, id int) {
	r.l.Lock()

	reg, ok := r.registrations[id]

	r.l.Unlock()

	if ok {
		r.scrapeFromLoop(ctx, loopCtx, t0, reg)
	}
}

// ctx must be a sub-context of loopCtx. If the gatherer method don't finished within ShutdownDeadline after
// loopCtx expired, we abandon it behind and finish anyway.
func (r *Registry) scrapeFromLoop(ctx context.Context, loopCtx context.Context, t0 time.Time, reg *registration) {
	state := GatherState{QueryType: All, FromScrapeLoop: true, T0: t0}

	// Never use HintMetricFilter when GatherModifier is present, because this will be source of
	// confusion: HintMetricFilter is applied before GatherModifier, so if labels are changed
	// by GatherModifier, HintMetricFilter will not do what is expected.
	// Them same apply to ApplyDynamicRelabel.
	// The Registry.renamer is also effected by this issue. renamer is only used for SNMP, SNMP should not
	// activate AcceptAllowedMetricsOnly.
	if reg.option.AcceptAllowedMetricsOnly && reg.option.GatherModifier == nil && !reg.option.ApplyDynamicRelabel {
		reg.l.Lock()
		extraLabels := reg.gatherer.labels
		reg.l.Unlock()

		state.HintMetricFilter = func(lbls labels.Labels) bool {
			return r.option.Filter.IsMetricAllowed(mergeLabels(lbls, extraLabels), true)
		}
	}

	goroutingFinished := make(chan any)

	var (
		mfs      []*dto.MetricFamily
		duration time.Duration
		err      error
	)

	go func() {
		defer crashreport.ProcessPanic()
		defer close(goroutingFinished)

		mfs, duration, err = r.scrape(ctx, state, reg)
	}()

	// We use a gorouting and not a WaitGroup because we want to wait with a maximum deadline.
	// If the gorouting don't want to finish (e.g. don't honor context), we left it behind.
	select {
	case <-goroutingFinished:
	case <-loopCtx.Done():
		deadlineTimer := time.NewTimer(r.option.ShutdownDeadline)
		defer deadlineTimer.Stop()

		select {
		case <-deadlineTimer.C:
			logger.V(2).Printf("%s didn't finished on time, abandon the gorouting", reg.option.Description)

			return
		case <-goroutingFinished:
		}
	}

	reg.l.Lock()

	if err != nil {
		logLvl := 2

		if time.Since(reg.lastErrorLogAt) >= scrapeErrLogMinInterval {
			logLvl = 1
			reg.lastErrorLogAt = time.Now().Truncate(time.Second)
		}

		if len(mfs) == 0 {
			logger.V(logLvl).Printf("Gather of metrics failed on %s: %v", reg.option.Description, err)
		} else {
			// When there is points, log at lower level because we know that some gatherers always
			// fail on some setup. Also, node_exporter may send "node_rapl_package_joules_total" duplicated.
			logger.V(logLvl).Printf("Gather of metrics failed on %s, some metrics may be missing: %v", reg.option.Description, err)
		}
	}

	reg.lastScrapes = append(reg.lastScrapes, scrapeRun{ScrapeAt: t0, ScrapeDuration: duration, ScrapedPointsCount: len(mfs), Error: err})

	if len(reg.lastScrapes) > maxLastScrape {
		reg.lastScrapes = reg.lastScrapes[len(reg.lastScrapes)-maxLastScrape:]
	}

	reg.l.Unlock()

	// Don't drop the meta labels here, they are needed for relabeling.
	points := gloutonModel.FamiliesToMetricPoints(t0, mfs, !reg.option.ApplyDynamicRelabel)

	if (reg.annotations != types.MetricAnnotations{}) {
		for i := range points {
			points[i].Annotations = points[i].Annotations.Merge(reg.annotations)
		}
	}

	if reg.option.ApplyDynamicRelabel {
		points = r.relabelPoints(ctx, points)
	}

	// Apply the thresholds after relabeling to get the instance UUID in the labels.
	if r.option.ThresholdHandler != nil {
		var statusPoints []types.MetricPoint

		points, statusPoints = r.option.ThresholdHandler.ApplyThresholds(points)
		points = append(points, statusPoints...)
	}

	if reg.option.AcceptAllowedMetricsOnly {
		points = r.option.Filter.FilterPoints(points, true)
	}

	if len(points) > 0 && r.option.PushPoint != nil {
		r.option.PushPoint.PushPoints(ctx, points)
	}
}

func (r *Registry) scrape(ctx context.Context, state GatherState, reg *registration) ([]*dto.MetricFamily, time.Duration, error) {
	r.l.Lock()
	reg.l.Lock()

	if reg.relabelHookSkip && time.Since(reg.lastRelabelHookRetry) > hookRetryDelay {
		restartNeeded := r.setupGatherer(reg, reg.gatherer.getSource())
		if restartNeeded {
			go func() {
				defer crashreport.ProcessPanic()

				// Run restartLoop in a goroutine because restartLoop will wait
				// for scrape() to terminate before returning.
				r.restartLoop(reg)
			}()
		}
	}

	r.l.Unlock()

	if reg.relabelHookSkip {
		reg.l.Unlock()

		return nil, 0, nil
	}

	secretInput, hasSecrets := reg.gatherer.source.(inputs.SecretfulInput)
	gatherMethod := reg.gatherer.GatherWithState

	reg.l.Unlock()

	if hasSecrets && secretInput.SecretCount() > 0 {
		if r.option.SecretInputsGate == nil {
			return nil, 0, fmt.Errorf("%w: no secretInputsGate but input had SecretCount()", ErrBadArgument)
		}

		releaseGate, err := WaitForSecrets(ctx, r.option.SecretInputsGate, secretInput.SecretCount())
		if err != nil {
			return nil, 0, err // The context expired
		}

		defer releaseGate()
	}

	start := time.Now()

	mfs, err := gatherMethod(ctx, state)

	return mfs, time.Since(start), err
}

func (r *Registry) restartLoop(reg *registration) {
	r.startStopLock.Lock()
	defer r.startStopLock.Unlock()

	r.l.Lock()
	stopping := !r.running
	r.l.Unlock()

	reg.l.Lock()
	defer reg.l.Unlock()

	if reg.loop != nil {
		loop := reg.loop
		reg.loop = nil

		reg.l.Unlock()
		loop.stop()
		reg.l.Lock()
	}

	if stopping {
		return
	}

	timeout := reg.interval * 8 / 10
	if reg.option.Timeout != 0 && reg.option.Timeout < reg.interval {
		timeout = reg.option.Timeout
	}

	reg.loop = startScrapeLoop(
		reg.interval,
		timeout,
		reg.option.JitterSeed,
		func(ctx context.Context, loopCtx context.Context, t0 time.Time) {
			r.scrapeFromLoop(ctx, loopCtx, t0, reg)
		},
		reg.option.Description,
		reg.runOnStart,
	)
}

// pushPoint add a new point to the list of pushed point with a specified TTL.
// As for AddMetricPointFunction, points should not be mutated after the call.
func (r *Registry) pushPoint(ctx context.Context, points []types.MetricPoint, ttl time.Duration) {
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
			err       error
			skip      bool
			newLabels labels.Labels
		)

		point.Labels, err = fixLabels(point.Labels)
		if err != nil {
			logger.V(2).Printf("Ignoring metric %v: %v", point.Labels, err)

			continue
		}

		point.Labels = r.addMetaLabels(point.Labels, RegistrationOption{})

		// Add annotation to meta-label, which allow relabel to work correctly.
		for _, lbl := range gloutonModel.AnnotationToMetaLabels(nil, point.Annotations) {
			point.Labels[lbl.Name] = lbl.Value
		}

		newLabelsMap := map[string]string{
			types.LabelName: point.Labels[types.LabelName],
		}

		if point.Labels[types.LabelItem] != "" {
			newLabelsMap[types.LabelItem] = point.Labels[types.LabelItem]
		}

		// Kept meta-labels
		for k, v := range point.Labels {
			if strings.HasPrefix(k, model.ReservedLabelPrefix) {
				newLabelsMap[k] = v
			}
		}

		point.Labels = newLabelsMap

		ctx, cancel := context.WithTimeout(ctx, relabelTimeout)
		defer cancel()

		newLabels, _, _, skip = r.applyRelabel(ctx, point.Labels)
		point.Labels = newLabels.Map()

		// It's possible to have container_name and item labels that both exists and contains the same value.
		// If that the case, drop the container_name label.
		if point.Labels[types.LabelContainerName] == point.Labels[types.LabelItem] && point.Labels[types.LabelContainerName] != "" {
			delete(point.Labels, types.LabelContainerName)
		}

		if !skip {
			points[n] = point
			n++
		}
	}

	points = points[:n]

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

func (r *Registry) addMetaLabels(input map[string]string, opts RegistrationOption) (result map[string]string) {
	if input == nil {
		result = make(map[string]string)
	} else {
		result = maps.Clone(input)
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

	if opts.InstanceUseContainerName {
		result[types.LabelMetaInstanceUseContainerName] = "yes"
	}

	return result
}

func (r *Registry) relabelPoints(ctx context.Context, points []types.MetricPoint) []types.MetricPoint {
	n := 0

	for _, point := range points {
		ctx, cancel := context.WithTimeout(ctx, relabelTimeout)
		defer cancel()

		newLabels, _, newAnnotations, skip := r.applyRelabel(ctx, point.Labels)
		point.Labels = newLabels.Map()
		point.Annotations = point.Annotations.Merge(newAnnotations)

		if !skip {
			points[n] = point
			n++
		}
	}

	return points[:n]
}

func (r *Registry) applyRelabel(
	ctx context.Context,
	input map[string]string,
) (promLabels labels.Labels, labelsWithMeta map[string]string, annotations types.MetricAnnotations, retryLater bool) {
	if r.relabelHook != nil {
		ctx, cancel := context.WithTimeout(ctx, relabelTimeout)
		input, retryLater = r.relabelHook(ctx, input)

		cancel()
	}

	promLabels = labels.FromMap(input)
	annotations = gloutonModel.MetaLabelsToAnnotation(promLabels)

	promLabels, keep := relabel.Process(promLabels, r.relabelConfigs...)
	if !keep {
		return promLabels, labelsWithMeta, annotations, retryLater
	}

	labelsWithMeta = promLabels.Map()

	promLabels = gloutonModel.DropMetaLabels(promLabels)

	return promLabels, labelsWithMeta, annotations, retryLater
}

// setupGatherer assume reg.l and r.l locks are held.
func (r *Registry) setupGatherer(reg *registration, source prometheus.Gatherer) (restartNeeded bool) {
	var (
		promLabels     labels.Labels
		labelsWithMeta map[string]string
		annotations    types.MetricAnnotations
	)

	if !reg.option.NoLabelsAlteration {
		extraLabels := r.addMetaLabels(reg.option.ExtraLabels, reg.option)

		reg.relabelHookSkip = false

		if r.relabelHook != nil {
			reg.lastRelabelHookRetry = time.Now()
		}

		ctxTimeout, cancel := context.WithTimeout(context.Background(), relabelTimeout)
		defer cancel()

		promLabels, labelsWithMeta, annotations, reg.relabelHookSkip = r.applyRelabel(ctxTimeout, extraLabels)
		reg.labelsWithMeta = labelsWithMeta
	}

	var retryLater bool

	reg.hookMinimalInterval, retryLater = r.minimalIntervalHook(labelsWithMeta)
	reg.interval = max(reg.option.MinInterval, reg.hookMinimalInterval, gloutonMinimalInterval)
	reg.relabelHookSkip = reg.relabelHookSkip || retryLater

	g := newWrappedGatherer(source, promLabels, reg.option)
	reg.annotations = annotations
	reg.gatherer = g

	return reg.loop == nil || reg.loop.interval != reg.interval
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

// minimalIntervalHook returns the minimal gathering interval
// according to the current updateDelayHook.
// If the updateDelayHook is not defined, it returns 0.
// It assumes that the r.l lock is held.
func (r *Registry) minimalIntervalHook(labels map[string]string) (time.Duration, bool) {
	if r.updateDelayHook != nil {
		return r.updateDelayHook(labels)
	}

	return 0, false
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

// WaitForSecrets hold the current goroutine until the given number of slots is taken.
// This is to ensure that too many inputs with secrets don't run at the same time,
// which would result in exceeding the locked memory limit.
// WaitForSecrets returns a callback to release all the slots taken once the gathering is done,
// or an error if the given context expired.
func WaitForSecrets(ctx context.Context, secretsGate *gate.Gate, slotsNeeded int) (func(), error) {
	releaseGates := func(gatesCrossed int) {
		for range gatesCrossed {
			secretsGate.Done()
		}
	}

fromZero:
	for {
		for slotsTaken := range slotsNeeded {
			passGateCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)

			err := secretsGate.Start(passGateCtx)

			cancel()

			if err != nil {
				releaseGates(slotsTaken)

				if ctx.Err() != nil {
					return nil, ctx.Err()
				}

				// Give way to another input ... then retry from zero
				time.Sleep(delay.JitterMs(3*time.Millisecond, 0.9))

				continue fromZero
			}
		}

		break // All the needed slots have been taken
	}

	return func() { releaseGates(slotsNeeded) }, nil
}
