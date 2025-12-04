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

package rules

import (
	"context"
	"fmt"
	"maps"
	"runtime"
	"sync"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/matcher"
	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/rules"
	"github.com/prometheus/prometheus/storage"
)

// Manager is a wrapper handling everything related to prometheus recording rules.
type Manager struct {
	recordingRules []*rules.Group
	matchers       []matcher.Matchers

	appendable *dynamicAppendable
	queryable  storage.Queryable
	engine     *promql.Engine

	l            sync.Mutex
	agentStarted time.Time
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

func NewManager(ctx context.Context, queryable storage.Queryable, baseRules map[string]string) *Manager {
	rules := defaultLinuxRecordingRules
	if runtime.GOOS == "windows" {
		rules = defaultWindowsRecordingRules
	}

	maps.Copy(rules, baseRules)

	return newManager(ctx, queryable, rules, time.Now())
}

func newManager(ctx context.Context, queryable storage.Queryable, defaultRules map[string]string, created time.Time) *Manager {
	engine := promql.NewEngine(promql.EngineOpts{
		Logger:             logger.NewSlog().With("component", "query engine"),
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
			Logger:     logger.NewSlog().With("component", "rules manager"),
			Appendable: app,
			Queryable:  queryable,
			QueryFunc:  rules.EngineQueryFunc(engine, queryable),
		},
	})

	matchers := make([]matcher.Matchers, 0, len(defaultGroupRules))
	for _, rule := range defaultGroupRules {
		matchers = append(matchers, matcher.MatchersFromQuery(rule.Query())...)
	}

	rm := Manager{
		appendable:     app,
		queryable:      queryable,
		engine:         engine,
		recordingRules: []*rules.Group{defaultGroup},
		matchers:       matchers,
		agentStarted:   created,
	}

	return &rm
}

// InputMetricMatchers returns a list of matchers for metrics used as input or recording rules.
// Those metrics should pass from input if you expect recording rule to work correctly.
func (rm *Manager) InputMetricMatchers() []matcher.Matchers {
	return rm.matchers
}

// MetricNames returns a list of all recording rules metric names.
// This is used for dynamic generation of metrics filter.
func (rm *Manager) MetricNames() []string {
	names := make([]string, 0)

	rm.l.Lock()
	defer rm.l.Unlock()

	for _, group := range rm.recordingRules {
		for _, rule := range group.Rules() {
			names = append(names, rule.Name())
		}
	}

	return names
}

func (rm *Manager) CollectWithState(ctx context.Context, state registry.GatherState, app storage.Appender) error {
	now := state.T0

	rm.appendable.SetAppendable(model.NewFromAppender(app))
	defer rm.appendable.SetAppendable(nil)

	rm.l.Lock()
	defer rm.l.Unlock()

	for _, rgr := range rm.recordingRules {
		rgr.Eval(ctx, now)
	}

	return nil
}

func (rm *Manager) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("recording-rules.txt")
	if err != nil {
		return err
	}

	rm.l.Lock()
	defer rm.l.Unlock()

	fmt.Fprintf(file, "# Recording rules (%d entries)\n", len(rm.recordingRules))

	for _, gr := range rm.recordingRules {
		fmt.Fprintf(file, "# group %s\n", gr.Name())

		for _, r := range gr.Rules() {
			fmt.Fprintln(file, r.String())
		}
	}

	return nil
}
