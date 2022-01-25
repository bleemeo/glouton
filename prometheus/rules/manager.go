// Copyright 2015-2021 Bleemeo
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

package rules

import (
	"context"
	"errors"
	"fmt"
	"glouton/logger"
	"glouton/prometheus/model"
	"glouton/threshold"
	"glouton/types"
	"math"
	"runtime"
	"sync"
	"time"

	"github.com/go-kit/log"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/rules"
	"github.com/prometheus/prometheus/storage"
)

// promAlertTime represents the duration for which the alerting rule
// should exceed the threshold to be considered fired.
const promAlertTime = 5 * time.Minute

// minResetTime is the minimum time used by the inactive checks.
// The bleemeo connector uses 15 seconds as a synchronization time,
// thus we add a bit a leeway in the disabled countdown.
const minResetTime = 17 * time.Second

const (
	lowWarningState   = "low_warning"
	highWarningState  = "high_warning"
	lowCriticalState  = "low_critical"
	highCriticalState = "high_critical"
)

var (
	errUnknownState = errors.New("unknown state for metric")
	errSkipPoints   = errors.New("skip points")
)

// Manager is a wrapper handling everything related to prometheus recording
// and alerting rules.
type Manager struct {
	recordingRules []*rules.Group
	alertingRules  map[string]*alertRuleGroup

	appendable *dynamicAppendable
	queryable  storage.Queryable
	engine     *promql.Engine
	logger     log.Logger

	l                sync.Mutex
	now              func() time.Time
	agentStarted     time.Time
	metricResolution time.Duration
}

// MetricAlertRule is a metric that will execute a PromQL to generate a status.
type MetricAlertRule struct {
	// Labels of the resulting metric
	Labels      labels.Labels
	PromQLQuery string
	Threshold   threshold.Threshold
	// InstanceUUID is the instance used to execute the query, a matcher instance_uuid=$THIS_VALUE will be applied.
	InstanceUUID string
	Resolution   time.Duration
	// IsUserPromQLAlert tells whether this rule is created by a user or an automatically generated one.
	IsUserPromQLAlert bool
}

type alertRuleGroup struct {
	rules         map[string]*rules.AlertingRule
	instanceID    string
	inactiveSince time.Time
	disabledUntil time.Time
	pointsRead    int32
	lastRun       time.Time
	lastErr       error
	lastStatus    types.StatusDescription

	metric  MetricAlertRule
	isError string
}

//nolint:gochecknoglobals
var (
	defaultLinuxRecordingRules = map[string]string{
		"node_cpu_seconds_global": "sum(node_cpu_seconds_total) without (cpu)",
	}
	defaultWindowsRecordingRules = map[string]string{
		"windows_cpu_time_global":            "sum(windows_cpu_time_total) without(core)",
		"windows_memory_standby_cache_bytes": "windows_memory_standby_cache_core_bytes+windows_memory_standby_cache_normal_priority_bytes+windows_memory_standby_cache_reserve_bytes",
	}
)

func NewManager(ctx context.Context, queryable storage.Queryable, metricResolution time.Duration) *Manager {
	defaultRules := defaultLinuxRecordingRules
	if runtime.GOOS == "windows" {
		defaultRules = defaultWindowsRecordingRules
	}

	return newManager(ctx, queryable, defaultRules, time.Now(), metricResolution)
}

func newManager(ctx context.Context, queryable storage.Queryable, defaultRules map[string]string, created time.Time, metricResolution time.Duration) *Manager {
	promLogger := logger.GoKitLoggerWrapper(logger.V(1))
	engine := promql.NewEngine(promql.EngineOpts{
		Logger:             log.With(promLogger, "component", "query engine"),
		Reg:                nil,
		MaxSamples:         50000000,
		Timeout:            2 * time.Minute,
		ActiveQueryTracker: nil,
		LookbackDelta:      5 * time.Minute,
	})

	app := &dynamicAppendable{}

	defaultGroupRules := []rules.Rule{}

	for metricName, val := range defaultRules {
		exp, err := parser.ParseExpr(val)
		if err != nil {
			logger.V(2).Printf("An error occurred while parsing expression %s: %v. This rule was not registered", val, err)
		} else {
			newRule := rules.NewRecordingRule(metricName, exp, labels.Labels{})
			defaultGroupRules = append(defaultGroupRules, newRule)
		}
	}

	// We should use a queryable that limit to a given instance_uuid to avoid cross-agent
	// recording rules (and thus have multiple rules Group).
	// But this isn't yet needed because currently rules are only configured by code and
	// current hard-coded rules will kept the instance_uuid label which avoid cross-agent.
	defaultGroup := rules.NewGroup(rules.GroupOptions{
		Name:          "default",
		Rules:         defaultGroupRules,
		ShouldRestore: true,
		Opts: &rules.ManagerOptions{
			Context:    ctx,
			Logger:     log.With(promLogger, "component", "rules manager"),
			Appendable: app,
			Queryable:  queryable,
			QueryFunc:  rules.EngineQueryFunc(engine, queryable),
			NotifyFunc: func(ctx context.Context, expr string, alerts ...*rules.Alert) {
				if len(alerts) == 0 {
					return
				}

				logger.V(2).Printf("notification triggered for expression %x with state %v and labels %v", expr, alerts[0].State, alerts[0].Labels)
			},
		},
	})

	rm := Manager{
		appendable:       app,
		queryable:        queryable,
		engine:           engine,
		recordingRules:   []*rules.Group{defaultGroup},
		alertingRules:    map[string]*alertRuleGroup{},
		logger:           promLogger,
		agentStarted:     created,
		metricResolution: 0,
		now:              time.Now,
	}

	rm.UpdateMetricResolution(metricResolution)

	return &rm
}

// UpdateMetricResolution updates the metric resolution time.
func (rm *Manager) UpdateMetricResolution(metricResolution time.Duration) {
	rm.l.Lock()
	defer rm.l.Unlock()

	// rm.metricResolution is the minimum time before actually sending
	// any alerting points. Some metrics can a take bit of time before
	// first points creation; we do not want them to be labelled as unknown on start.
	rm.metricResolution = metricResolution*2 + 10*time.Second
}

// MetricList returns a list of all alerting rules metric names.
// This is used for dynamic generation of filters.
func (rm *Manager) MetricList() []string {
	res := make([]string, 0, len(rm.alertingRules))

	rm.l.Lock()
	defer rm.l.Unlock()

	for _, r := range rm.alertingRules {
		res = append(res, r.metric.Labels.Get(types.LabelName))
	}

	return res
}

func (rm *Manager) Collect(ctx context.Context, app storage.Appender) error {
	var errs types.MultiErrors

	res := []types.MetricPoint{}
	now := rm.now()

	rm.appendable.SetAppendable(model.NewFromAppender(app))
	defer rm.appendable.SetAppendable(nil)

	rm.l.Lock()

	for _, rgr := range rm.recordingRules {
		rgr.Eval(ctx, now)
	}

	for _, agr := range rm.alertingRules {
		point, err := agr.runGroup(ctx, now, rm)

		if err != nil && !errors.Is(err, errSkipPoints) {
			errs = append(errs, fmt.Errorf("error occurred while trying to execute rules: %w", err))
		} else if !errors.Is(err, errSkipPoints) {
			res = append(res, point)
		}
	}

	rm.l.Unlock()

	if len(res) != 0 {
		logger.V(2).Printf("Sending %d new alert to the api", len(res))

		err := model.SendPointsToAppender(res, app)
		if err != nil {
			errs = append(errs, fmt.Errorf("error occurred while appending points: %w", err))
		}

		err = app.Commit()
		if err != nil {
			errs = append(errs, fmt.Errorf("error occurred while commit the appender: %w", err))
		}
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

func (agr *alertRuleGroup) shouldSkip(now time.Time) bool {
	if !agr.inactiveSince.IsZero() && !agr.metric.IsUserPromQLAlert {
		if agr.disabledUntil.IsZero() && now.After(agr.inactiveSince.Add(2*time.Minute)) {
			logger.V(2).Printf("rule %s has been disabled for the last 2 minutes. retrying this metric in 10 minutes", agr.metric.Labels.String())
			agr.disabledUntil = now.Add(10 * time.Minute)
		}

		if now.After(agr.disabledUntil) {
			logger.V(2).Printf("Inactive rule %s will be re executed. Time since inactive: %s", agr.metric.Labels.String(), agr.inactiveSince.Format(time.RFC3339))
			agr.disabledUntil = now.Add(10 * time.Minute)
		} else {
			return true
		}
	}

	if agr.metric.Resolution != 0 && now.Add(time.Second).Sub(agr.lastRun) < agr.metric.Resolution {
		return true
	}

	return false
}

func (agr *alertRuleGroup) runGroup(ctx context.Context, now time.Time, rm *Manager) (types.MetricPoint, error) {
	thresholdOrder := []string{highCriticalState, lowCriticalState, highWarningState, lowWarningState}

	var generatedPoint types.MetricPoint

	if agr.shouldSkip(now) {
		return types.MetricPoint{}, errSkipPoints
	}

	if agr.isError != "" {
		return types.MetricPoint{
			Point: types.Point{
				Time:  now,
				Value: float64(types.StatusUnknown.NagiosCode()),
			},
			Labels: agr.metric.Labels.Map(),
			Annotations: types.MetricAnnotations{
				Status: types.StatusDescription{
					CurrentStatus:     types.StatusUnknown,
					StatusDescription: "Invalid PromQL: " + agr.isError,
				},
			},
		}, nil
	}

	agr.lastRun = rm.now()
	agr.pointsRead = 0
	agr.lastStatus = types.StatusDescription{}
	agr.lastErr = nil

	for _, val := range thresholdOrder {
		rule := agr.rules[val]

		if rule == nil {
			continue
		}

		prevState := rule.State()

		tmp, err := newFilterQueryable(
			rm.queryable,
			map[string]string{
				types.LabelInstanceUUID: agr.instanceID,
			},
		)
		if err != nil {
			agr.lastErr = err

			return types.MetricPoint{}, err
		}

		queryable := newCoutingQueryable(tmp)

		_, err = rule.Eval(ctx, now, rules.EngineQueryFunc(rm.engine, queryable), nil, 100)
		if err != nil {
			agr.lastErr = err

			return types.MetricPoint{}, err
		}

		state := rule.State()

		agr.pointsRead = queryable.Count()

		if agr.pointsRead == 0 {
			if now.Sub(rm.agentStarted) < 2*time.Minute {
				return types.MetricPoint{}, errSkipPoints
			}

			return agr.checkNoPoint(now, rm.agentStarted, rm.metricResolution)
		}

		agr.inactiveSince = time.Time{}
		agr.disabledUntil = time.Time{}

		// we add 20 seconds to the promAlert time to compensate the delta between agent startup and first metrics
		if state != rules.StateInactive && now.Sub(rm.agentStarted) < promAlertTime+20*time.Second {
			agr.lastErr = err

			return types.MetricPoint{}, errSkipPoints
		}

		logger.V(2).Printf("metric state for %s previous state=%v, new state=%v", rule.Labels().String(), prevState, state)

		newPoint, err := agr.generateNewPoint(val, rule, state, now)
		if err != nil {
			agr.lastErr = err

			return types.MetricPoint{}, err
		}

		if state == rules.StateFiring {
			agr.lastStatus = newPoint.Annotations.Status

			return newPoint, nil
		} else if generatedPoint.Labels == nil || newPoint.Annotations.Status.CurrentStatus > generatedPoint.Annotations.Status.CurrentStatus {
			generatedPoint = newPoint
		}
	}

	agr.lastStatus = generatedPoint.Annotations.Status

	return generatedPoint, nil
}

func (agr *alertRuleGroup) checkNoPoint(now time.Time, agentStart time.Time, metricRes time.Duration) (types.MetricPoint, error) {
	if agr.metric.IsUserPromQLAlert && now.Sub(agentStart) > metricRes {
		return types.MetricPoint{
			Point: types.Point{
				Time:  now,
				Value: float64(types.StatusUnknown.NagiosCode()),
			},
			Labels: agr.metric.Labels.Map(),
			Annotations: types.MetricAnnotations{
				Status: types.StatusDescription{
					CurrentStatus:     types.StatusUnknown,
					StatusDescription: "PromQL read zero points. The PromQL may be incorrect or the source measurement may have disappeared",
				},
			},
		}, nil
	}

	if agr.inactiveSince.IsZero() {
		agr.inactiveSince = now
	}

	return types.MetricPoint{}, errSkipPoints
}

func (agr *alertRuleGroup) generateNewPoint(thresholdType string, rule *rules.AlertingRule, state rules.AlertState, now time.Time) (types.MetricPoint, error) {
	statusCode := statusFromThreshold(thresholdType)
	alerts := rule.ActiveAlerts()

	desc := "Current value is within the thresholds."

	if len(alerts) != 0 {
		exceeded := ""

		if state != rules.StateFiring {
			exceeded = "not "
		}

		desc = fmt.Sprintf("Current value: %s. Threshold (%s) %sexceeded for the last %s",
			threshold.FormatValue(alerts[0].Value, threshold.Unit{}),
			threshold.FormatValue(agr.lowestThreshold(), threshold.Unit{}),
			exceeded, promAlertTime.String())
	}

	status := types.StatusDescription{
		CurrentStatus:     statusCode,
		StatusDescription: desc,
	}

	if statusCode == types.StatusUnknown {
		return types.MetricPoint{}, fmt.Errorf("%w %s", errUnknownState, rule.Labels().String())
	}

	newPoint := types.MetricPoint{
		Point: types.Point{
			Time:  now,
			Value: float64(statusCode.NagiosCode()),
		},
		Labels: agr.metric.Labels.Map(),
		Annotations: types.MetricAnnotations{
			Status: status,
		},
	}

	if state == rules.StatePending || state == rules.StateInactive {
		statusCode = types.StatusOk
		newPoint.Value = float64(statusCode.NagiosCode())
		newPoint.Annotations.Status.CurrentStatus = statusCode
	}

	return newPoint, nil
}

func (agr *alertRuleGroup) lowestThreshold() float64 {
	orderToPrint := []string{lowWarningState, highWarningState, lowCriticalState, highCriticalState}

	for _, thr := range orderToPrint {
		_, ok := agr.rules[thr]
		if !ok {
			continue
		}

		return agr.thresholdFromString(thr)
	}

	logger.V(2).Printf("An unknown error occurred: not threshold found for rule %s\n", agr.metric.Labels.String())

	return 0
}

func (agr *alertRuleGroup) thresholdFromString(threshold string) float64 {
	switch threshold {
	case highCriticalState:
		return agr.metric.Threshold.HighCritical
	case lowCriticalState:
		return agr.metric.Threshold.LowCritical
	case highWarningState:
		return agr.metric.Threshold.HighWarning
	case lowWarningState:
		return agr.metric.Threshold.LowWarning
	default:
		return agr.metric.Threshold.HighCritical
	}
}

func (rm *Manager) addAlertingRule(metric MetricAlertRule, isError string) error {
	lbls := metric.Labels

	if lbls.Get(types.LabelInstanceUUID) != metric.InstanceUUID {
		builder := labels.NewBuilder(lbls)

		builder.Set(types.LabelInstanceUUID, metric.InstanceUUID)
		lbls = builder.Labels()
	}

	newGroup := &alertRuleGroup{
		rules:         make(map[string]*rules.AlertingRule, 4),
		instanceID:    metric.InstanceUUID,
		inactiveSince: time.Time{},
		disabledUntil: time.Time{},
		metric:        metric,
		isError:       isError,
	}

	if !math.IsNaN(metric.Threshold.LowWarning) {
		err := newGroup.newRule(
			fmt.Sprintf("(%s) < %f", metric.PromQLQuery, metric.Threshold.LowWarning),
			metric.Labels,
			lowWarningState,
			rm.logger,
		)
		if err != nil {
			return err
		}
	}

	if !math.IsNaN(metric.Threshold.HighWarning) {
		err := newGroup.newRule(
			fmt.Sprintf("(%s) > %f", metric.PromQLQuery, metric.Threshold.HighWarning),
			metric.Labels,
			highWarningState,
			rm.logger,
		)
		if err != nil {
			return err
		}
	}

	if !math.IsNaN(metric.Threshold.LowCritical) {
		err := newGroup.newRule(
			fmt.Sprintf("(%s) < %f", metric.PromQLQuery, metric.Threshold.LowCritical),
			metric.Labels,
			lowCriticalState,
			rm.logger,
		)
		if err != nil {
			return err
		}
	}

	if !math.IsNaN(metric.Threshold.HighCritical) {
		err := newGroup.newRule(
			fmt.Sprintf("(%s) > %f", metric.PromQLQuery, metric.Threshold.HighCritical),
			metric.Labels,
			highCriticalState,
			rm.logger,
		)
		if err != nil {
			return err
		}
	}

	rm.alertingRules[lbls.String()] = newGroup

	return nil
}

func (agr *alertRuleGroup) Equal(threshold threshold.Threshold) bool {
	return agr.metric.Threshold.Equal(threshold)
}

// RebuildAlertingRules rebuild the alerting rules list from a bleemeo api metric list.
func (rm *Manager) RebuildAlertingRules(metricsList []MetricAlertRule) error {
	rm.l.Lock()
	defer rm.l.Unlock()

	old := rm.alertingRules

	rm.alertingRules = make(map[string]*alertRuleGroup)

	for _, metric := range metricsList {
		if len(metric.PromQLQuery) == 0 || metric.Threshold.IsZero() {
			continue
		}

		key := metric.Labels.String()
		prevInstance, ok := old[key]

		if ok && prevInstance.instanceID != metric.InstanceUUID {
			ok = false
		}

		if ok && prevInstance.metric.PromQLQuery == metric.PromQLQuery && prevInstance.Equal(metric.Threshold) {
			rm.alertingRules[key] = prevInstance
		} else {
			err := rm.addAlertingRule(metric, "")
			if err != nil {
				metric.PromQLQuery = "not_used"
				metric.IsUserPromQLAlert = true

				_ = rm.addAlertingRule(metric, err.Error())
			}
		}
	}

	return nil
}

func (rm *Manager) ResetInactiveRules() {
	now := time.Now().Truncate(time.Second)

	for _, val := range rm.alertingRules {
		if val.disabledUntil.IsZero() {
			continue
		}

		if now.Add(minResetTime).Before(val.disabledUntil) {
			val.disabledUntil = now.Add(minResetTime)
		}
	}
}

func (rm *Manager) DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("alertings-recording-rules.txt")
	if err != nil {
		return err
	}

	rm.l.Lock()
	defer rm.l.Unlock()

	fmt.Fprintf(file, "# Recording rules (%d entries)\n", len(rm.recordingRules))

	for _, gr := range rm.recordingRules {
		fmt.Fprintf(file, "# group %s\n", gr.Name())

		for _, r := range gr.Rules() {
			fmt.Fprintf(file, "%s\n", r.String())
		}
	}

	activeAlertingRules := 0

	for _, r := range rm.alertingRules {
		if r.inactiveSince.IsZero() {
			activeAlertingRules++
		}
	}

	fmt.Fprintf(file, "# Active Alerting rules (%d entries)\n", activeAlertingRules)

	for _, r := range rm.alertingRules {
		if r.inactiveSince.IsZero() {
			fmt.Fprintf(file, "%s\n", r.String())
		}
	}

	fmt.Fprintf(file, "\n# Inactive Alerting Rules (%d entries)\n", len(rm.alertingRules)-activeAlertingRules)

	for _, r := range rm.alertingRules {
		if !r.inactiveSince.IsZero() {
			fmt.Fprintf(file, "%s\n", r.String())
		}
	}

	return nil
}

func (agr *alertRuleGroup) newRule(exp string, lbls labels.Labels, threshold string, logger log.Logger) error {
	newExp, err := parser.ParseExpr(exp)
	if err != nil {
		return err
	}

	newRule := rules.NewAlertingRule(
		"unused",
		newExp,
		promAlertTime,
		lbls,
		nil,
		labels.Labels{},
		"",
		true,
		log.With(logger, "component", "alert_rule", "metric", lbls.String(), "theshold", threshold),
	)

	agr.rules[threshold] = newRule

	return nil
}

func (agr *alertRuleGroup) String() string {
	return fmt.Sprintf("id=%s query=%#v \n\tinactive_since=%v disabled_until=%v is_user_promql_alert=%v\n\tlastRun=%v pointsRead=%d status=%v err=%v\n%v",
		agr.metric.Labels.String(), agr.metric.PromQLQuery, agr.inactiveSince, agr.disabledUntil, agr.metric.IsUserPromQLAlert, agr.lastRun, agr.pointsRead, agr.lastStatus, agr.lastErr, agr.query())
}

func (agr *alertRuleGroup) query() string {
	res := ""

	for key, val := range agr.rules {
		res += fmt.Sprintf("\tThreshold_%s: %s; current state=%s\n", key, val.Query().String(), val.State())
	}

	return res
}

func statusFromThreshold(s string) types.Status {
	switch s {
	case lowWarningState, highWarningState:
		return types.StatusWarning
	case lowCriticalState, highCriticalState:
		return types.StatusCritical
	default:
		return types.StatusUnknown
	}
}
