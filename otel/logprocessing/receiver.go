// Copyright 2015-2024 Bleemeo
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
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/execlogreceiver"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/go-viper/mapstructure/v2"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/filelogreceiver"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/receiver"
	"gopkg.in/yaml.v3"
)

// Since github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal/consumerretry is internal,
// we recreate its config type and mapstructure.Decode() it into the receivers' options.
var retryCfg = struct { //nolint:gochecknoglobals
	Enabled         bool          `mapstructure:"enabled"`
	InitialInterval time.Duration `mapstructure:"initial_interval"`
	MaxInterval     time.Duration `mapstructure:"max_interval"`
	MaxElapsedTime  time.Duration `mapstructure:"max_elapsed_time"`
}{
	Enabled:         true,
	InitialInterval: 1 * time.Second,  // default value
	MaxInterval:     30 * time.Second, // default value
	MaxElapsedTime:  1 * time.Hour,
}

type consumerKind string

const (
	consumerFileLog consumerKind = "filelogreceiver"
	consumerExecLog consumerKind = "execlogreceiver"
)

type logReceiver struct {
	cfg         config.OTLPReceiver
	logConsumer processor.Logs
	operators   []operator.Config

	// l should always be acquired after the pipeline lock
	l        sync.Mutex
	watching map[string]consumerKind

	logCounter      *atomic.Int64
	throughputMeter *ringCounter
}

func newLogReceiver(cfg config.OTLPReceiver, logConsumer processor.Logs) (*logReceiver, error) {
	var ops []operator.Config

	if err := yaml.Unmarshal([]byte(cfg.OperatorsYAML), &ops); err != nil {
		return nil, fmt.Errorf("invalid receiver operators: %w", err)
	}

	return &logReceiver{
		cfg:             cfg,
		logConsumer:     logConsumer,
		operators:       ops,
		watching:        make(map[string]consumerKind, len(cfg.Include)),
		logCounter:      new(atomic.Int64),
		throughputMeter: newRingCounter(throughputMeterResolutionSecs),
	}, nil
}

// update tries to create a log receiver for each file from the config
// that hasn't been handled yet.
//
// Passing the pipelineContext at each call rather than storing it in logReceiver
// makes explicit the fact that its lock must be acquired during the call to update().
func (r *logReceiver) update(ctx context.Context, pipeline *pipelineContext) error {
	r.l.Lock()
	defer r.l.Unlock()

	logFiles := make([]string, 0, len(r.cfg.Include))

	for _, filePattern := range r.cfg.Include {
		matching, err := doublestar.FilepathGlob(filePattern, doublestar.WithFailOnIOErrors())
		if err != nil {
			return fmt.Errorf("file %q: %w", filePattern, err)
		}

		for _, file := range matching {
			if _, found := r.watching[file]; !found {
				logFiles = append(logFiles, file)
			}
		}
	}

	if len(logFiles) == 0 {
		return nil
	}

	fileLogReceiverFactories, readFiles, execFiles, err := setupLogReceiverFactories(logFiles, r.operators, pipeline.lastFileSizes, pipeline.commandRunner)
	if err != nil {
		return fmt.Errorf("setting up receiver factories: %w", err)
	}

	for logReceiverFactory, logReceiverCfg := range fileLogReceiverFactories {
		logRcvr, err := logReceiverFactory.CreateLogs(
			ctx,
			receiver.Settings{TelemetrySettings: pipeline.telemetry},
			logReceiverCfg,
			wrapWithCounters(r.logConsumer, r.logCounter, r.throughputMeter),
		)
		if err != nil {
			return fmt.Errorf("setup receiver: %w", err)
		}

		if err = logRcvr.Start(ctx, nil); err != nil {
			return fmt.Errorf("start receiver: %w", err)
		}

		pipeline.startedComponents = append(pipeline.startedComponents, logRcvr)
	}

	for _, logFile := range readFiles {
		r.watching[logFile] = consumerFileLog
	}

	for _, logFile := range execFiles {
		r.watching[logFile] = consumerExecLog
	}

	return nil
}

// currentlyWatching returns the list of files that are being processed.
func (r *logReceiver) currentlyWatching() []string {
	r.l.Lock()
	defer r.l.Unlock()

	return slices.Collect(maps.Keys(r.watching))
}

func (r *logReceiver) diagnosticInfo() receiverDiagnosticInformation {
	info := receiverDiagnosticInformation{
		LogProcessedCount:      r.logCounter.Load(),
		LogThroughputPerMinute: r.throughputMeter.Total(),
		FileLogReceiverPaths:   []string{},
		ExecLogReceiverPaths:   []string{},
		IgnoredFilePaths:       make([]string, 0, len(r.cfg.Include)-len(r.watching)),
	}

	r.l.Lock()
	defer r.l.Unlock()

	for logFile, kind := range r.watching {
		switch kind {
		case consumerFileLog:
			info.FileLogReceiverPaths = append(info.FileLogReceiverPaths, logFile)
		case consumerExecLog:
			info.ExecLogReceiverPaths = append(info.ExecLogReceiverPaths, logFile)
		default:
			logger.V(1).Printf("Unknown log receiver kind %q", kind)
		}
	}

FilesFromConfig:
	for _, logFilePattern := range r.cfg.Include {
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

// setupLogReceiverFactories builds receiver factories for the given log files,
// accordingly to whether the file is directly readable or not.
// Files that don't exist at the time of the call to this function will be ignored.
func setupLogReceiverFactories(logFiles []string, operators []operator.Config, lastFileSizes map[string]int64, commandRunner *gloutonexec.Runner) (
	factories map[receiver.Factory]component.Config,
	readableFiles, execFiles []string,
	err error,
) {
	sizeByFile := make(map[string]int64, len(logFiles))

	for _, logFile := range logFiles {
		stat, err := os.Stat(logFile)
		if err != nil {
			logger.V(1).Printf("Can't stat log file %q (ignoring it): %v", logFile, err)

			continue
		}

		f, err := os.OpenFile(logFile, os.O_RDONLY, 0) // the mode perm isn't needed for read
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			execFiles = append(execFiles, logFile) // assuming the error will not occur using "sudo tail ..."
		} else {
			err = f.Close()
			if err != nil {
				logger.V(1).Printf("Failed to close log file %q: %v", logFile, err)
			}

			readableFiles = append(readableFiles, logFile)
		}

		sizeByFile[logFile] = stat.Size()
	}

	factories = make(map[receiver.Factory]component.Config, len(readableFiles)+len(execFiles))

	for _, logFile := range readableFiles {
		factory := filelogreceiver.NewFactory()
		fileCfg := factory.CreateDefaultConfig()

		fileTypedCfg, ok := fileCfg.(*filelogreceiver.FileLogConfig)
		if !ok {
			return nil, nil, nil, fmt.Errorf("%w for file log receiver: %T", errUnexpectedType, fileCfg)
		}

		fileTypedCfg.InputConfig.Include = []string{logFile}
		fileTypedCfg.Operators = operators

		if lastSize, ok := lastFileSizes[logFile]; ok {
			if lastSize > sizeByFile[logFile] {
				fileTypedCfg.InputConfig.StartAt = "beginning"

				logger.Printf("Start to read file %q from beginning", logFile) // TODO: remove
			}
		}

		err = mapstructure.Decode(retryCfg, &fileTypedCfg.RetryOnFailure)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to define consumerretry config for filelogreceiver: %w", err)
		}

		factories[factory] = fileTypedCfg

		logger.Printf("Chose file log receiver for file(s) %v", readableFiles) // TODO: remove
	}

	for _, logFile := range execFiles {
		factory := execlogreceiver.NewFactory()
		execCfg := factory.CreateDefaultConfig()

		execTypedCfg, ok := execCfg.(*execlogreceiver.ExecLogConfig)
		if !ok {
			return nil, nil, nil, fmt.Errorf("%w for exec log receiver: %T", errUnexpectedType, execCfg)
		}

		tailArgs := []string{"tail", "--follow=name"}

		if lastSize, ok := lastFileSizes[logFile]; ok {
			if lastSize > sizeByFile[logFile] {
				tailArgs = append(tailArgs, "--bytes=+0") // +0 -> start at byte 0
			} else if sizeByFile[logFile] > lastSize {
				tailArgs = append(tailArgs, fmt.Sprintf("--bytes=%d", sizeByFile[logFile]-lastSize)) // start '%d' bytes before the end
			}
		}

		execTypedCfg.InputConfig.Argv = append(tailArgs, logFile) //nolint: gocritic
		execTypedCfg.InputConfig.CommandRunner = commandRunner
		execTypedCfg.InputConfig.RunAsRoot = true
		execTypedCfg.Operators = operators

		logger.Printf("Tail command for file %q: %v", logFile, execTypedCfg.InputConfig.Argv) // TODO: remove

		err = mapstructure.Decode(retryCfg, &execTypedCfg.RetryOnFailure)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to define consumerretry config for execlogreceiver: %w", err)
		}

		factories[factory] = execTypedCfg

		logger.Printf("Chose exec log receiver for file %q", logFile) // TODO: remove
	}

	return factories, readableFiles, execFiles, nil
}

func wrapWithCounters(next consumer.Logs, counter *atomic.Int64, throughputMeter *ringCounter) consumer.Logs {
	logCounter, err := consumer.NewLogs(func(ctx context.Context, ld plog.Logs) error {
		count := ld.LogRecordCount()
		counter.Add(int64(count))
		throughputMeter.Add(count)

		return next.ConsumeLogs(ctx, ld)
	})
	if err != nil {
		logger.V(1).Printf("Failed to wrap component with log counters: %v", err)

		return next // give up wrapping it and just use it as is
	}

	return logCounter
}
