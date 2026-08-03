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
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/logsource"
	"github.com/bleemeo/glouton/utils/hostrootsymlink"

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

	rawOps, err := expandOperators(fields.Operators, knownLogFormats, false)
	if err != nil {
		return nil, nil, fmt.Errorf("expanding operators: %w", err) //nolint: nilnil
	}

	operators, err := buildOperators(rawOps)
	if err != nil {
		return nil, nil, fmt.Errorf("building operators: %w", err) //nolint: nilnil
	}

	if fields.LogFormat != "" {
		opsGroup, found := knownLogFormats[fields.LogFormat]
		if !found {
			logger.V(1).Printf("Log receiver %q requires the log format %q, which is not defined", name, fields.LogFormat)
		} else {
			// Operators from known log formats have already been expanded.
			referencedOps, err := buildOperators(opsGroup)
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
// to make clear its lock must be held during the call.
func (r *logReceiver) update(ctx context.Context, pipeline *pipelineContext, addWarnings func(...error)) error {
	r.l.Lock()
	defer r.l.Unlock()

	hasHostRoot := len(pipeline.hostroot) > len(string(os.PathSeparator))
	logFiles := make(map[string]bool, len(r.include))

	for _, filePattern := range r.include {
		matching, err := doublestar.FilepathGlob(
			filepath.Join(pipeline.hostroot, filePattern),
			doublestar.WithFilesOnly(),
			doublestar.WithFailOnIOErrors(),
		)
		if err != nil {
			if errors.Is(err, doublestar.ErrBadPattern) {
				addWarnings(errorf("Log receiver %q: file %q: %w", r.name, filePattern, err))

				continue // ignoring this file
			}

			if errors.Is(err, fs.ErrPermission) {
				if hasHostRoot {
					// We don't support execlogreceiver from a container
					addWarnings(errorf("Log receiver %q: resolving file %q: %w (ignoring it)", r.name, filePattern, err))

					continue // ignoring this file
				}

				if strings.Contains(filePattern, "*") {
					if unwrapped := errors.Unwrap(err); unwrapped != nil {
						// Getting rid of the operation that failed (stat, open, ...)
						// to only show the actual error (e.g. "permission denied").
						err = unwrapped
					}

					addWarnings(errorf(
						"Log receiver %q: resolving file pattern %q: %w (ignoring it)\n%s",
						r.name, filePattern, err,
						"(Note that Glouton may be able to read protected log file using sudo tail, but you need to use explicit path (no glob pattern).)",
					))

					continue // ignoring this pattern
				}
				// We still have a chance to handle it with sudo commands.
				matching = []string{filePattern}
			} else {
				logger.V(1).Printf("Log receiver %q: file %q: %v", r.name, filePattern, err)

				continue // ignoring this file
			}
		} else if hasHostRoot {
			// Dropping the hostroot from each log file path, if necessary.
			// We'll re-add it only where it is needed (stat, tail, ...)
			for i, logFile := range matching {
				matching[i] = strings.TrimPrefix(logFile, pipeline.hostroot)
			}
		}

		for _, file := range matching {
			realFile := file
			// Resolve symlinks relative to hostroot (Kubernetes/containerd's /var/log/containers/XXX -> /var/log/pods/XXX).
			if pipeline.hostroot != "/" {
				realFile = hostrootsymlink.EvalSymlinks(pipeline.hostroot, realFile)
			}

			// Skip if already watching or already matched by another pattern.
			if _, found := r.watching[realFile]; !found && !logFiles[realFile] {
				logFiles[realFile] = true
			}
		}
	}

	if len(logFiles) == 0 {
		return nil
	}

	makeStorageFn := func(logFile string) *component.ID {
		id := pipeline.persister.NewPersistentExt(r.name + metadataKeySeparator + logFile)

		r.registeredExtensions = append(r.registeredExtensions, id)

		return &id
	}

	fileLogReceiverFactories, readFiles, execFiles, sizeFnByFile, err := logsource.SetupLogReceiverFactories(
		slices.Collect(maps.Keys(logFiles)),
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
		return fmt.Errorf("setting up receiver factories: %w", err)
	}

	if !r.setupFilterDone {
		r.setupFilterDone = true

		err = r.setupFilters(ctx, pipeline)
		if err != nil {
			return err
		}
	}

	for logReceiverFactory, logReceiverCfg := range fileLogReceiverFactories {
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

			return fmt.Errorf("start receiver: %w", err)
		}

		if r.isFromService {
			// Store in r.startedComponents (not pipeline) to find and stop it when the service disappears.
			r.startedComponents = append(r.startedComponents, logRcvr)
		} else {
			pipeline.startedComponents = append(pipeline.startedComponents, logRcvr)
		}
	}

	for _, logFile := range readFiles {
		r.watching[logFile] = logsource.ReceiverFileLog
	}

	for _, logFile := range execFiles {
		r.watching[logFile] = logsource.ReceiverExecLog
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
			if errors.Is(err, fs.ErrNotExist) {
				// We may not catch errors produced by the "sudo stat" cmd,
				// but this would not really be convenient ...
				continue
			}

			return nil, err
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
