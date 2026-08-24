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

package logprocessing

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/logsource"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/uuid"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	stanzaErrors "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/stanzaerrors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/filterprocessor"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/receiver"
)

var receiverNameRegex = regexp.MustCompile(`^(filelog/)?[^/]+$`)

var errInvalidReceiverName = errors.New("invalid receiver name")

const metadataKeySeparator = "/"

// logReceiver's tail lifecycle bookkeeping (watching/sizeFnByFile/startedComponents/registeredExtensions)
// independently parallels otel/logsource's managedSource (receiver_manager.go) and this package's own
// containerReceiver (containers.go) -- each tracks a differently-shaped fan-out chain, so they haven't been
// unified, but a fix to one's tail-start/stop or offset-forget logic likely applies to the others too.
type logReceiver struct {
	name string
	// cfg is the raw config (see config.LogReceiver), passed through as-is to SetupLogReceiverFactories;
	// include is also pulled out here for Glouton's own glob-resolution logic below.
	cfg             config.LogReceiver
	include         []string
	isFromService   bool
	logConsumer     consumer.Logs
	operators       []operator.Config
	filterCfg       *filterprocessor.Config
	setupFilterDone bool
	statFile        logsource.StatFileFunc

	// l should always be acquired after the pipeline lock
	l            sync.Mutex
	watching     map[string]logsource.ReceiverKind
	sizeFnByFile map[string]func() (int64, error)
	// startedComponents is only used if the receiver is from a service
	startedComponents    []component.Component
	registeredExtensions []component.ID

	logCounter      *atomic.Int64
	throughputMeter *logsource.RingCounter
}

func newLogReceiver(
	name string,
	cfg config.LogReceiver,
	isFromService bool,
	logConsumer consumer.Logs,
	knownLogFormats map[string][]config.OTELOperator,
	statFile logsource.StatFileFunc,
) (*logReceiver, error, error) {
	if !receiverNameRegex.MatchString(name) {
		return nil, nil, fmt.Errorf("%w: %q. It must be of the form 'my-receiver' or 'filelog/my-receiver'", errInvalidReceiverName, name) //nolint: nilnil
	}

	// cfg is raw; pull out the fields Glouton's own logic needs (log_format/filters have no equivalent
	// in the real schema).
	var fields struct {
		Include   []string              `mapstructure:"include"`
		Operators []config.OTELOperator `mapstructure:"operators"`
		LogFormat string                `mapstructure:"log_format"`
		Filters   config.OTELFilters    `mapstructure:"filters"`
	}

	if err := mapstructure.Decode(cfg, &fields); err != nil {
		return nil, nil, fmt.Errorf("decoding receiver %q config: %w", name, err) //nolint: nilnil
	}

	rawOps, err := logsource.ExpandOperators(fields.Operators, knownLogFormats, false)
	if err != nil {
		return nil, nil, fmt.Errorf("expanding operators: %w", err) //nolint: nilnil
	}

	operators, err := logsource.BuildOperators(rawOps)
	if err != nil {
		return nil, nil, fmt.Errorf("building operators: %w", err) //nolint: nilnil
	}

	if fields.LogFormat != "" {
		opsGroup, found := knownLogFormats[fields.LogFormat]
		if !found {
			logger.V(1).Printf("Log receiver %q requires the log format %q, which is not defined", name, fields.LogFormat)
		} else {
			// Operators from known log formats have already been expanded.
			referencedOps, err := logsource.BuildOperators(opsGroup)
			if err != nil {
				return nil, nil, fmt.Errorf("building globally-defined operators: %w", err) //nolint: nilnil
			}

			operators = append(operators, referencedOps...)
		}
	}

	filterCfg, warn, err := buildLogFilterConfig(fields.Filters)
	if err != nil {
		return nil, nil, fmt.Errorf("building filters: %w", err) //nolint: nilnil
	}

	if warn != nil {
		warn = fmt.Errorf("building filters: %w", warn)
	}

	return &logReceiver{
		name:            name,
		cfg:             cfg,
		include:         fields.Include,
		isFromService:   isFromService,
		logConsumer:     logConsumer,
		operators:       operators,
		filterCfg:       filterCfg,
		watching:        make(map[string]logsource.ReceiverKind, len(fields.Include)),
		sizeFnByFile:    make(map[string]func() (int64, error), len(fields.Include)),
		logCounter:      new(atomic.Int64),
		throughputMeter: logsource.NewRingCounter(throughputMeterResolutionSecs),
		statFile:        statFile,
	}, warn, nil
}

// update creates a log receiver for each unhandled file in the config. pipeline is passed in (not stored)
// to make clear its lock must be held during the call. Files are started one at a time (see startFile) so
// a failure on one file can't affect the others in the same call.
func (r *logReceiver) update(ctx context.Context, pipeline *pipelineContext, addWarnings func(...error)) error {
	r.l.Lock()
	defer r.l.Unlock()

	resolvedFiles := logsource.ResolveIncludeGlobs(pipeline.hostroot, r.include, func(msg string) {
		addWarnings(errorf("Log receiver %q: %s", r.name, msg))
	})

	var newFiles []string

	for _, realFile := range resolvedFiles {
		// Skip if already watching.
		if _, found := r.watching[realFile]; !found {
			newFiles = append(newFiles, realFile)
		}
	}

	if len(newFiles) == 0 {
		return nil
	}

	if !r.setupFilterDone {
		if err := r.setupFilters(ctx, pipeline); err != nil {
			return err
		}

		r.setupFilterDone = true
	}

	var errs error

	for _, file := range newFiles {
		if err := r.startFile(ctx, pipeline, file); err != nil {
			errs = errors.Join(errs, fmt.Errorf("file %q: %w", file, err))
		}
	}

	return errs
}

// startFile starts a single new file's receiver(s) under r. Processing one file at a time (instead of the
// whole newFiles batch through a single SetupLogReceiverFactories call) means a later file's failure can't
// leave an earlier, already-started file untracked in r.watching -- which would otherwise make the next
// update() call start a second, duplicate receiver tailing (and shipping) that same file. Any receiver or
// persistent storage extension started for this file is rolled back if a later step for the SAME file
// fails. Callers must hold r.l.
func (r *logReceiver) startFile(ctx context.Context, pipeline *pipelineContext, file string) error {
	var newExtIDs []component.ID

	makeStorageFn := func(logFile string) *component.ID {
		id := pipeline.persister.NewPersistentExt(r.name + metadataKeySeparator + logFile)

		newExtIDs = append(newExtIDs, id)

		return &id
	}

	factories, readFiles, execFiles, sizeFnByFile, err := logsource.SetupLogReceiverFactories(
		[]string{file},
		pipeline.hostroot,
		r.operators,
		pipeline.lastFileSizes,
		pipeline.commandRunner,
		makeStorageFn,
		r.statFile,
		nil,
		r.cfg,
	)
	if err != nil {
		pipeline.persister.RemovePersistentExts(newExtIDs)

		return fmt.Errorf("setting up receiver factories: %w", err)
	}

	newRecvs := make([]component.Component, 0, len(factories))

	for logReceiverFactory, logReceiverCfg := range factories {
		settings := receiver.Settings{
			ID:                component.NewIDWithName(logReceiverFactory.Type(), uuid.NewString()),
			TelemetrySettings: pipeline.telemetry,
		}

		logRcvr, err := logReceiverFactory.CreateLogs(
			ctx,
			settings,
			logReceiverCfg,
			r.logConsumer,
		)
		if err != nil {
			stopComponents(newRecvs)
			pipeline.persister.RemovePersistentExts(newExtIDs)

			var agentErr stanzaErrors.AgentError
			if errors.As(err, &agentErr) && agentErr.Suggestion != "" {
				return fmt.Errorf("setup receiver: %w (%s)", err, agentErr.Suggestion)
			}

			return fmt.Errorf("setup receiver: %w", err)
		}

		if err = logRcvr.Start(ctx, pipeline.persister); err != nil {
			if err := logRcvr.Shutdown(ctx); err != nil {
				logger.V(1).Printf("Unable to stop logRcvr: %s", err.Error())
			}

			stopComponents(newRecvs)
			pipeline.persister.RemovePersistentExts(newExtIDs)

			return fmt.Errorf("start receiver: %w", err)
		}

		newRecvs = append(newRecvs, logRcvr)
	}

	if r.isFromService {
		// Store in r.startedComponents (not pipeline) to find and stop it when the service disappears.
		r.startedComponents = append(r.startedComponents, newRecvs...)
	} else {
		pipeline.startedComponents = append(pipeline.startedComponents, newRecvs...)
	}

	r.registeredExtensions = append(r.registeredExtensions, newExtIDs...)

	switch {
	case len(readFiles) == 1:
		r.watching[file] = logsource.ReceiverFileLog
	case len(execFiles) == 1:
		r.watching[file] = logsource.ReceiverExecLog
	}

	maps.Insert(r.sizeFnByFile, maps.All(sizeFnByFile))

	return nil
}

// currentlyWatching returns the list of files that are being processed.
func (r *logReceiver) currentlyWatching() []string {
	r.l.Lock()
	defer r.l.Unlock()

	return slices.Collect(maps.Keys(r.watching))
}

// SizesByFile returns the size of each log file watched by this receiver.
func (r *logReceiver) SizesByFile() (map[string]int64, error) {
	r.l.Lock()
	defer r.l.Unlock()

	sizes := make(map[string]int64, len(r.sizeFnByFile))

	for logFile, sizeFn := range r.sizeFnByFile {
		size, err := sizeFn()
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				// We may not catch errors produced by the "sudo stat" cmd,
				// but this would not really be convenient ...
				logger.V(1).Printf("Can't get size of file %q (ignoring it): %v", logFile, err)
			}

			continue
		}

		sizes[logFile] = size
	}

	return sizes, nil
}

func (r *logReceiver) setupFilters(ctx context.Context, pipeline *pipelineContext) error {
	factoryFilter := filterprocessor.NewFactory()

	logFilter, err := factoryFilter.CreateLogs(
		ctx,
		processor.Settings{
			ID:                component.NewIDWithName(factoryFilter.Type(), "log-filter-recv-"+r.name),
			TelemetrySettings: withoutDebugLogs(pipeline.telemetry),
		},
		r.filterCfg,
		logsource.WrapWithInstrumentation(r.logConsumer, r.logCounter, r.throughputMeter),
	)
	if err != nil {
		return fmt.Errorf("setup log filter: %w", err)
	}

	if err = logFilter.Start(ctx, nil); err != nil {
		if err := logFilter.Shutdown(ctx); err != nil {
			logger.V(1).Printf("Unable to stop logFilter: %s", err.Error())
		}

		return fmt.Errorf("start log filter: %w", err)
	}

	if r.isFromService {
		r.startedComponents = append(r.startedComponents, logFilter)
	} else {
		pipeline.startedComponents = append(pipeline.startedComponents, logFilter)
	}

	r.logConsumer = logFilter

	return nil
}

func (r *logReceiver) diagnosticInfo() receiverDiagnosticInformation {
	info := receiverDiagnosticInformation{
		LogProcessedCount:      r.logCounter.Load(),
		LogThroughputPerMinute: r.throughputMeter.Total(),
		FileLogReceiverPaths:   []string{},
		ExecLogReceiverPaths:   []string{},
		IgnoredFilePaths:       []string{},
	}

	r.l.Lock()
	defer r.l.Unlock()

	for logFile, kind := range r.watching {
		switch kind {
		case logsource.ReceiverFileLog:
			info.FileLogReceiverPaths = append(info.FileLogReceiverPaths, logFile)
		case logsource.ReceiverExecLog:
			info.ExecLogReceiverPaths = append(info.ExecLogReceiverPaths, logFile)
		default:
			logger.V(1).Printf("Unknown log receiver kind %q for file %q", kind, logFile)
		}
	}

FilesFromConfig:
	for _, logFilePattern := range r.include {
		if strings.ContainsRune(logFilePattern, '*') {
			for watching := range r.watching {
				// Since the pattern is known to be valid, we can safely ignore this error.
				if matches, _ := doublestar.PathMatch(logFilePattern, watching); matches {
					// We're currently watching a file that matches this pattern, so it isn't ignored.
					continue FilesFromConfig
				}
			}

			// The pattern matched no file being watched; it is thus unused.
			info.IgnoredFilePaths = append(info.IgnoredFilePaths, logFilePattern)
		} else {
			if _, found := r.watching[logFilePattern]; !found {
				info.IgnoredFilePaths = append(info.IgnoredFilePaths, logFilePattern)
			}
		}
	}

	return info
}
