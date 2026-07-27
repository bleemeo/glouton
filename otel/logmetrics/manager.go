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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	crTypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/logsource"
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

const (
	persistStorageType = "glouton_log_metrics_storage"
	persistCacheKey    = "LogMetricsFileMetadata"
	persistArchivePath = "log-to-metrics/persister.json"
)

// Manager tails log sources (static paths, and dynamically-resolved container log
// files) and counts lines matching configured regexes.
type Manager struct {
	cfg       config.Log
	hostroot  string
	runtime   crTypes.RuntimeInterface
	state     bleemeoTypes.State
	telemetry component.TelemetrySettings

	reg *metricsRegistry
	// sink is the item="" sink, shared by every non-container source (static, network).
	sink          consumer.Metrics
	persister     *logsource.PersistHost // nil if persistence setup failed; sources then run without a StorageID
	commandRunner logsource.CommandRunner

	l                 sync.Mutex
	staticSources     []*source
	pendingStatic     []pendingStaticSource // static sources that found no log file yet, retried every updateInterval
	containerSources  map[string]*source    // map key: container ID
	watchedContainers map[string]string     // map key: container ID -> container name, for diagnostics
	networkSource     *networkSource        // nil if disabled or unconfigured
}

// pendingStaticSource is a static source spec that failed to resolve to any
// log file at the time it was attempted (e.g. the file doesn't exist yet).
type pendingStaticSource struct {
	include  []string
	counters []config.LogCounter
}

func New(cfg config.Log, hostroot string, runtime crTypes.RuntimeInterface, state bleemeoTypes.State, commandRunner logsource.CommandRunner) *Manager {
	reg := newMetricsRegistry()

	persister, err := logsource.NewPersistHost(state, logsource.PersistConfig{
		StorageType: persistStorageType,
		CacheKey:    persistCacheKey,
		ArchivePath: persistArchivePath,
		// Unlike otel/logprocessing, keep every receiver's metadata on every save
		// (not just the ones touched this run): a log-to-metric source that's
		// simply idle (no matching line since restart) must not lose its offset.
		FullSnapshot: true,
	})
	if err != nil {
		logger.V(1).Printf("logmetrics: persistence disabled, read offsets won't survive a restart: %v", err)
	}

	man := &Manager{
		cfg:           cfg,
		hostroot:      hostroot,
		runtime:       runtime,
		state:         state,
		commandRunner: commandRunner,
		telemetry: component.TelemetrySettings{
			Logger:         logger.ZapLogger(),
			TracerProvider: noop.NewTracerProvider(),
			MeterProvider:  noopM.NewMeterProvider(),
			Resource:       pcommon.NewResource(),
		},
		reg:               reg,
		sink:              reg.metricsSinkForItem(""),
		persister:         persister,
		containerSources:  make(map[string]*source),
		watchedContainers: make(map[string]string),
	}

	// Declare every configured metric name (legacy and new-style) so MetricNames() is complete
	// immediately, without waiting for a dynamically-resolved source (container) to appear.
	man.reg.declare(collectAllCounters(cfg))

	return man
}

// collectAllCounters gathers every LogCounter declared anywhere in the config.
// cfg.Metrics is expected to already be the output of translateLegacyInputs.
func collectAllCounters(cfg config.Log) []config.LogCounter {
	var counters []config.LogCounter

	for _, metricsRecv := range cfg.Metrics.Receivers {
		counters = append(counters, metricsRecv.Counters...)
	}

	for _, metricsKnownCounter := range cfg.Metrics.KnownCounters {
		counters = append(counters, metricsKnownCounter...)
	}

	counters = append(counters, cfg.Metrics.Network.Counters...)

	return counters
}

// Run starts static sources once, then every updateInterval starts/stops container
// sources as containers appear/disappear (skipped if cfg has no container-based
// rule), retries static sources that found no log file yet (e.g. not created at
// startup), starts watching any new file matching an already-running static
// source's include pattern (e.g. daily-rotated logs), and saves persisted read
// offsets to the state cache.
func (man *Manager) Run(ctx context.Context) error {
	defer crashreport.ProcessPanic()

	man.startStaticSources(ctx)
	man.startNetworkSource(ctx)

	watchContainers := hasContainerCounters(man.cfg)

	for ctx.Err() == nil {
		if watchContainers {
			man.updateContainerSources(ctx)
		}

		man.retryPendingStaticSources(ctx)
		man.updateStaticSources(ctx)

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
		man.persister.SaveToState(man.state)
	}
}

// hasHostRoot reports whether Glouton runs with a non-trivial hostroot (i.e.
// containerized, with the host filesystem bind-mounted): a sudo-tail fallback
// can't reach a path that only makes sense under that mount, since the sudo
// command runs in Glouton's own mount namespace, not the host's.
func (man *Manager) hasHostRoot() bool {
	return len(man.hostroot) > len(string(os.PathSeparator))
}

func (man *Manager) startStaticSources(ctx context.Context) {
	man.l.Lock()
	defer man.l.Unlock()

	for _, recv := range man.cfg.Metrics.Receivers {
		include := make([]string, len(recv.Include))

		for i, pattern := range recv.Include {
			include[i] = filepath.Join(man.hostroot, pattern)
		}

		man.startStaticSource(ctx, include, recv.Counters)
	}
}

func (man *Manager) startStaticSource(ctx context.Context, include []string, counters []config.LogCounter) {
	if len(counters) == 0 {
		return
	}

	man.reg.resolve(counters, "")

	src, err := newSource(ctx, man.telemetry, include, false, man.hasHostRoot(), counters, man.sink, man.persister, man.commandRunner, logsource.StatFile, staticSourceName(include))
	if err != nil {
		logger.V(1).Printf("logmetrics: no log file yet for %v, will retry: %v", include, err)

		man.pendingStatic = append(man.pendingStatic, pendingStaticSource{include: include, counters: counters})

		return
	}

	man.staticSources = append(man.staticSources, src)
}

// retryPendingStaticSources retries static sources that previously found no
// log file at all yet (e.g. not created yet at startup).
func (man *Manager) retryPendingStaticSources(ctx context.Context) {
	man.l.Lock()
	defer man.l.Unlock()

	if len(man.pendingStatic) == 0 {
		return
	}

	stillPending := man.pendingStatic[:0]

	for _, pending := range man.pendingStatic {
		man.reg.resolve(pending.counters, "")

		src, err := newSource(ctx, man.telemetry, pending.include, false, man.hasHostRoot(), pending.counters, man.sink, man.persister, man.commandRunner, logsource.StatFile, staticSourceName(pending.include))
		if err != nil {
			stillPending = append(stillPending, pending)

			continue
		}

		man.staticSources = append(man.staticSources, src)
	}

	man.pendingStatic = stillPending
}

// updateStaticSources starts a receiver for any file newly matching an
// already-running static source's include pattern (e.g. a daily-rotated log),
// without disturbing already-running receivers for that same source.
func (man *Manager) updateStaticSources(ctx context.Context) {
	man.l.Lock()
	defer man.l.Unlock()

	for _, src := range man.staticSources {
		if err := src.update(ctx); err != nil {
			logger.V(1).Printf("logmetrics: failed to update source: %v", err)
		}
	}
}

// staticSourceName is a stable persisted-offset identity for a static source, across restarts.
func staticSourceName(include []string) string {
	return "path:" + strings.Join(include, ",")
}

// startNetworkSource starts the gRPC/HTTP OTLP network source, if configured.
func (man *Manager) startNetworkSource(ctx context.Context) {
	man.l.Lock()
	defer man.l.Unlock()

	man.reg.resolve(man.cfg.Metrics.Network.Counters, "")

	src, err := newNetworkSource(ctx, man.telemetry, man.cfg.Metrics.Network, man.sink)
	if err != nil {
		logger.V(1).Printf("logmetrics: failed to start network source: %v", err)

		return
	}

	man.networkSource = src // nil if disabled or unconfigured, which is fine
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

		counters := resolveContainerCounters(man.cfg, ctr)
		if len(counters) == 0 {
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

		man.reg.resolve(counters, ctr.ContainerName())

		src, err := newSource(ctx, man.telemetry, []string{logPath}, true, man.hasHostRoot(), counters, man.reg.metricsSinkForItem(ctr.ContainerName()), man.persister, man.commandRunner, logsource.StatFile, name)
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

	if man.networkSource != nil {
		if err := man.networkSource.stop(ctx); err != nil {
			logger.V(1).Printf("logmetrics: failed to stop network source: %v", err)
		}
	}
}

// EmitMetrics implements registry.AppenderFunc, reporting the current "matches per
// second" rate of every configured log-to-metric counter.
func (man *Manager) EmitMetrics(_ context.Context, _ registry.GatherState, app storage.Appender) error {
	return man.reg.emit(app)
}

// MetricNames returns the name of every configured log-to-metric counter, so it can
// be fed into the metric allow-list (see agent.rebuildDynamicMetricAllowDenyList).
func (man *Manager) MetricNames() []string {
	return man.reg.metricNames()
}

// staticSourceDiagnostic and containerSourceDiagnostic describe a source's
// resolved log files for the diagnostic archive.
type staticSourceDiagnostic struct {
	Include              []string
	FileLogReceiverPaths []string
	ExecLogReceiverPaths []string
}

type containerSourceDiagnostic struct {
	Name                 string
	FileLogReceiverPaths []string
	ExecLogReceiverPaths []string
}

// networkSourceDiagnostic describes the network source for the diagnostic archive.
type networkSourceDiagnostic struct {
	Active      bool
	GRPCEnabled bool
	GRPCAddress string
	HTTPEnabled bool
	HTTPAddress string
	MetricNames []string
}

func (man *Manager) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("log-to-metrics.json")
	if err != nil {
		return err
	}

	man.l.Lock()

	staticSources := make([]staticSourceDiagnostic, 0, len(man.staticSources))

	for _, src := range man.staticSources {
		fileLogPaths, execLogPaths := src.watchedFiles()
		staticSources = append(staticSources, staticSourceDiagnostic{
			Include:              src.include,
			FileLogReceiverPaths: fileLogPaths,
			ExecLogReceiverPaths: execLogPaths,
		})
	}

	containerSources := make(map[string]containerSourceDiagnostic, len(man.containerSources))

	for id, src := range man.containerSources {
		fileLogPaths, execLogPaths := src.watchedFiles()
		containerSources[id] = containerSourceDiagnostic{
			Name:                 man.watchedContainers[id],
			FileLogReceiverPaths: fileLogPaths,
			ExecLogReceiverPaths: execLogPaths,
		}
	}

	netCfg := man.cfg.Metrics.Network

	metricNames := make([]string, 0, len(netCfg.Counters))
	for _, counter := range netCfg.Counters {
		metricNames = append(metricNames, counter.Metric)
	}

	networkSource := networkSourceDiagnostic{
		Active:      man.networkSource != nil,
		GRPCEnabled: netCfg.GRPC.Enable,
		HTTPEnabled: netCfg.HTTP.Enable,
		MetricNames: metricNames,
	}

	if netCfg.GRPC.Enable {
		networkSource.GRPCAddress = fmt.Sprintf("%s:%d", netCfg.GRPC.Address, netCfg.GRPC.Port)
	}

	if netCfg.HTTP.Enable {
		networkSource.HTTPAddress = fmt.Sprintf("%s:%d", netCfg.HTTP.Address, netCfg.HTTP.Port)
	}

	info := struct {
		MetricNames        []string
		StaticSources      []staticSourceDiagnostic
		ContainerSources   map[string]containerSourceDiagnostic
		PendingStaticCount int
		NetworkSource      networkSourceDiagnostic
	}{
		MetricNames:        man.reg.metricNames(),
		StaticSources:      staticSources,
		ContainerSources:   containerSources,
		PendingStaticCount: len(man.pendingStatic),
		NetworkSource:      networkSource,
	}
	man.l.Unlock()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	if err := enc.Encode(info); err != nil {
		return err
	}

	if man.persister != nil {
		return man.persister.WriteToArchive(archive)
	}

	return nil
}
