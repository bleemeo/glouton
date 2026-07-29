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
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/logsource"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/uuid"
	"github.com/open-telemetry/opentelemetry-collector-contrib/connector/countconnector"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"go.opentelemetry.io/collector/component"
	otelconnector "go.opentelemetry.io/collector/connector"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/receiver"
)

var (
	errNoValidCounter = errors.New("no valid counter for source")
	errNoLogFileFound = errors.New("no log file found for source")
)

// source is one OTel mini-pipeline for a single log-to-metric source (a static
// path or a resolved container log file):
//
//	filelogreceiver/execlogreceiver --(plog.Logs)--> countconnector(s) --(pmetric.Metrics)--> shared registry sink
//
// Normally one connector handles all of a source's counters, falling back to
// one connector per counter if the combined config fails validation.
//
// A source may resolve to several log files (glob include pattern), each with
// its own receiver (filelogreceiver, or execlogreceiver as a sudo-tail
// fallback). update() starts receivers for newly-appeared files without
// disturbing already-running ones.
type source struct {
	telemetry     component.TelemetrySettings
	include       []string
	hasHostRoot   bool
	operators     []operator.Config
	extraRaw      map[string]any // raw pass-through into the real filelogreceiver config
	commandRunner logsource.CommandRunner
	statFile      logsource.StatFileFunc
	name          string
	recvConsumer  consumer.Logs

	persister     *logsource.PersistHost // nil if this source runs without persisted offsets
	lastFileSizes map[string]int64       // cross-restart "have we ever seen this file" cache

	l            sync.Mutex
	watching     map[string]logsource.ReceiverKind
	recvs        []receiver.Logs
	extIDs       []component.ID                   // valid only if persister != nil, one per log file
	sizeFnByFile map[string]func() (int64, error) // for SizesByFile

	conns []otelconnector.Logs
}

// newSource builds and starts a source. include is a list of glob patterns
// with hostroot already applied; hasHostRoot disables the sudo-tail fallback
// when true. If isContainer, the Docker/CRI envelope is unwrapped before
// formatOperators run. If persister is non-nil, name is the stable
// persisted-offset identity used to resume tailing across restarts.
// lastFileSizes is the fallback "have we ever seen this file" cache used when
// persister is nil.
//
// count and specs are the full, global log.metrics.count registry; sourceName
// identifies this source for count's own "sources" field, and kind
// (kindReceiver, kindContainer or kindNetwork) is this source's kind, used to
// resolve each global metric's own per_<kind>_item flag (see
// groupMetricsByItem).
func newSource(
	ctx context.Context,
	telemetry component.TelemetrySettings,
	include []string,
	isContainer bool,
	hasHostRoot bool,
	count map[string]config.LogMetricsCount,
	formatOperators []operator.Config,
	extraRaw map[string]any,
	specs []metricSpec,
	sourceName string,
	kind string,
	reg *metricsRegistry,
	persister *logsource.PersistHost,
	lastFileSizes map[string]int64,
	commandRunner logsource.CommandRunner,
	statFile logsource.StatFileFunc,
	name string,
) (*source, error) {
	conns, err := buildGroupedConnectors(ctx, telemetry, count, specs, sourceName, kind, reg, name)
	if err != nil {
		return nil, err
	}

	var operators []operator.Config

	if isContainer {
		operators = append(operators, logsource.BuildContainerEnvelopeOperator())
	}

	operators = append(operators, formatOperators...)

	src := &source{
		telemetry:     telemetry,
		include:       include,
		hasHostRoot:   hasHostRoot,
		operators:     operators,
		extraRaw:      extraRaw,
		commandRunner: commandRunner,
		statFile:      statFile,
		name:          name,
		recvConsumer:  nextConsumer(conns),
		persister:     persister,
		lastFileSizes: lastFileSizes,
		watching:      make(map[string]logsource.ReceiverKind),
		conns:         conns,
	}

	if err := src.addNewFiles(ctx); err != nil {
		shutdownConns(ctx, conns)

		return nil, err
	}

	if len(src.watching) == 0 {
		shutdownConns(ctx, conns)

		return nil, errNoLogFileFound
	}

	return src, nil
}

// watchedFiles returns the log files currently covered by a running receiver.
func (s *source) watchedFiles() (fileLogPaths, execLogPaths []string) {
	s.l.Lock()
	defer s.l.Unlock()

	for f, kind := range s.watching {
		switch kind {
		case logsource.ReceiverFileLog:
			fileLogPaths = append(fileLogPaths, f)
		case logsource.ReceiverExecLog:
			execLogPaths = append(execLogPaths, f)
		default:
			logger.V(1).Printf("logmetrics: unknown log receiver kind %q for file %q", kind, f)
		}
	}

	slices.Sort(fileLogPaths)
	slices.Sort(execLogPaths)

	return fileLogPaths, execLogPaths
}

// SizesByFile returns the size of each log file watched by this source, for
// the cross-restart lastFileSizes cache.
func (s *source) SizesByFile() (map[string]int64, error) {
	s.l.Lock()
	defer s.l.Unlock()

	sizes := make(map[string]int64, len(s.sizeFnByFile))

	for logFile, sizeFn := range s.sizeFnByFile {
		size, err := sizeFn()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}

			return nil, err
		}

		sizes[logFile] = size
	}

	return sizes, nil
}

// update starts a receiver for any file newly matching this source's include
// patterns that isn't already being watched. Already-running receivers are
// left untouched.
func (s *source) update(ctx context.Context) error {
	s.l.Lock()
	defer s.l.Unlock()

	return s.addNewFiles(ctx)
}

// addNewFiles must be called with s.l held.
func (s *source) addNewFiles(ctx context.Context) error {
	logFiles := expandIncludePatterns(s.include, s.hasHostRoot)

	var newFiles []string

	for _, f := range logFiles {
		if _, ok := s.watching[f]; !ok {
			newFiles = append(newFiles, f)
		}
	}

	if len(newFiles) == 0 {
		return nil
	}

	var (
		host      component.Host
		newExtIDs []component.ID
	)

	makeStorageFn := func(string) *component.ID { return nil }

	if s.persister != nil {
		host = s.persister
		makeStorageFn = func(logFile string) *component.ID {
			id := s.persister.NewPersistentExt(s.name + "/" + logFile)
			newExtIDs = append(newExtIDs, id)

			return &id
		}
	}

	factories, readFiles, execFiles, newSizeFns, err := logsource.SetupLogReceiverFactories(
		newFiles,
		"", // logFiles are already fully resolved (hostroot applied by the caller)
		s.operators,
		s.lastFileSizes,
		s.commandRunner,
		makeStorageFn,
		s.statFile,
		nil,
		s.extraRaw,
	)
	if err != nil {
		if s.persister != nil {
			s.persister.RemovePersistentExts(newExtIDs)
		}

		return fmt.Errorf("setting up receiver factories: %w", err)
	}

	if len(readFiles) > 0 {
		logger.V(2).Printf("logmetrics: source %q opened log file(s) directly: %v", s.name, readFiles)
	}

	if len(execFiles) > 0 {
		logger.V(2).Printf("logmetrics: source %q opened log file(s) via sudo: %v", s.name, execFiles)
	}

	if len(factories) == 0 {
		if s.persister != nil {
			s.persister.RemovePersistentExts(newExtIDs)
		}

		return nil // nothing new actually resolved (e.g. every new file vanished/unreadable)
	}

	newRecvs := make([]receiver.Logs, 0, len(factories))

	for factory, recvCfg := range factories {
		recv, err := factory.CreateLogs(
			ctx,
			receiver.Settings{
				ID:                component.NewIDWithName(factory.Type(), uuid.NewString()),
				TelemetrySettings: s.telemetry,
			},
			recvCfg,
			s.recvConsumer,
		)
		if err != nil {
			shutdownReceivers(ctx, newRecvs)

			if s.persister != nil {
				s.persister.RemovePersistentExts(newExtIDs)
			}

			return fmt.Errorf("build receiver: %w", err)
		}

		if err := recv.Start(ctx, host); err != nil {
			shutdownReceivers(ctx, newRecvs)

			if s.persister != nil {
				s.persister.RemovePersistentExts(newExtIDs)
			}

			return fmt.Errorf("start receiver: %w", err)
		}

		newRecvs = append(newRecvs, recv)
	}

	s.recvs = append(s.recvs, newRecvs...)
	s.extIDs = append(s.extIDs, newExtIDs...)

	if s.sizeFnByFile == nil {
		s.sizeFnByFile = make(map[string]func() (int64, error), len(newSizeFns))
	}

	maps.Copy(s.sizeFnByFile, newSizeFns)

	for _, f := range readFiles {
		s.watching[f] = logsource.ReceiverFileLog
	}

	for _, f := range execFiles {
		s.watching[f] = logsource.ReceiverExecLog
	}

	return nil
}

// expandIncludePatterns resolves glob patterns into actual file paths. A
// pattern that hits a permission error is passed through as a literal path
// (when it has no wildcard and hasHostRoot is false) to allow a sudo-tail
// fallback; otherwise it's dropped.
func expandIncludePatterns(patterns []string, hasHostRoot bool) []string {
	seen := make(map[string]bool, len(patterns))

	var files []string

	for _, pattern := range patterns {
		matches, err := doublestar.FilepathGlob(pattern, doublestar.WithFilesOnly(), doublestar.WithFailOnIOErrors())
		if err != nil {
			if errors.Is(err, doublestar.ErrBadPattern) {
				logger.V(1).Printf("logmetrics: file pattern %q: %v", pattern, err)

				continue
			}

			if errors.Is(err, fs.ErrPermission) && !hasHostRoot && !strings.Contains(pattern, "*") {
				matches = []string{pattern} // still a chance via sudo tail
			} else {
				logger.V(1).Printf("logmetrics: file %q: %v", pattern, err)

				continue
			}
		}

		for _, m := range matches {
			if !seen[m] {
				seen[m] = true

				files = append(files, m)
			}
		}
	}

	return files
}

func shutdownReceivers(ctx context.Context, recvs []receiver.Logs) {
	for _, recv := range recvs {
		_ = recv.Shutdown(ctx)
	}
}

// filterCount restricts count to names, dropping (with a warning identifying
// srcCtx) any name that isn't a real count entry. Returns count unchanged if
// names is empty -- the default, global-registry behavior.
func filterCount(count map[string]config.LogMetricsCount, names []string, srcCtx string) map[string]config.LogMetricsCount {
	if len(names) == 0 {
		return count
	}

	filtered := make(map[string]config.LogMetricsCount, len(names))

	for _, name := range names {
		raw, ok := count[name]
		if !ok {
			logger.Printf("logmetrics: %s: metric %q listed but not defined in log.metrics.count", srcCtx, name)

			continue
		}

		filtered[name] = raw
	}

	return filtered
}

// filterSpecs is filterCount's counterpart for metricSpecs, so a scoped
// source only registers the metrics it can actually produce.
func filterSpecs(specs []metricSpec, names []string) []metricSpec {
	if len(names) == 0 {
		return specs
	}

	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}

	filtered := make([]metricSpec, 0, len(names))

	for _, spec := range specs {
		if wanted[spec.Metric] {
			filtered = append(filtered, spec)
		}
	}

	return filtered
}

// extractSources reads the "sources" field out of a raw config.LogMetricsCount
// entry -- the metric-side counterpart naming which receivers/containers/the
// network source feed it. Unset or empty means global (every source).
func extractSources(raw config.LogMetricsCount) []string {
	rawSources, _ := raw["sources"].([]any)
	if len(rawSources) == 0 {
		return nil
	}

	sources := make([]string, 0, len(rawSources))

	for _, v := range rawSources {
		if s, ok := v.(string); ok {
			sources = append(sources, s)
		}
	}

	return sources
}

// Source kinds, used both as groupMetricsByItem's kind parameter and as the
// "per_<kind>_item" raw config key suffix (see extractPerItemFlag).
const (
	kindReceiver  = "receiver"
	kindContainer = "container"
	kindNetwork   = "network"
)

// perItemDefault is the fallback when a metric doesn't set its own
// "per_<kind>_item" flag. Containers default to true: a container's identity
// isn't known ahead of time (dynamic discovery, restarts, replica suffixes),
// so there's no practical way to opt one in via "sources" just to get a
// per-instance item -- it needs one automatically. Receivers and the network
// source default to false: they're named statically in config, so "sources"
// is already a cheap way to opt one in when a separate item is wanted.
func perItemDefault(kind string) bool {
	return kind == kindContainer
}

// extractPerItemFlag reads a raw config.LogMetricsCount entry's
// "per_<kind>_item" override, falling back to perItemDefault(kind). It only
// affects a metric with no "sources" (global): whether each matching source
// of that kind still gets its own item, or merges into the shared item="".
func extractPerItemFlag(raw config.LogMetricsCount, kind string) bool {
	v, ok := raw["per_"+kind+"_item"].(bool)
	if !ok {
		return perItemDefault(kind)
	}

	return v
}

// groupMetricsByItem partitions count's metric names by the item label
// sourceName (of the given kind: kindReceiver, kindContainer or kindNetwork)
// should report them under. A metric naming only sourceName in "sources" gets
// its own item (sourceName); one naming 2+ sources including sourceName
// merges into a single series with no item at all; one naming sources that
// don't include sourceName isn't fed by this source and produces nothing
// here. A metric with no "sources" (global) gets sourceName as its item if
// its own per_<kind>_item flag is true (see extractPerItemFlag), else "".
func groupMetricsByItem(count map[string]config.LogMetricsCount, sourceName, kind string) map[string][]string {
	groups := make(map[string][]string)

	for name, raw := range count {
		switch sources := extractSources(raw); {
		case len(sources) == 0:
			item := ""
			if extractPerItemFlag(raw, kind) {
				item = sourceName
			}

			groups[item] = append(groups[item], name)
		case len(sources) == 1:
			if sources[0] == sourceName {
				groups[sourceName] = append(groups[sourceName], name)
			}
		default:
			if slices.Contains(sources, sourceName) {
				groups[""] = append(groups[""], name)
			}
		}
	}

	return groups
}

// buildGroupedConnectors partitions count by item via groupMetricsByItem and
// builds one connector set per resulting group, so a single source can feed
// several different item buckets at once (e.g. its own scoped metric plus a
// shared global one). A group that fails to produce any valid counter is
// logged and skipped, not fatal; errNoValidCounter is only returned if
// nothing survived across every group.
func buildGroupedConnectors(
	ctx context.Context,
	telemetry component.TelemetrySettings,
	count map[string]config.LogMetricsCount,
	specs []metricSpec,
	sourceName string,
	kind string,
	reg *metricsRegistry,
	name string,
) ([]otelconnector.Logs, error) {
	connFactory := countconnector.NewFactory()

	var conns []otelconnector.Logs

	for item, names := range groupMetricsByItem(count, sourceName, kind) {
		groupCount := filterCount(count, names, name)
		if len(groupCount) == 0 {
			continue
		}

		reg.resolve(filterSpecs(specs, names), item)

		groupConns, err := buildConnectors(ctx, connFactory, telemetry, groupCount, reg.metricsSinkForItem(item))
		if err != nil {
			logger.Printf("logmetrics: source %q: item %q: %v", name, item, err)

			continue
		}

		conns = append(conns, groupConns...)
	}

	if len(conns) == 0 {
		return nil, errNoValidCounter
	}

	return conns, nil
}

// buildConnectors tries one connector for all of count together. If that
// combined config fails validation, it falls back to one connector per metric
// so a bad condition only disables its own metric. A metric whose raw config
// can't be decoded is excluded from both paths, with a warning.
func buildConnectors(
	ctx context.Context,
	connFactory otelconnector.Factory,
	telemetry component.TelemetrySettings,
	count map[string]config.LogMetricsCount,
	sink consumer.Metrics,
) ([]otelconnector.Logs, error) {
	infos := make(map[string]countconnector.MetricInfo, len(count))

	for name, raw := range count {
		info, err := metricInfo(name, raw)
		if err != nil {
			logger.Printf("logmetrics: metric %q disabled, invalid config: %v", name, err)

			continue
		}

		infos[name] = info
	}

	if len(infos) == 0 {
		return nil, errNoValidCounter
	}

	combinedCfg := &countconnector.Config{Logs: infos}

	if combinedCfg.Validate() == nil {
		if conn, err := createConnector(ctx, connFactory, telemetry, combinedCfg, sink); err == nil {
			return []otelconnector.Logs{conn}, nil
		}
	}

	conns := make([]otelconnector.Logs, 0, len(infos))

	for name, info := range infos {
		counterCfg := &countconnector.Config{Logs: map[string]countconnector.MetricInfo{name: info}}

		if err := counterCfg.Validate(); err != nil {
			logger.Printf("logmetrics: metric %q disabled, invalid counter: %v", name, err)

			continue
		}

		conn, err := createConnector(ctx, connFactory, telemetry, counterCfg, sink)
		if err != nil {
			logger.Printf("logmetrics: metric %q disabled: %v", name, err)

			continue
		}

		conns = append(conns, conn)
	}

	if len(conns) == 0 {
		return nil, errNoValidCounter
	}

	return conns, nil
}

// metricInfo builds the countconnector.MetricInfo for metric name from its raw
// config.LogMetricsCount, decoding straight into the vendored struct so an
// existing connectors.count.logs.<metric> definition pastes in almost
// verbatim. "labels" isn't a real countconnector field and is ignored here;
// extractLabels reads it separately. A decode failure returns an error rather
// than silently falling back to "count everything".
func metricInfo(name string, raw config.LogMetricsCount) (countconnector.MetricInfo, error) {
	info := countconnector.MetricInfo{
		Description: "log-to-metric: " + name,
	}

	if len(raw) == 0 {
		return info, nil
	}

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{Result: &info})
	if err != nil {
		return countconnector.MetricInfo{}, fmt.Errorf("creating decoder: %w", err)
	}

	if err := decoder.Decode(raw); err != nil {
		return countconnector.MetricInfo{}, fmt.Errorf("decoding config: %w", err)
	}

	return info, nil
}

// extractLabels reads the "labels" field out of a raw config.LogMetricsCount
// entry, the one field metricInfo's decode never sets.
func extractLabels(raw config.LogMetricsCount) map[string]string {
	rawLabels, _ := raw["labels"].(map[string]any)
	if len(rawLabels) == 0 {
		return nil
	}

	labels := make(map[string]string, len(rawLabels))

	for k, v := range rawLabels {
		if s, ok := v.(string); ok {
			labels[k] = s
		}
	}

	return labels
}

func createConnector(
	ctx context.Context,
	factory otelconnector.Factory,
	telemetry component.TelemetrySettings,
	cfg *countconnector.Config,
	sink consumer.Metrics,
) (otelconnector.Logs, error) {
	conn, err := factory.CreateLogsToMetrics(
		ctx,
		otelconnector.Settings{
			ID:                component.NewIDWithName(factory.Type(), uuid.NewString()),
			TelemetrySettings: telemetry,
		},
		cfg,
		sink,
	)
	if err != nil {
		return nil, fmt.Errorf("build connector: %w", err)
	}

	if err := conn.Start(ctx, nil); err != nil {
		_ = conn.Shutdown(ctx)

		return nil, fmt.Errorf("start connector: %w", err)
	}

	return conn, nil
}

// nextConsumer avoids the fan-out wrapper entirely in the common case (a
// single connector, e.g. one counter or the combined fast path).
func nextConsumer(conns []otelconnector.Logs) consumer.Logs {
	if len(conns) == 1 {
		return conns[0]
	}

	return fanoutLogs(conns)
}

// fanoutLogs forwards each batch to every conn. Sharing one plog.Logs across
// all of them is safe: countconnector never mutates its input.
func fanoutLogs(conns []otelconnector.Logs) consumer.Logs {
	fanout, err := consumer.NewLogs(func(ctx context.Context, ld plog.Logs) error {
		var errs error

		for _, conn := range conns {
			errs = errors.Join(errs, conn.ConsumeLogs(ctx, ld))
		}

		return errs
	})
	if err != nil {
		panic(err) // only fails if the func were nil
	}

	return fanout
}

func shutdownConns(ctx context.Context, conns []otelconnector.Logs) {
	for _, conn := range conns {
		_ = conn.Shutdown(ctx)
	}
}

func (s *source) stop(ctx context.Context) error {
	s.l.Lock()
	recvs := s.recvs
	extIDs := s.extIDs
	s.l.Unlock()

	var recvErr error

	for _, recv := range recvs {
		recvErr = errors.Join(recvErr, recv.Shutdown(ctx))
	}

	if s.persister != nil {
		s.persister.RemovePersistentExts(extIDs)
	}

	var connErr error

	for _, conn := range s.conns {
		connErr = errors.Join(connErr, conn.Shutdown(ctx))
	}

	if recvErr != nil {
		return recvErr
	}

	return connErr
}
