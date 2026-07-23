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

package logmetrics

import (
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"strings"
	"sync"
	"time"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	crTypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/prometheus/storage"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	noopM "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
)

// Package logmetrics counts log lines matching a regex and reports the rate as a
// metric, via filelogreceiver + countconnector (OTTL matching). Independent from
// otel/logprocessing: never ships log content, works without log shipping or
// Bleemeo enabled.

const updateInterval = time.Minute

// Manager tails log sources (static paths, and dynamically-resolved container log
// files) and counts lines matching configured regexes.
type Manager struct {
	cfg       config.Log
	hostroot  string
	runtime   crTypes.RuntimeInterface
	state     bleemeoTypes.State
	telemetry component.TelemetrySettings

	reg       *metricsRegistry
	sink      consumer.Metrics
	persister *persistHost // nil if persistence setup failed; sources then run without a StorageID

	l                 sync.Mutex
	staticSources     []*source
	containerSources  map[string]*source // map key: container ID
	watchedContainers map[string]string  // map key: container ID -> container name, for diagnostics
}

func New(cfg config.Log, hostroot string, runtime crTypes.RuntimeInterface, state bleemeoTypes.State) *Manager {
	reg := newMetricsRegistry()

	persister, err := newPersistHost(state)
	if err != nil {
		logger.V(1).Printf("logmetrics: persistence disabled, read offsets won't survive a restart: %v", err)
	}

	man := &Manager{
		cfg:      cfg,
		hostroot: hostroot,
		runtime:  runtime,
		state:    state,
		telemetry: component.TelemetrySettings{
			Logger:         logger.ZapLogger(),
			TracerProvider: noop.NewTracerProvider(),
			MeterProvider:  noopM.NewMeterProvider(),
			Resource:       pcommon.NewResource(),
		},
		reg:               reg,
		sink:              reg.metricsSink(),
		persister:         persister,
		containerSources:  make(map[string]*source),
		watchedContainers: make(map[string]string),
	}

	// Pre-register every declared metric name (legacy and new-style) so MetricNames() is complete immediately
	// without waiting for a dynamically-resolved source (container) to appear.
	man.reg.resolve(collectAllFilters(cfg))

	return man
}

// collectAllFilters gathers every LogFilter declared anywhere in the config.
func collectAllFilters(cfg config.Log) []config.LogFilter {
	var filters []config.LogFilter

	for _, input := range cfg.Inputs {
		filters = append(filters, input.Filters...)
	}

	for _, metricsRecv := range cfg.Metrics.Receivers {
		filters = append(filters, metricsRecv.Filters...)
	}

	for _, metricsKnownFilter := range cfg.Metrics.KnownFilters {
		filters = append(filters, metricsKnownFilter...)
	}

	return filters
}

// Run starts static sources once, then every updateInterval starts/stops container
// sources as containers appear/disappear (skipped if cfg has no container-based
// rule) and saves persisted read offsets to the state cache.
func (man *Manager) Run(ctx context.Context) error {
	defer crashreport.ProcessPanic()

	man.startStaticSources(ctx)

	watchContainers := hasContainerFilters(man.cfg)

	for ctx.Err() == nil {
		if watchContainers {
			man.updateContainerSources(ctx)
		}

		man.saveState()

		select {
		case <-time.After(updateInterval):
		case <-ctx.Done():
		}
	}

	man.stopAll(context.Background())
	man.saveState()

	return ctx.Err()
}

// saveState persists every source's read offset to the state cache, so a Glouton
// restart resumes tailing where it left off instead of skipping to the file's end.
func (man *Manager) saveState() {
	if man.persister != nil {
		man.persister.saveToState(man.state)
	}
}

func (man *Manager) startStaticSources(ctx context.Context) {
	man.l.Lock()
	defer man.l.Unlock()

	for _, input := range man.cfg.Inputs {
		if input.Path == "" {
			continue // container-based, handled dynamically, see updateContainerSources
		}

		man.startStaticSource(ctx, []string{filepath.Join(man.hostroot, input.Path)}, input.Filters)
	}

	for _, recv := range man.cfg.Metrics.Receivers {
		include := make([]string, len(recv.Include))

		for i, pattern := range recv.Include {
			include[i] = filepath.Join(man.hostroot, pattern)
		}

		man.startStaticSource(ctx, include, recv.Filters)
	}
}

func (man *Manager) startStaticSource(ctx context.Context, include []string, filters []config.LogFilter) {
	if len(filters) == 0 {
		return
	}

	// The joined include patterns are a stable identity across restarts.
	name := "path:" + strings.Join(include, ",")

	src, err := newSource(ctx, man.telemetry, include, false, filters, man.sink, man.persister, name)
	if err != nil {
		logger.V(1).Printf("logmetrics: failed to start source for %v: %v", include, err)

		return
	}

	man.staticSources = append(man.staticSources, src)
}

// updateContainerSources resolves the live container list and starts a source for
// every newly-matching container, stopping sources for containers that disappeared.
func (man *Manager) updateContainerSources(ctx context.Context) {
	containers, err := man.runtime.Containers(ctx, updateInterval, false)
	if err != nil {
		logger.V(1).Printf("logmetrics: failed to list containers: %v", err)

		return
	}

	man.l.Lock()
	defer man.l.Unlock()

	currentIDs := make(map[string]bool, len(containers))

	for _, ctr := range containers {
		currentIDs[ctr.ID()] = true

		if _, alreadyWatched := man.containerSources[ctr.ID()]; alreadyWatched {
			continue
		}

		filters := resolveContainerFilters(man.cfg, ctr)
		if len(filters) == 0 {
			continue
		}

		logPath := resolveContainerLogPath(ctr, man.hostroot)
		if logPath == "" {
			logger.V(1).Printf("logmetrics: no log file found for container %s (%s), logs won't be counted", ctr.ContainerName(), ctr.ID())

			continue
		}

		// The container ID is a stable identity across a Glouton restart as long as
		// the same container instance is still running (it changes if the container
		// itself is recreated, which is fine: that's effectively a new log source).
		name := "container:" + ctr.ID()

		src, err := newSource(ctx, man.telemetry, []string{logPath}, true, filters, man.sink, man.persister, name)
		if err != nil {
			logger.V(1).Printf("logmetrics: failed to start source for container %s (%s): %v", ctr.ContainerName(), ctr.ID(), err)

			continue
		}

		man.containerSources[ctr.ID()] = src
		man.watchedContainers[ctr.ID()] = ctr.ContainerName()
	}

	for id, src := range man.containerSources {
		if currentIDs[id] {
			continue
		}

		if err := src.stop(ctx); err != nil {
			logger.V(1).Printf("logmetrics: failed to stop source for container %s: %v", id, err)
		}

		delete(man.containerSources, id)
		delete(man.watchedContainers, id)
	}
}

func (man *Manager) stopAll(ctx context.Context) {
	man.l.Lock()
	defer man.l.Unlock()

	for _, src := range man.staticSources {
		if err := src.stop(ctx); err != nil {
			logger.V(1).Printf("logmetrics: failed to stop source: %v", err)
		}
	}

	for _, src := range man.containerSources {
		if err := src.stop(ctx); err != nil {
			logger.V(1).Printf("logmetrics: failed to stop source: %v", err)
		}
	}
}

// EmitMetrics implements registry.AppenderFunc, reporting the current "matches per
// second" rate of every configured log-to-metric filter.
func (man *Manager) EmitMetrics(_ context.Context, _ registry.GatherState, app storage.Appender) error {
	return man.reg.emit(app)
}

// MetricNames returns the name of every configured log-to-metric filter, so it can
// be fed into the metric allow-list (see agent.rebuildDynamicMetricAllowDenyList).
func (man *Manager) MetricNames() []string {
	return man.reg.metricNames()
}

func (man *Manager) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("log-to-metrics.json")
	if err != nil {
		return err
	}

	man.l.Lock()
	info := struct {
		MetricNames       []string
		WatchedContainers map[string]string
		StaticSourceCount int
	}{
		MetricNames:       man.reg.metricNames(),
		WatchedContainers: maps.Clone(man.watchedContainers),
		StaticSourceCount: len(man.staticSources),
	}
	man.l.Unlock()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	if err := enc.Encode(info); err != nil {
		return err
	}

	if man.persister != nil {
		return man.persister.writeToArchive(archive)
	}

	return nil
}
