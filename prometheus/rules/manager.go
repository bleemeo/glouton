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
	"glouton/types"
	"runtime"
	"sort"
	"strings"
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
	ruleGroups     map[string]*alertRuleGroup

	appendable *dynamicAppendable
	queryable  storage.Queryable
	engine     *promql.Engine
	logger     log.Logger

	l                sync.Mutex
	now              func() time.Time
	agentStarted     time.Time
	metricResolution time.Duration
}

// PromQLRule is a rule that runs on a single agent.
type PromQLRule struct {
	ID                  string
	Name                string
	WarningQuery        string
	WarningDelaySecond  int
	CriticalQuery       string
	CriticalDelaySecond int
	InstanceID          string
	Resolution          time.Duration
}

type alertRuleGroup struct {
	warningRule  *rules.AlertingRule
	criticalRule *rules.AlertingRule

	instanceID    string
	inactiveSince time.Time
	disabledUntil time.Time
	pointsRead    int32
	lastRun       time.Time
	lastErr       error
	lastStatus    types.StatusDescription

	promqlRule PromQLRule
	resolution time.Duration
	isError    string
}

type alertMinimal struct {
	Labels labels.Labels
	Value  float64
	Status types.Status
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
		ruleGroups:       map[string]*alertRuleGroup{},
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

// MetricNames returns a list of all alerting rules metric names.
// This is used for dynamic generation of filters.
func (rm *Manager) MetricNames() []string {
	names := make([]string, 0, len(rm.ruleGroups))

	rm.l.Lock()
	defer rm.l.Unlock()

	for _, r := range rm.ruleGroups {
		names = append(names, r.promqlRule.Name)
	}

	return names
}

func (rm *Manager) Collect(ctx context.Context, app storage.Appender) error {
	var errs types.MultiErrors

	res := []types.MetricPoint{} //nolint:ifshort // False positive.
	now := rm.now()

	rm.appendable.SetAppendable(model.NewFromAppender(app))
	defer rm.appendable.SetAppendable(nil)

	rm.l.Lock()

	for _, rgr := range rm.recordingRules {
		rgr.Eval(ctx, now)
	}

	for _, agr := range rm.ruleGroups {
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
	if !agr.inactiveSince.IsZero() {
		if agr.disabledUntil.IsZero() && now.After(agr.inactiveSince.Add(2*time.Minute)) {
			logger.V(2).Printf("rule %s has been disabled for the last 2 minutes. retrying this metric in 10 minutes", agr.promqlRule.ID)
			agr.disabledUntil = now.Add(10 * time.Minute)
		}

		if now.After(agr.disabledUntil) {
			logger.V(2).Printf("Inactive rule %s will be re executed. Time since inactive: %s", agr.promqlRule.ID, agr.inactiveSince.Format(time.RFC3339))
			agr.disabledUntil = now.Add(10 * time.Minute)
		} else {
			return true
		}
	}

	if agr.resolution != 0 && now.Add(time.Second).Sub(agr.lastRun) < agr.resolution {
		return true
	}

	return false
}

func (agr *alertRuleGroup) runGroup(ctx context.Context, now time.Time, rm *Manager) (types.MetricPoint, error) {
	if agr.shouldSkip(now) {
		return types.MetricPoint{}, errSkipPoints
	}

	if agr.isError != "" {
		return types.MetricPoint{
			Point: types.Point{
				Time:  now,
				Value: float64(types.StatusUnknown.NagiosCode()),
			},
			Labels: labelsMap(agr.promqlRule),
			Annotations: types.MetricAnnotations{
				Status: types.StatusDescription{
					CurrentStatus:     types.StatusUnknown,
					StatusDescription: "Invalid PromQL: " + agr.isError,
				},
				AlertingRuleID: agr.promqlRule.ID,
			},
		}, nil
	}

	agr.lastRun = rm.now()
	agr.pointsRead = 0
	agr.lastStatus = types.StatusDescription{}
	agr.lastErr = nil
	agr.inactiveSince = time.Time{}
	agr.disabledUntil = time.Time{}

	for _, rule := range []*rules.AlertingRule{agr.warningRule, agr.criticalRule} {
		if rule == nil {
			continue
		}

		tmp, err := newFilterQueryable(
			rm.queryable,
			map[string]string{
				// TODO: Don't hardcode this.
				types.LabelInstanceUUID: "fdfedf2a-9441-4b9d-8d6f-e82ce8a820d5",
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

		agr.pointsRead += queryable.Count()
	}

	if agr.pointsRead == 0 {
		if now.Sub(rm.agentStarted) < 2*time.Minute {
			return types.MetricPoint{}, errSkipPoints
		}

		return agr.checkNoPoint(now, rm.agentStarted, rm.metricResolution)
	}

	var warningState, criticalState rules.AlertState
	if agr.warningRule != nil {
		warningState = agr.warningRule.State()
	}
	if agr.criticalRule != nil {
		criticalState = agr.criticalRule.State()
	}

	// we add 20 seconds to the promAlert time to compensate the delta between agent startup and first metrics
	if now.Sub(rm.agentStarted) < promAlertTime+20*time.Second &&
		warningState != rules.StateInactive && criticalState != rules.StateInactive {
		return types.MetricPoint{}, errSkipPoints
	}

	newPoint, err := agr.generateNewPoint(warningState, criticalState, now)
	if err != nil {
		agr.lastErr = err

		return types.MetricPoint{}, err
	}

	agr.lastStatus = newPoint.Annotations.Status

	// Be careful not to send an OK point if one of the rules is pending,
	// otherwise we might send wrong OK points at the start of the agent.
	if newPoint.Annotations.Status.CurrentStatus == types.StatusOk &&
		(warningState == rules.StatePending || criticalState == rules.StatePending) {
		return types.MetricPoint{}, fmt.Errorf("%w: status ok with a pending rule", errSkipPoints)
	}

	return newPoint, nil
}

func (agr *alertRuleGroup) checkNoPoint(now time.Time, agentStart time.Time, metricRes time.Duration) (types.MetricPoint, error) {
	if now.Sub(agentStart) > metricRes {
		return types.MetricPoint{
			Point: types.Point{
				Time:  now,
				Value: float64(types.StatusUnknown.NagiosCode()),
			},
			Labels: labelsMap(agr.promqlRule),
			Annotations: types.MetricAnnotations{
				Status: types.StatusDescription{
					CurrentStatus:     types.StatusUnknown,
					StatusDescription: "PromQL read zero points. The PromQL may be incorrect or the source measurement may have disappeared",
				},
				AlertingRuleID: agr.promqlRule.ID,
			},
		}, nil
	}

	if agr.inactiveSince.IsZero() {
		agr.inactiveSince = now
	}

	return types.MetricPoint{}, errSkipPoints
}

func (agr *alertRuleGroup) generateNewPoint(
	warningState, criticalState rules.AlertState,
	now time.Time,
) (types.MetricPoint, error) {
	warningStatus := statusFromAlertState(warningState, types.StatusWarning)
	criticalStatus := statusFromAlertState(criticalState, types.StatusCritical)

	lbls := agr.criticalRule.Labels()
	status := criticalStatus
	if warningStatus > criticalStatus {
		lbls = agr.warningRule.Labels()
		status = warningStatus
	}

	newPoint := types.MetricPoint{
		Point: types.Point{
			Time:  now,
			Value: float64(status.NagiosCode()),
		},
		Labels: lbls.Map(),
		Annotations: types.MetricAnnotations{
			Status: types.StatusDescription{
				CurrentStatus:     status,
				StatusDescription: agr.ruleDescription(),
			},
			AlertingRuleID: agr.promqlRule.ID,
		},
	}

	return newPoint, nil
}

// ruleDescription returns the description for a rule.
//
// The description is formatted as follows:
// Current value:
// instance="myserver:8015",item="/": 71.67,
// instance="myserver:8015",item="/boot": 39.61,
// instance="myserver:8015",item="/home": 29.39.
func (agr *alertRuleGroup) ruleDescription() string {
	alertsMap := agr.deduplicateAlerts()
	if len(alertsMap) == 0 {
		return "Everything is running fine"
	}

	points := make([]alertMinimal, 0, len(alertsMap))
	for _, point := range alertsMap {
		points = append(points, point)
	}

	// Sort the points to put the critical alerts first in the description.
	sort.Slice(points, func(i, j int) bool {
		if points[i].Status != points[j].Status {
			return points[i].Status > points[j].Status
		}

		return labels.Compare(points[i].Labels, points[j].Labels) < 0
	})

	// Build the status description.
	var desc strings.Builder

	desc.WriteString("Current value: ")

	if len(points) > 1 {
		desc.WriteRune('\n')
	}

	for i, point := range points {
		if i > 0 {
			desc.WriteString(", \n")
		}

		// It's possible for a PromQL to generate no other label than the alertname (which was removed here).
		if len(point.Labels) > 0 {
			desc.WriteString(fmt.Sprintf("%s: ", formatLabels(point.Labels)))
		}

		desc.WriteString(fmt.Sprintf("%.2f", point.Value))
	}

	return desc.String()
}

// Deduplicate the alerts (an alert can appear both in the warning and the critical rule).
func (agr *alertRuleGroup) deduplicateAlerts() map[uint64]alertMinimal {
	var warningAlerts, criticalAlerts []*rules.Alert

	if agr.warningRule != nil {
		warningAlerts = append(warningAlerts, agr.warningRule.ActiveAlerts()...)
	}

	if agr.criticalRule != nil {
		criticalAlerts = append(warningAlerts, agr.criticalRule.ActiveAlerts()...)
	}

	alertsMap := make(map[uint64]alertMinimal)

	for _, alert := range warningAlerts {
		newAlert := alertMinimal{
			Labels: alertLabels(alert.Labels),
			Value:  alert.Value,
			Status: statusFromAlertState(alert.State, types.StatusWarning),
		}

		alertsMap[alert.Labels.Hash()] = newAlert
	}

	for _, alert := range criticalAlerts {
		newAlert := alertMinimal{
			Labels: alertLabels(alert.Labels),
			Value:  alert.Value,
			Status: statusFromAlertState(alert.State, types.StatusWarning),
		}

		alertKey := alert.Labels.Hash()
		if prevAlert, ok := alertsMap[alertKey]; (ok && newAlert.Status > prevAlert.Status) || !ok {
			alertsMap[alertKey] = newAlert
		}
	}

	return alertsMap
}

// formatLabels converts labels to a human readable string.
// {instance="localhost", severity="warning"} -> instance=localhost, severity=warning.
func formatLabels(ls labels.Labels) string {
	var s strings.Builder

	for i, l := range ls {
		if i > 0 {
			s.WriteString(", ")
		}

		s.WriteString(l.Name)
		s.WriteByte('=')
		s.WriteString(l.Value)
	}

	return s.String()
}

// alertLabels returns the labels that should appear in the status description for an alert.
func alertLabels(lbls labels.Labels) labels.Labels {
	builder := labels.NewBuilder(lbls)
	builder.Del(types.LabelName)
	builder.Del(types.LabelAlertname)
	builder.Del(types.LabelInstanceUUID)

	return builder.Labels()
}

// We send an OK status if the rule is inactive or pending, else a
// warning or critical status depending on the rule severity.
func statusFromAlertState(state rules.AlertState, severity types.Status) types.Status {
	status := types.StatusOk

	if state == rules.StateFiring {
		status = severity
	}

	return status
}

func (rm *Manager) addRule(
	promqlRule PromQLRule,
	resolution time.Duration,
	isError string,
) error {
	// TODO:
	// if lbls.Get(types.LabelInstanceUUID) != rule.InstanceUUID {
	// 	builder := labels.NewBuilder(lbls)
	// 	builder.Set(types.LabelInstanceUUID, rule.InstanceUUID)
	// 	lbls = builder.Labels()
	// }

	lbls := labels.FromMap(labelsMap(promqlRule))

	var (
		warningRule, criticalRule *rules.AlertingRule
		err                       error
	)

	if promqlRule.WarningQuery != "" {
		warningRule, err = newRule(
			promqlRule.WarningQuery,
			time.Duration(promqlRule.WarningDelaySecond)*time.Second,
			lbls,
			promqlRule.ID,
			rm.logger,
		)
		if err != nil {
			return err
		}
	}

	if promqlRule.CriticalQuery != "" {
		criticalRule, err = newRule(
			promqlRule.CriticalQuery,
			time.Duration(promqlRule.CriticalDelaySecond)*time.Second,
			lbls,
			promqlRule.ID,
			rm.logger,
		)
		if err != nil {
			return err
		}
	}

	newGroup := &alertRuleGroup{
		// instanceID:    rule.InstanceUUID,
		promqlRule:   promqlRule,
		isError:      isError,
		resolution:   resolution,
		warningRule:  warningRule,
		criticalRule: criticalRule,
	}

	rm.ruleGroups[promqlRule.ID] = newGroup

	return nil
}

// RebuildPromQLRules rebuild the PromQL rules list.
func (rm *Manager) RebuildPromQLRules(promqlRules []PromQLRule) error {
	rm.l.Lock()
	defer rm.l.Unlock()

	old := rm.ruleGroups
	rm.ruleGroups = make(map[string]*alertRuleGroup)

	for _, rule := range promqlRules {
		// Keep the previous group if it hasn't changed.
		// TODO : && prevInstance.instanceID == rule.InstanceUUID
		if prevInstance, ok := old[rule.ID]; ok {
			if prevRule := prevInstance.promqlRule; prevRule.WarningQuery == rule.WarningQuery &&
				prevRule.WarningDelaySecond == rule.WarningDelaySecond &&
				prevRule.CriticalQuery == rule.CriticalQuery &&
				prevRule.CriticalDelaySecond == rule.CriticalDelaySecond {
				rm.ruleGroups[rule.ID] = prevInstance
			}

			continue
		}

		err := rm.addRule(rule, rule.Resolution, "")
		if err != nil {
			_ = rm.addRule(rule, rule.Resolution, err.Error())
		}
	}

	return nil
}

func (rm *Manager) ResetInactiveRules() {
	now := time.Now().Truncate(time.Second)

	for _, val := range rm.ruleGroups {
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

	for _, r := range rm.ruleGroups {
		if r.inactiveSince.IsZero() {
			activeAlertingRules++
		}
	}

	fmt.Fprintf(file, "# Active Alerting rules (%d entries)\n", activeAlertingRules)

	for _, r := range rm.ruleGroups {
		if r.inactiveSince.IsZero() {
			fmt.Fprintf(file, "%s\n", r.String())
		}
	}

	fmt.Fprintf(file, "\n# Inactive Alerting Rules (%d entries)\n", len(rm.ruleGroups)-activeAlertingRules)

	for _, r := range rm.ruleGroups {
		if !r.inactiveSince.IsZero() {
			fmt.Fprintf(file, "%s\n", r.String())
		}
	}

	return nil
}

func newRule(
	exp string,
	holdDuration time.Duration,
	lbls labels.Labels,
	alertingRuleID string,
	logger log.Logger,
) (*rules.AlertingRule, error) {
	newExp, err := parser.ParseExpr(exp)
	if err != nil {
		return nil, err
	}

	newRule := rules.NewAlertingRule(
		"unused",
		newExp,
		holdDuration,
		lbls,
		nil,
		labels.Labels{},
		"",
		true,
		log.With(logger, "component", "alert_rule", "alerting_rule", alertingRuleID),
	)

	return newRule, nil
}

func (agr *alertRuleGroup) String() string {
	return fmt.Sprintf("%#v\n", agr)
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

func labelsMap(rule PromQLRule) map[string]string {
	return map[string]string{
		types.LabelName: rule.Name,
		// TODO: This shouldn't be hardcoded.
		types.LabelInstanceUUID: "fdfedf2a-9441-4b9d-8d6f-e82ce8a820d5",
	}
}
