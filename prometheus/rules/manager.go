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
	"time"

	"github.com/go-kit/kit/log"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/rules"
)

//Manager is a wrapper handling everything related to prometheus recording
// and alerting rules.
type Manager struct {
	// store implements both appendable and queryable.
	store          *store.Store
	recordingRules []*rules.Group
	alertingRules  []*rules.Group
	interval       time.Duration

	mgr *rules.Manager
}

//nolint: gochecknoglobals
var defaultRecordingRules = map[string]string{
	"node_cpu_seconds_global": "sum(node_cpu_seconds_total) without (cpu)",
}

func NewManager(ctx context.Context, interval time.Duration, store *store.Store) *Manager {
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

			err := promLogger.Log("component", "alert", "expr", expr, "alerts", fmt.Sprintf("%v", alerts), "alert0.State", alerts[0].State, "alert0.Labels", alerts[0].Labels.Copy())
			if err != nil {
				logger.V(2).Printf("an error occurred while running a prometheus notify: %w", err)
			}
		},
	}

	defaultGroupRules := []rules.Rule{}

	for metricName, val := range defaultRecordingRules {
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
		Interval:      interval,
		Rules:         defaultGroupRules,
		ShouldRestore: true,
		Opts:          mgrOptions,
	})

	rm := Manager{
		store:    store,
		interval: interval,
	}

	rm.recordingRules = append(rm.recordingRules, defaultGroup)

	rm.mgr = rules.NewManager(mgrOptions)

	return &rm
}

func (rm *Manager) Run() {
	ctx := context.Background()
	now := time.Now()

	for _, rgr := range rm.recordingRules {
		rgr.Eval(ctx, now)
	}

	for _, agr := range rm.alertingRules {
		agr.Eval(ctx, now)
	}
}
