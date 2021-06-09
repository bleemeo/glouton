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
)

// promAlertTime represents the duration for which the alerting rule
// should exceed the threshold to be considered fired.
const promAlertTime = 5 * time.Minute

//Manager is a wrapper handling everything related to prometheus recording
// and alerting rules.
type Manager struct {
	// store implements both appendable and queryable.
	store          *store.Store
	recordingRules []*rules.Group
	alertingRules  map[string]*rules.AlertingRule

	engine *promql.Engine
	logger log.Logger

	l            sync.Mutex
	agentStarted time.Time
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

func NewManager(ctx context.Context, store *store.Store) *Manager {
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
			logger.V(0).Printf("An error occurred while parsing expression %s: %v. This rule was not registered", val, err)
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
		alertingRules:  make(map[string]*rules.AlertingRule),
		engine:         engine,
		logger:         promLogger,
		agentStarted:   time.Now(), //FIXME: change this time.now with the real agent created time
	}

	return &rm
}

func (rm *Manager) Run() {
	ctx := context.Background()
	now := time.Now()
	warningPoints := make(map[string]types.MetricPoint)
	criticalPoints := make(map[string]types.MetricPoint)
	okPoints := make(map[string]types.MetricPoint)

	rm.l.Lock()

	for _, rgr := range rm.recordingRules {
		rgr.Eval(ctx, now)
	}

	for _, agr := range rm.alertingRules {
		prevState := agr.State()

		queryable := &store.CountingQueryable{Queryable: rm.store}
		_, err := agr.Eval(ctx, now, rules.EngineQueryFunc(rm.engine, queryable), nil)

		if err != nil {
			logger.V(2).Printf("an error occurred while evaluating an alerting rule: %w", err)
			continue
		}

		if queryable.Count() == 0 {
			continue
		}

		state := agr.State()

		statusCode := types.StatusFromString(agr.Labels().Get("severity"))
		status := types.StatusDescription{
			CurrentStatus:     statusCode,
			StatusDescription: "",
		}

		logger.V(2).Printf("metric state for %s previous state=%v, new state=%v", agr.Name(), prevState, state)

		metricName := agr.Labels().Get(types.LabelName)
		newPoint := types.MetricPoint{
			Point: types.Point{
				Time:  now,
				Value: float64(statusCode.NagiosCode()),
			},
			Annotations: types.MetricAnnotations{
				Status: status,
			},
		}

		if state != rules.StateFiring {
			if time.Since(rm.agentStarted) < promAlertTime {
				continue
			}

			newPoint.Value = float64(types.StatusFromString("ok"))
		}

		switch statusCode {
		case types.StatusCritical:
			delete(warningPoints, metricName)

			criticalPoints[metricName] = newPoint
		case types.StatusWarning:
			warningPoints[metricName] = newPoint
		case types.StatusOk:
			okPoints[metricName] = newPoint
		default:
			logger.V(2).Printf("An error occurred while creating new alerting points: unknown state for metric %s", metricName)
		}
	}

	rm.l.Unlock()

	res := []types.MetricPoint{}

	for _, val := range okPoints {
		res = append(res, val)
	}

	for _, val := range warningPoints {
		res = append(res, val)
	}

	for _, val := range criticalPoints {
		res = append(res, val)
	}

	if len(res) != 0 {
		rm.store.PushPoints(res)
	}
}

func (rm *Manager) newRule(exp string, metricName string, threshold string, severity string) error {
	newExp, err := parser.ParseExpr(exp)
	if err != nil {
		return err
	}

	newRule := rules.NewAlertingRule(metricName+threshold,
		newExp, promAlertTime, labels.Labels{labels.Label{Name: types.LabelName, Value: metricName}}, labels.Labels{labels.Label{Name: "severity", Value: severity}},
		labels.Labels{}, false, log.With(rm.logger, "alerting_rule", metricName+threshold))

	rm.alertingRules[metricName+threshold] = newRule

	return nil
}

func (rm *Manager) addAlertingRule(metric bleemeoTypes.Metric) error {
	if !math.IsNaN(*metric.Threshold.LowWarning) {
		err := rm.newRule(metric.PromQLQuery+fmt.Sprintf("< %f", *metric.Threshold.LowWarning), metric.Labels[types.LabelName], metric.LabelsText+"_low_warning", "warning")
		if err != nil {
			return err
		}
	}

	if !math.IsNaN(*metric.Threshold.HighWarning) {
		err := rm.newRule(metric.PromQLQuery+fmt.Sprintf("> %f", *metric.Threshold.HighWarning), metric.Labels[types.LabelName], metric.LabelsText+"_high_warning", "warning")
		if err != nil {
			return err
		}
	}

	if !math.IsNaN(*metric.Threshold.LowCritical) {
		err := rm.newRule(metric.PromQLQuery+fmt.Sprintf("< %f", *metric.Threshold.LowCritical), metric.Labels[types.LabelName], metric.LabelsText+"_low_critical", "critical")
		if err != nil {
			return err
		}
	}

	if !math.IsNaN(*metric.Threshold.HighCritical) {
		err := rm.newRule(metric.PromQLQuery+fmt.Sprintf("> %f", *metric.Threshold.HighCritical), metric.Labels[types.LabelName], metric.LabelsText+"_high_critical", "critical")
		if err != nil {
			return err
		}
	}

	return nil
}

//RebuildAlertingRules rebuild the alerting rules list from a bleemeo api metric list.
func (rm *Manager) RebuildAlertingRules(metricsList []bleemeoTypes.Metric) error {
	rm.alertingRules = make(map[string]*rules.AlertingRule)

	rm.l.Lock()
	defer rm.l.Unlock()

	for _, val := range metricsList {
		if len(val.PromQLQuery) == 0 {
			continue
		}

		err := rm.addAlertingRule(val)
		if err != nil {
			return err
		}
	}

	return nil
}
