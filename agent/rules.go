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

package agent

import (
	"context"
	"fmt"
	"glouton/logger"
	"glouton/store"
	"os"
	"sync"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/rules"
)

type rulesManager struct {
	queryable  *store.Store
	appendable *store.Store

	recordingRules []*rules.Group

	//alertingRules ]*rules.AlertingRule | Rule?

	l   sync.Mutex
	mgr *rules.Manager
}

func (a *agent) recordingRules(ctx context.Context) error {
	promLogger := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	m := rulesManager{}
	m.queryable = a.store
	m.appendable = a.store
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
		Appendable: m.appendable,
		Queryable:  m.queryable,
		QueryFunc:  rules.EngineQueryFunc(engine, m.queryable),
		NotifyFunc: func(ctx context.Context, expr string, alerts ...*rules.Alert) {
			if len(alerts) == 0 {
				return
			}
			promLogger.Log("component", "alert", "expr", expr, "alerts", fmt.Sprintf("%v", alerts), "alert0.State", alerts[0].State, "alert0.Labels", alerts[0].Labels.Copy())
		},
	}

	exp, err := parser.ParseExpr("sum(node_cpu_seconds_total) without (cpu)")
	if err != nil {
		logger.V(0).Printf("An error occurred while parsing expression: %v", err)
	}

	rule := rules.NewRecordingRule("node_cpu_seconds_global", exp, labels.Labels{})
	group := rules.NewGroup(rules.GroupOptions{
		Name:          "default",
		Interval:      a.metricResolution,
		Rules:         []rules.Rule{rule},
		ShouldRestore: true,
		Opts:          mgrOptions,
	})

	m.recordingRules = append(m.recordingRules, group)

	m.mgr = rules.NewManager(mgrOptions)

	go func() {
		for ctx.Err() == nil {
			time.Sleep(a.metricResolution)
			for _, gr := range m.recordingRules {
				gr.Eval(ctx, time.Now())
			}
		}
	}()

	return nil
}
