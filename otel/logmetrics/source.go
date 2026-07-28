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
// Normally one connector handles all of a source's counters. If that combined
// config fails validation, it falls back to one connector per counter (fanned
// out) so a bad regex only disables its own metric.
//
// A source may resolve to several log files (e.g. include is a glob pattern),
// each gets its own receiver (filelogreceiver, or execlogreceiver as a
// sudo-tail fallback for a file this process can't read directly). update()
// can be called periodically to start receivers for newly-appeared files
// matching the same include patterns, without disturbing already-running ones
// -- unlike a glob handed directly to a single long-lived filelogreceiver,
// this needs to be driven explicitly (see Manager.updateStaticSources).
type source struct {
	telemetry     component.TelemetrySettings
	include       []string
	hasHostRoot   bool
	operators     []operator.Config
	extraRaw      map[string]any // raw pass-through into the real filelogreceiver config, see LogMetricsReceiver.Raw
	commandRunner logsource.CommandRunner
	statFile      logsource.StatFileFunc
	name          string
	recvConsumer  consumer.Logs

	persister *logsource.PersistHost // nil if this source runs without persisted offsets

	l        sync.Mutex
	watching map[string]logsource.ReceiverKind
	recvs    []receiver.Logs
	extIDs   []component.ID // valid only if persister != nil, one per underlying log file

	conns []otelconnector.Logs
}

// newSource builds and starts a source. include is a list of glob patterns,
// with hostroot already applied (hasHostRoot reports whether that hostroot is
// non-trivial, in which case sudo-tail isn't attempted: same restriction
// otel/logprocessing applies, since a sudo command run from Glouton's own
// mount namespace can't reach a path that only makes sense under hostroot).
// If isContainer, the Docker/CRI envelope is unwrapped first (same operator
// otel/logprocessing uses), before formatOperators (from
// LogMetricsReceiver.LogFormat, if set) run, so a format parses the actual
// log line rather than its envelope. If persister is non-nil, name is used as
// this source's stable persisted-offset identity (a joined path list for
// static sources, a container ID for container sources), so a restart
// resumes tailing instead of skipping to the file's end.
func newSource(
	ctx context.Context,
	telemetry component.TelemetrySettings,
	include []string,
	isContainer bool,
	hasHostRoot bool,
	count map[string]config.LogMetricsCount,
	formatOperators []operator.Config,
	extraRaw map[string]any,
	sink consumer.Metrics,
	persister *logsource.PersistHost,
	commandRunner logsource.CommandRunner,
	statFile logsource.StatFileFunc,
	name string,
) (*source, error) {
	connFactory := countconnector.NewFactory()

	conns, err := buildConnectors(ctx, connFactory, telemetry, count, sink)
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

	factories, readFiles, execFiles, _, err := logsource.SetupLogReceiverFactories(
		newFiles,
		"", // logFiles are already fully resolved (hostroot applied by the caller)
		s.operators,
		nil, // no cross-restart file-size tracking (yet) for log-to-metric sudo-tail sources
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

	for _, f := range readFiles {
		s.watching[f] = logsource.ReceiverFileLog
	}

	for _, f := range execFiles {
		s.watching[f] = logsource.ReceiverExecLog
	}

	return nil
}

// expandIncludePatterns resolves glob patterns into actual file paths (already
// fully hostroot-resolved). A pattern that can't be listed due to a permission
// error is passed through as a literal path when it has no wildcard and
// hasHostRoot is false, giving SetupLogReceiverFactories/StatFile a chance to
// fall back to a sudo-tail (execlogreceiver) -- otherwise it's dropped.
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
				// We still have a chance to handle it with a sudo tail.
				matches = []string{pattern}
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

// buildConnectors tries one connector for all of count together (fast path: one
// tree walk and one registry lock per batch). If that combined config fails
// validation, it falls back to one connector per metric so a bad condition only
// disables its own metric instead of every metric on this source. A metric
// whose raw config can't even be decoded (see metricInfo) is excluded from both
// paths entirely, with a visible warning -- it never silently falls back to
// matching every log record.
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
// config.LogMetricsCount, decoding straight into the real vendored struct
// (description, conditions, attributes -- same trick as OTLPReceiver), so an
// existing connectors.count.logs.<metric> definition pastes in almost
// verbatim. "labels" isn't a real countconnector field (see LogMetricsCount's
// doc comment) and is silently ignored by the decode; extractLabels is what
// reads it. A metric with no conditions at all counts every log record on
// every source unconditionally, matching real countconnector semantics -- but
// only when that's genuinely what the config says: a raw value that fails to
// decode at all (e.g. "conditions" given as a bare string instead of a list)
// returns an error instead of silently falling back to that same "count
// everything" shape, which would otherwise turn a config mistake into
// wildly-wrong metrics with no visible sign anything is broken.
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
// entry -- the one field metricInfo's decode above never sets, since it has
// no real countconnector counterpart (see LogMetricsCount's doc comment).
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
