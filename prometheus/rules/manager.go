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
	"os"
	"runtime"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/rules"
)

// Manager is a wrapper handling everything related to prometheus recording
// and alerting rules.
type Manager struct {
	// store implements both appendable and queryable.
	store          *store.Store
	recordingRules []*rules.Group
	alertingRules  map[string]*rules.AlertingRule
	inactive       map[string]bool

	engine *promql.Engine
	logger log.Logger
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
		inactive:       make(map[string]bool),
		engine:         engine,
		logger:         promLogger,
	}

	return &rm
}

func (rm *Manager) Run() {
	ctx := context.Background()
	now := time.Now()

	for _, rgr := range rm.recordingRules {
		rgr.Eval(ctx, now)
	}

	for i, agr := range rm.alertingRules {
		prevState := agr.State()

		queryable := &store.CountingQueryable{Queryable: rm.store}
		_, err := agr.Eval(ctx, now, rules.EngineQueryFunc(rm.engine, queryable), nil)

		if err != nil {
			logger.V(2).Printf("an error occurred while evaluating an alerting rule: %w", err)
		}
		inactive := queryable.Count() == 0
		state := agr.State()

		rm.inactive[i] = inactive

		if inactive {
			continue
		}

		if state != prevState {
			logger.V(0).Printf("metric state for %s changed: previous state=%v, new state=%v", agr.Name(), prevState, state)
		}
	}
}

//AddAlertingRule adds a new alerting rule.
func (rm *Manager) AddAlertingRule(name string, exp string, hold time.Duration, lowWarning int64, highWarning int64, lowCritical int64, highCritical int64) error {
	severityList := []string{"warning", "warning", "critical", "critical"}
	thresholdValues := []string{fmt.Sprintf("%d", lowWarning), fmt.Sprintf("%d", highWarning), fmt.Sprintf("%d", lowCritical), fmt.Sprintf("%d", highCritical)}
	expList := []string{
		exp + " > " + thresholdValues[0] + " < " + thresholdValues[1],
		exp + " > " + thresholdValues[1] + " < " + thresholdValues[2],
		exp + " > " + thresholdValues[2] + " < " + thresholdValues[3],
		exp + " > " + thresholdValues[3]}

	for i, val := range expList {
		newExp, err := parser.ParseExpr(val)
		if err != nil {
			return err
		}
		newRule := rules.NewAlertingRule(name,
			newExp, hold, labels.Labels{labels.Label{Name: "severity", Value: severityList[i]}}, labels.Labels{}, labels.Labels{}, false, log.With(rm.logger, "alerting_rule", name))
		rm.alertingRules[name+thresholdValues[i]] = newRule

		rm.inactive[name] = true
	}

	return nil
}

//DeleteAlertingRule deletes a an alerting rule set.
func (rm *Manager) DeleteAlertingRule(name string) {
	delete(rm.alertingRules, name)
	delete(rm.inactive, name)
}

//UpdateAlertingRule updates an alerting rule set.
func (rm *Manager) UpdateAlertingRule(name string, exp string, hold time.Duration, lowWarning int64, highWarning int64, lowCritical int64, highCritical int64) error {
	rm.DeleteAlertingRule(name)
	return rm.AddAlertingRule(name, exp, hold, lowWarning, highWarning, lowCritical, highCritical)
}
