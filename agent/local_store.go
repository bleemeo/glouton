// Copyright 2015-2026 Bleemeo
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
	"path/filepath"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/store/tsdb"
	"github.com/bleemeo/glouton/types"
)

// setupLocalTSDB opens (once per process) the on-disk TSDB if the
// resolved local_store policy says so. The handle is cached in the
// reloadState so subsequent reloads reuse it without replaying the WAL.
//
// Resolution rule: an explicit agent.local_store.enable always wins;
// when unset, the store is enabled iff bleemeo.enable is false (i.e.
// when no SaaS backend will retain data, persist locally by default).
func (a *agent) setupLocalTSDB() {
	cfg := a.config.Agent.LocalStore

	enabled := !a.config.Bleemeo.Enable
	if cfg.Enable != nil {
		enabled = *cfg.Enable
	}

	if !enabled {
		return
	}

	if a.reloadState.LocalStore() != nil {
		return
	}

	path := cfg.Path
	if path == "" {
		path = filepath.Join(a.stateDir, "tsdb")
	}

	store, err := tsdb.Open(tsdb.Options{
		Path:      path,
		Retention: cfg.Retention,
	})
	if err != nil {
		logger.Printf("Local TSDB unavailable, continuing without on-disk metric persistence: %v", err)

		return
	}

	a.reloadState.SetLocalStore(store)

	logger.V(0).Printf("Local TSDB enabled at %s (retention %s)", path, cfg.Retention)
}

// teePointPusher forwards every PushPoints call to two underlying
// pushers. It is used to mirror the registry output to both the
// in-memory store (consumed by the Bleemeo connector) and the on-disk
// TSDB (consumed by the local API).
type teePointPusher struct {
	primary   types.PointPusher
	secondary types.PointPusher
}

func (t teePointPusher) PushPoints(ctx context.Context, points []types.MetricPoint) {
	t.primary.PushPoints(ctx, points)
	t.secondary.PushPoints(ctx, points)
}
