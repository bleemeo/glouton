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
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"glouton/logger"
	"glouton/store"
	"glouton/types"
	"math"
	"os"
	"runtime"
	"sync"
	"time"

	bleemeoTypes "glouton/bleemeo/types"

	"github.com/go-kit/kit/log"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/rules"
	"github.com/prometheus/prometheus/storage"
)

// promAlertTime represents the duration for which the alerting rule
// should exceed the threshold to be considered fired.
const promAlertTime = 5 * time.Minute

var errUnknownState = errors.New("unknown state for metric")

//Manager is a wrapper handling everything related to prometheus recording
// and alerting rules.
type Manager struct {
	// store implements both appendable and queryable.
	store          *store.Store
	recordingRules []*rules.Group
	alertingRules  []*ruleGroup

	engine *promql.Engine
	logger log.Logger

	l            sync.Mutex
	agentStarted time.Time
}

type ruleGroup struct {
	rules         map[string]*rules.AlertingRule
	inactiveSince time.Time
	disabledUntil time.Time

	id          string
	promql      string
	isUserAlert bool
}

//nolint: gochecknoglobals
var (
	defaultLinuxRecordingRules = map[string]string{
		"node_cpu_seconds_global": "sum(node_cpu_seconds_total) without (cpu)",
	}
	defaultWindowsRecordingRules = map[string]string{
		"windows_cpu_time_global":            "sum(windows_cpu_time_total) without(core)",
		"windows_memory_standby_cache_bytes": "windows_memory_standby_cache_core_bytes+windows_memory_standby_cache_normal_priority_bytes+windows_memory_standby_cache_reserve_bytes",
	}
)

func NewManager(ctx context.Context, store *store.Store, created time.Time) *Manager {
	promLogger := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	engine := promql.NewEngine(promql.EngineOpts{
		Logger:             log.With(promLogger, "component", "query engine"),
		Reg:                nil,
		MaxSamples:         50000000,
		Timeout:            2 * time.Minute,
		ActiveQueryTracker: nil,
		LookbackDelta:      5 * time.Minute,
	})

	mgrOptions := &rules.ManagerOptions{
		Context:    ctx,
		Logger:     log.With(promLogger, "component", "rules manager"),
		Appendable: store,
		Queryable:  store,
		QueryFunc:  rules.EngineQueryFunc(engine, store),
		NotifyFunc: func(ctx context.Context, expr string, alerts ...*rules.Alert) {
			if len(alerts) == 0 {
				return
			}

			logger.V(2).Printf("notification triggered for expression %x with state %v and labels %v", expr, alerts[0].State, alerts[0].Labels)
		},
	}

	defaultGroupRules := []rules.Rule{}

	defaultRules := defaultLinuxRecordingRules
	if runtime.GOOS == "windows" {
		defaultRules = defaultWindowsRecordingRules
	}

	for metricName, val := range defaultRules {
		exp, err := parser.ParseExpr(val)
		if err != nil {
			logger.V(2).Printf("An error occurred while parsing expression %s: %v. This rule was not registered", val, err)
		} else {
			newRule := rules.NewRecordingRule(metricName, exp, labels.Labels{})
			defaultGroupRules = append(defaultGroupRules, newRule)
		}
	}

	defaultGroup := rules.NewGroup(rules.GroupOptions{
		Name:          "default",
		Rules:         defaultGroupRules,
		ShouldRestore: true,
		Opts:          mgrOptions,
	})

	rm := Manager{
		store:          store,
		recordingRules: []*rules.Group{defaultGroup},
		alertingRules:  nil,
		engine:         engine,
		logger:         promLogger,
		agentStarted:   created,
	}

	return &rm
}

func (rm *Manager) Run(ctx context.Context, now time.Time) {
	res := []types.MetricPoint{}

	rm.l.Lock()

	for _, rgr := range rm.recordingRules {
		rgr.Eval(ctx, now)
	}

	for _, agr := range rm.alertingRules {
		point, err := agr.runGroup(ctx, now, rm)
		if err != nil {
			logger.V(2).Printf("An error occurred while trying to execute rules: %w")
		} else if point != nil {
			res = append(res, *point)
		}
	}

	rm.l.Unlock()

	if len(res) != 0 {
		logger.V(2).Printf("Sending %d new alert to the api", len(res))
		rm.store.PushPoints(res)
	}
}

func (agr *ruleGroup) shouldSkip(now time.Time) bool {
	if !agr.inactiveSince.IsZero() && !agr.isUserAlert {
		if agr.disabledUntil.IsZero() && now.After(agr.inactiveSince.Add(2*time.Minute)) {
			logger.V(2).Printf("rule %s has been disabled for the last 2 minutes. retrying this metric in 10 minutes", agr.id)
			agr.disabledUntil = now.Add(10 * time.Minute)
		}

		if now.After(agr.disabledUntil) {
			logger.V(2).Printf("Inactive rule %s will be re executed. Time since inactive: %s", agr.id, agr.inactiveSince.Format(time.RFC3339))
			agr.disabledUntil = now.Add(10 * time.Minute)
		} else {
			return true
		}
	}

	return false
}

func (agr *ruleGroup) runGroup(ctx context.Context, now time.Time, rm *Manager) (*types.MetricPoint, error) {
	thresholdOrder := []string{"high_critical", "low_critical", "high_warning", "low_warning"}

	var generatedPoint *types.MetricPoint = nil

	if agr.shouldSkip(now) {
		return nil, nil
	}

	for _, val := range thresholdOrder {
		rule := agr.rules[val]

		if rule == nil {
			continue
		}

		prevState := rule.State()

		queryable := &store.CountingQueryable{Queryable: rm.store}
		_, err := rule.Eval(ctx, now, rules.EngineQueryFunc(rm.engine, queryable), nil)

		if err != nil {
			return nil, err
		}

		state := rule.State()

		if queryable.Count() == 0 {
			if agr.isUserAlert {
				return &types.MetricPoint{
					Point: types.Point{
						Time:  now,
						Value: math.NaN(),
					},
				}, nil
			}

			if agr.inactiveSince.IsZero() {
				agr.inactiveSince = now
			}

			return nil, nil
		}

		agr.inactiveSince = time.Time{}
		agr.disabledUntil = time.Time{}

		if time.Since(rm.agentStarted) < promAlertTime {
			return nil, nil
		}

		logger.V(2).Printf("metric state for %s previous state=%v, new state=%v", rule.Name(), prevState, state)

		newPoint, err := generateNewPoint(val, rule, state, now)
		if err != nil {
			return nil, err
		}

		if state == rules.StateFiring {
			return newPoint, nil
		} else if generatedPoint == nil {
			generatedPoint = newPoint
		}
	}

	return generatedPoint, nil
}

func generateNewPoint(threshold string, rule storage.Labels, state rules.AlertState, now time.Time) (*types.MetricPoint, error) {
	statusCode := statusFromThreshold(threshold)
	status := types.StatusDescription{
		CurrentStatus:     statusCode,
		StatusDescription: "",
	}

	if statusCode == types.StatusUnknown {
		return nil, fmt.Errorf("%w for metric %s", errUnknownState, rule.Labels().String())
	}

	newPoint := types.MetricPoint{
		Point: types.Point{
			Time:  now,
			Value: float64(statusCode.NagiosCode()),
		},
		Annotations: types.MetricAnnotations{
			Status: status,
		},
	}

	if state == rules.StatePending || state == rules.StateInactive {
		statusCode = statusFromThreshold("ok")
		newPoint.Value = float64(statusCode.NagiosCode())
		newPoint.Annotations.Status.CurrentStatus = statusCode
	}

	return &newPoint, nil
}

func (rm *Manager) addAlertingRule(metric bleemeoTypes.Metric) error {
	newGroup := &ruleGroup{
		rules:         make(map[string]*rules.AlertingRule, 4),
		inactiveSince: time.Time{},
		disabledUntil: time.Time{},
		id:            metric.LabelsText,
		promql:        metric.PromQLQuery,
		isUserAlert:   metric.IsUserPromQLAlert,
	}

	if metric.Threshold.LowWarning != nil {
		err := newGroup.newRule(fmt.Sprintf("(%s) < %f", metric.PromQLQuery, *metric.Threshold.LowWarning), metric.Labels[types.LabelName], "low_warning", "warning", rm.logger)
		if err != nil {
			return err
		}
	}

	if metric.Threshold.HighWarning != nil {
		err := newGroup.newRule(fmt.Sprintf("(%s) > %f", metric.PromQLQuery, *metric.Threshold.HighWarning), metric.Labels[types.LabelName], "high_warning", "warning", rm.logger)
		if err != nil {
			return err
		}
	}

	if metric.Threshold.LowCritical != nil {
		err := newGroup.newRule(fmt.Sprintf("(%s) < %f", metric.PromQLQuery, *metric.Threshold.LowCritical), metric.Labels[types.LabelName], "low_critical", "critical", rm.logger)
		if err != nil {
			return err
		}
	}

	if metric.Threshold.HighCritical != nil {
		err := newGroup.newRule(fmt.Sprintf("(%s) > %f", metric.PromQLQuery, *metric.Threshold.HighCritical), metric.Labels[types.LabelName], "high_critical", "critical", rm.logger)
		if err != nil {
			return err
		}
	}

	rm.alertingRules = append(rm.alertingRules, newGroup)

	return nil
}

//RebuildAlertingRules rebuild the alerting rules list from a bleemeo api metric list.
func (rm *Manager) RebuildAlertingRules(metricsList []bleemeoTypes.Metric) error {
	rm.l.Lock()
	defer rm.l.Unlock()

	old := rm.alertingRules

	rm.alertingRules = []*ruleGroup{}

	for _, val := range metricsList {
		if len(val.PromQLQuery) == 0 || val.ToInternalThreshold().IsZero() {
			continue
		}

		err := rm.addAlertingRule(val)
		if err != nil {
			return err
		}
	}

	for _, val := range old {
		for _, newVal := range rm.alertingRules {
			if newVal.id == val.id {
				newVal.inactiveSince = val.inactiveSince
				newVal.disabledUntil = val.disabledUntil
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

		// The bleemeo connector uses 15 seconds as a synchronization time,
		// thus we add a bit a leeway in the disabled countdown.
		if now.Add(17 * time.Second).Before(val.disabledUntil) {
			val.disabledUntil = now.Add(17 * time.Second)
		}
	}
}

func (rm *Manager) DiagnosticZip(zipFile *zip.Writer) error {
	file, err := zipFile.Create("alertings-recording-rules.txt")
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

func (agr *ruleGroup) newRule(exp string, metricName string, threshold string, severity string, logger log.Logger) error {
	newExp, err := parser.ParseExpr(exp)
	if err != nil {
		return err
	}

	newRule := rules.NewAlertingRule(metricName+"_"+threshold,
		newExp, promAlertTime, nil, labels.Labels{labels.Label{Name: "severity", Value: severity}},
		labels.Labels{}, true, log.With(logger, "alerting_rule", metricName+"_"+threshold))

	agr.rules[threshold] = newRule

	return nil
}

func (agr *ruleGroup) String() string {
	return fmt.Sprintf("id=%s query=%s inactive_since=%v disabled_until=%v is_user_promql_alert=%v Threshold_low_Warning=%s Threshold_high_Warning=%s Threshold_low_Critical=%s Threshold_high_Critical=%s",
		agr.id, agr.promql, agr.inactiveSince, agr.disabledUntil, agr.isUserAlert, agr.rules["low_warning"], agr.rules["high_warning"], agr.rules["low_critical"], agr.rules["high_critical"])
}

func statusFromThreshold(s string) types.Status {
	switch s {
	case "ok":
		return types.StatusOk
	case "low_warning":
	case "high_warning":
		return types.StatusWarning
	case "low_critical":
	case "high_critical":
		return types.StatusCritical
	default:
		return types.StatusUnknown
	}

	return types.StatusUnknown
}
