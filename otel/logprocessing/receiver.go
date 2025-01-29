// Copyright 2015-2025 Bleemeo
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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/execlogreceiver"
	"github.com/bleemeo/glouton/utils/gloutonexec"
	"github.com/bleemeo/glouton/version"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/go-viper/mapstructure/v2"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/fileconsumer/attrs"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/filelogreceiver"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
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

type receiverKind string

const (
	receiverFileLog receiverKind = "filelogreceiver"
	receiverExecLog receiverKind = "execlogreceiver"
)

type logReceiver struct {
	name        string
	cfg         config.OTLPReceiver
	logConsumer consumer.Logs
	operators   []operator.Config

	// l should always be acquired after the pipeline lock
	l            sync.Mutex
	watching     map[string]receiverKind
	sizeFnByFile map[string]func() (int64, error)

	logCounter      *atomic.Int64
	throughputMeter *ringCounter
}

func newLogReceiver(name string, cfg config.OTLPReceiver, logConsumer consumer.Logs) (*logReceiver, error) {
	var ops []operator.Config

	if err := yaml.Unmarshal([]byte(cfg.OperatorsYAML), &ops); err != nil {
		return nil, fmt.Errorf("invalid receiver operators: %w", err)
	}

	return &logReceiver{
		name:            name,
		cfg:             cfg,
		logConsumer:     logConsumer,
		operators:       ops,
		watching:        make(map[string]receiverKind, len(cfg.Include)),
		sizeFnByFile:    make(map[string]func() (int64, error), len(cfg.Include)),
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

	logFiles := make(map[string]bool, len(r.cfg.Include))

	for _, filePattern := range r.cfg.Include {
		matching, err := doublestar.FilepathGlob(filePattern, doublestar.WithFailOnIOErrors())
		if err != nil {
			if errors.Is(err, doublestar.ErrBadPattern) {
				return fmt.Errorf("file %q: %w", filePattern, err)
			}

			if errors.Is(err, fs.ErrPermission) {
				if strings.Contains(filePattern, "*") {
					if unwrapped := errors.Unwrap(err); unwrapped != nil {
						// Getting rid of the operation that failed (stat, open, ...)
						// to only show the actual error (e.g. "permission denied").
						err = unwrapped
					}

					return fmt.Errorf("resolving file %q: %w - Note that Glouton could read protected log file using sudo tail, but you need to use explicit path (no glob pattern)", filePattern, err)
				}
				// We still have a chance to handle it with sudo commands.
				matching = []string{filePattern}
			} else {
				return fmt.Errorf("resolving file %q: %w", filePattern, err)
			}
		}

		for _, file := range matching {
			// Ensure we're not already watching it,
			// as well as it hasn't been matched by multiple patterns.
			if _, found := r.watching[file]; !found && !logFiles[file] {
				logFiles[file] = true
			}
		}
	}

	if len(logFiles) == 0 {
		return nil
	}

	storageID, err := pipeline.persister.newPersistentExt(ctx, r.name)
	if err != nil {
		return fmt.Errorf("could not create persister for %q: %w", r.name, err)
	}

	fileLogReceiverFactories, readFiles, execFiles, sizeFnByFile, err := setupLogReceiverFactories(
		slices.Collect(maps.Keys(logFiles)),
		r.operators,
		pipeline.lastFileSizes,
		pipeline.commandRunner,
		&storageID,
	)
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

		if err = logRcvr.Start(ctx, pipeline.persister); err != nil {
			return fmt.Errorf("start receiver: %w", err)
		}

		pipeline.startedComponents = append(pipeline.startedComponents, logRcvr)
	}

	for _, logFile := range readFiles {
		r.watching[logFile] = receiverFileLog
	}

	for _, logFile := range execFiles {
		r.watching[logFile] = receiverExecLog
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

// sizesByFile returns the size of each log file watched by this receiver.
func (r *logReceiver) sizesByFile() (map[string]int64, error) {
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
		case receiverFileLog:
			info.FileLogReceiverPaths = append(info.FileLogReceiverPaths, logFile)
		case receiverExecLog:
			info.ExecLogReceiverPaths = append(info.ExecLogReceiverPaths, logFile)
		default:
			logger.V(1).Printf("Unknown log receiver kind %q for file %q", kind, logFile)
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
func setupLogReceiverFactories(logFiles []string, operators []operator.Config, lastFileSizes map[string]int64, commandRunner CommandRunner, storageID *component.ID) (
	factories map[receiver.Factory]component.Config,
	readableFiles, execFiles []string,
	sizeFnByFile map[string]func() (int64, error),
	err error,
) {
	sizeFnByFile = make(map[string]func() (int64, error), len(logFiles))

	for _, logFile := range logFiles {
		ignore, needSudo, sizeFn := statFile(logFile, commandRunner)
		if ignore {
			continue
		}

		sizeFnByFile[logFile] = sizeFn

		if needSudo {
			execFiles = append(execFiles, logFile)
		} else {
			readableFiles = append(readableFiles, logFile)
		}
	}

	factories = make(map[receiver.Factory]component.Config, len(readableFiles)+len(execFiles))

	for _, logFile := range readableFiles {
		factory := filelogreceiver.NewFactory()
		fileCfg := factory.CreateDefaultConfig()

		fileTypedCfg, ok := fileCfg.(*filelogreceiver.FileLogConfig)
		if !ok {
			return nil, nil, nil, nil, fmt.Errorf("%w for file log receiver: %T", errUnexpectedType, fileCfg)
		}

		fileTypedCfg.InputConfig.Include = []string{logFile}
		fileTypedCfg.InputConfig.IncludeFileName = true
		fileTypedCfg.InputConfig.IncludeFilePath = true
		fileTypedCfg.Operators = operators
		fileTypedCfg.BaseConfig.StorageID = storageID

		size, err := sizeFnByFile[logFile]()
		if err != nil {
			logger.V(1).Printf("Error getting size of file %q (ignoring it): %v", logFile, err)

			continue
		}

		if lastSize, ok := lastFileSizes[logFile]; ok {
			if lastSize > size {
				fileTypedCfg.InputConfig.StartAt = "beginning" // FIXME
			}
		}

		logger.Printf("Resuming filerecv %q at %s", logFile, fileTypedCfg.InputConfig.StartAt) // TODO: remove

		err = mapstructure.Decode(retryCfg, &fileTypedCfg.RetryOnFailure)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to define consumerretry config on file log receiver: %w", err)
		}

		factories[factory] = fileTypedCfg
	}

	for _, logFile := range execFiles {
		factory := execlogreceiver.NewFactory()
		execCfg := factory.CreateDefaultConfig()

		execTypedCfg, ok := execCfg.(*execlogreceiver.ExecLogConfig)
		if !ok {
			return nil, nil, nil, nil, fmt.Errorf("%w for exec log receiver: %T", errUnexpectedType, execCfg)
		}

		size, err := sizeFnByFile[logFile]()
		if err != nil {
			logger.V(1).Printf("Error getting size of file %q (ignoring it): %v", logFile, err)

			continue
		}

		tailArgs := []string{"tail", "--follow=name"}

		if lastSize, ok := lastFileSizes[logFile]; ok {
			switch {
			case lastSize > size: // the file has been truncated
				tailArgs = append(tailArgs, "--bytes=+0") // start at the beginning of the file
			case size == lastSize: // the file hasn't changed
				tailArgs = append(tailArgs, "--bytes=0") // start at the end of the file
			case size > lastSize: // the file has been written since the last time
				tailArgs = append(tailArgs, fmt.Sprintf("--bytes=+%d", lastSize)) // start at byte 'lastSize'
			}
		}

		logger.Printf("Resuming execrecv %q at %s", logFile, tailArgs[len(tailArgs)-1]) // TODO: remove

		execTypedCfg.InputConfig.Argv = append(tailArgs, logFile) //nolint: gocritic
		execTypedCfg.InputConfig.CommandRunner = commandRunner
		execTypedCfg.InputConfig.RunAsRoot = true
		execTypedCfg.InputConfig.Attributes = map[string]helper.ExprStringConfig{
			attrs.LogFileName: helper.ExprStringConfig(filepath.Base(logFile)),
			attrs.LogFilePath: helper.ExprStringConfig(logFile),
		}
		execTypedCfg.Operators = operators

		err = mapstructure.Decode(retryCfg, &execTypedCfg.RetryOnFailure)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to define consumerretry config on exec log receiver: %w", err)
		}

		factories[factory] = execTypedCfg
	}

	return factories, readableFiles, execFiles, sizeFnByFile, nil
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

// Using statFile instead of the function allows us to mock it during tests.
var statFile = statFileImpl //nolint:gochecknoglobals

func statFileImpl(logFile string, commandRunner CommandRunner) (ignore, needSudo bool, sizeFn func() (int64, error)) {
	f, err := os.OpenFile(logFile, os.O_RDONLY, 0) // the mode perm isn't needed for read
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return true, false, nil
		}

		if !errors.Is(err, fs.ErrPermission) {
			logger.V(1).Printf("Failed to open log file %q (ignoring it): %v", logFile, err)

			return true, false, nil
		}

		if version.IsWindows() {
			logger.V(1).Printf("Can't open protected log file on Windows, ignoring %q.", logFile)

			return true, false, nil
		}

		if _, err = sudoStatFile(logFile, commandRunner); err != nil {
			logger.V(1).Printf("Can't `sudo stat` log file %q (ignoring it): %v", logFile, err)

			return true, false, nil
		}

		needSudo = true
		sizeFn = func() (int64, error) {
			statOutput, err := sudoStatFile(logFile, commandRunner)
			if err != nil {
				return 0, err
			}

			size, err := strconv.ParseInt(string(statOutput), 10, 64)
			if err != nil {
				return 0, fmt.Errorf("unexpected stat output %q: %w", statOutput, err)
			}

			return size, nil
		}
	} else {
		err = f.Close()
		if err != nil {
			logger.V(1).Printf("Failed to close log file %q: %v", logFile, err)
		}

		needSudo = false
		sizeFn = func() (int64, error) {
			stat, err := os.Stat(logFile)
			if err != nil {
				return 0, err
			}

			return stat.Size(), nil
		}
	}

	return false, needSudo, sizeFn
}

// sudoStatFile executes a `sudo stat` on the given file and returns its (trimmed) output.
func sudoStatFile(logFile string, commandRunner CommandRunner) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	runOpt := gloutonexec.Option{
		RunAsRoot:      true,
		CombinedOutput: true,
	}

	out, err := commandRunner.Run(ctx, runOpt, "stat", "--printf=%s", logFile)
	trimmedOutput := bytes.TrimSpace(out)

	if err != nil {
		strOut := string(trimmedOutput)
		if strOut != "" {
			strOut = ": " + strOut
		}

		return nil, fmt.Errorf("%w%s", err, strOut)
	}

	return trimmedOutput, nil
}
