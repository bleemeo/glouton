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

// minResetTime is the minimum time used by the inactive checks.
// The bleemeo connector uses 15 seconds as a synchronization time,
// thus we add a bit a leeway in the disabled countdown.
const minResetTime = 17 * time.Second

var (
	errSkipPoints = errors.New("skip points")
	errNilRules   = errors.New("both the warning and the critical rule are nil")
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

	l            sync.Mutex
	now          func() time.Time
	agentStarted time.Time
}

// PromQLRule is a rule that runs on a single agent.
type PromQLRule struct {
	ID            string
	Name          string
	WarningQuery  string
	WarningDelay  time.Duration
	CriticalQuery string
	CriticalDelay time.Duration
	InstanceID    string
	Resolution    time.Duration
}

func (rule PromQLRule) Equal(other PromQLRule) bool {
	return rule.WarningQuery == other.WarningQuery &&
		rule.WarningDelay == other.WarningDelay &&
		rule.CriticalQuery == other.CriticalQuery &&
		rule.CriticalDelay == other.CriticalDelay &&
		rule.InstanceID == other.InstanceID
}

type alertRuleGroup struct {
	warningRule  *rules.AlertingRule
	criticalRule *rules.AlertingRule

	disabledUntil time.Time
	pointsRead    int32
	lastRun       time.Time
	lastErr       error
	lastStatus    types.StatusDescription

	promqlRule PromQLRule
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

func NewManager(ctx context.Context, queryable storage.Queryable) *Manager {
	defaultRules := defaultLinuxRecordingRules
	if runtime.GOOS == "windows" {
		defaultRules = defaultWindowsRecordingRules
	}

	return newManager(ctx, queryable, defaultRules, time.Now())
}

func newManager(ctx context.Context, queryable storage.Queryable, defaultRules map[string]string, created time.Time) *Manager {
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
		appendable:     app,
		queryable:      queryable,
		engine:         engine,
		recordingRules: []*rules.Group{defaultGroup},
		ruleGroups:     map[string]*alertRuleGroup{},
		logger:         promLogger,
		agentStarted:   created,
		now:            time.Now,
	}

	return &rm
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
	if now.Before(agr.disabledUntil) {
		return true
	}

	if agr.promqlRule.Resolution != 0 && now.Add(time.Second).Sub(agr.lastRun) < agr.promqlRule.Resolution {
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
	agr.disabledUntil = time.Time{}

	for _, rule := range []*rules.AlertingRule{agr.warningRule, agr.criticalRule} {
		if rule == nil {
			continue
		}

		tmp, err := newFilterQueryable(
			rm.queryable,
			map[string]string{
				types.LabelInstanceUUID: agr.promqlRule.InstanceID,
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
		// Some metrics can a take bit of time before first points creation,
		// we don't want them to be labeled as unknown on start.
		if now.Sub(rm.agentStarted) < 2*time.Minute {
			return types.MetricPoint{}, errSkipPoints
		}

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

	var warningState, criticalState rules.AlertState

	if agr.warningRule != nil {
		warningState = agr.warningRule.State()
	}

	if agr.criticalRule != nil {
		criticalState = agr.criticalRule.State()
	}

	newPoint, err := agr.generateNewPoint(warningState, criticalState, now)
	if err != nil {
		return types.MetricPoint{}, err
	}

	agr.lastStatus = newPoint.Annotations.Status

	// Don't send any point at the start of the agent, as we do not provide a way for prometheus
	// to know previous values before the start. This avoids sending an OK point if one of the
	// rules is pending when the agent starts.
	maxDelay := agr.promqlRule.WarningDelay
	if agr.promqlRule.CriticalDelay > maxDelay {
		maxDelay = agr.promqlRule.CriticalDelay
	}

	// We remove 20 seconds to the start time to compensate the delta between agent startup and first metrics.
	startedFor := now.Sub(rm.agentStarted) - 20*time.Second
	if startedFor < maxDelay && (warningState != rules.StateInactive || criticalState != rules.StateInactive) {
		return types.MetricPoint{}, fmt.Errorf("%w: status ok with a pending rule", errSkipPoints)
	}

	return newPoint, nil
}

func (agr *alertRuleGroup) generateNewPoint(
	warningState, criticalState rules.AlertState,
	now time.Time,
) (types.MetricPoint, error) {
	warningStatus := statusFromAlertState(warningState, types.StatusWarning)
	criticalStatus := statusFromAlertState(criticalState, types.StatusCritical)

	var (
		lbls   labels.Labels
		status types.Status
	)

	if agr.criticalRule != nil && criticalStatus >= warningStatus {
		lbls = agr.criticalRule.Labels()
		status = criticalStatus
	} else if agr.warningRule != nil {
		lbls = agr.warningRule.Labels()
		status = warningStatus
	} else {
		return types.MetricPoint{}, errNilRules
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
				StatusDescription: agr.ruleDescription(status),
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
func (agr *alertRuleGroup) ruleDescription(ruleStatus types.Status) string {
	if ruleStatus == types.StatusOk {
		return "Everything is running fine"
	}

	alertsMap := agr.deduplicateAlerts()
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
		criticalAlerts = append(criticalAlerts, agr.criticalRule.ActiveAlerts()...)
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
	isError string,
) error {
	lbls := labels.FromMap(labelsMap(promqlRule))

	var (
		warningRule, criticalRule *rules.AlertingRule
		err                       error
	)

	if promqlRule.WarningQuery != "" {
		warningRule, err = newRule(
			promqlRule.WarningQuery,
			promqlRule.WarningDelay,
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
			promqlRule.CriticalDelay,
			lbls,
			promqlRule.ID,
			rm.logger,
		)
		if err != nil {
			return err
		}
	}

	newGroup := &alertRuleGroup{
		promqlRule:   promqlRule,
		isError:      isError,
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
		if rule.CriticalQuery == "" && rule.WarningQuery == "" {
			continue
		}

		// Keep the previous group if it hasn't changed.
		if prevInstance, ok := old[rule.ID]; ok && prevInstance.promqlRule.Equal(rule) {
			rm.ruleGroups[rule.ID] = prevInstance

			continue
		}

		err := rm.addRule(rule, "")
		if err != nil {
			_ = rm.addRule(rule, err.Error())
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

	fmt.Fprintf(file, "# Active Alerting rules (%d entries)\n", len(rm.ruleGroups))

	for _, r := range rm.ruleGroups {
		fmt.Fprintf(file, "%s\n", r.String())
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
	str := fmt.Sprintf(`# %s (%s)
	Warning: %s (delay %s) / Critical: %s (delay %s): %s
	Runs on agent %s with a resolution of %s.
	Last run at %s, read %d points.
	Disabled until %s.
	Last error: %v (isError=%s)
	`, agr.promqlRule.Name, agr.promqlRule.ID,
		agr.promqlRule.WarningQuery, agr.promqlRule.WarningDelay,
		agr.promqlRule.CriticalQuery, agr.promqlRule.CriticalDelay, agr.lastStatus,
		agr.promqlRule.InstanceID, agr.promqlRule.Resolution,
		agr.lastRun, agr.pointsRead,
		agr.disabledUntil,
		agr.lastErr, agr.isError,
	)

	return str
}

func labelsMap(rule PromQLRule) map[string]string {
	return map[string]string{
		types.LabelName:         rule.Name,
		types.LabelInstanceUUID: rule.InstanceID,
	}
}
