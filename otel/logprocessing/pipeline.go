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
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/execlogreceiver"
	"github.com/bleemeo/glouton/types"

	"github.com/go-viper/mapstructure/v2"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/filelogreceiver"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configtelemetry"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/batchprocessor"
	"go.opentelemetry.io/collector/processor/processorhelper"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/otlpreceiver"
	noopM "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
	"gopkg.in/yaml.v3"
)

const (
	throughputMeterResolutionSecs = 60
	retrySetupFileReceiversPeriod = 1 * time.Minute
	shutdownTimeout               = 5 * time.Second
)

var errUnexpectedType = errors.New("unexpected type")

type pipelineContext struct {
	config    config.OpenTelemetry
	telemetry component.TelemetrySettings

	l sync.Mutex
	// startedComponents represents all the components that must be shut down at the end of the context's lifecycle.
	startedComponents []component.Component
	// receiversToRetry is a map[receiver index] -> files not handled yet
	receiversToRetry           map[int][]string
	logCounterPerReceiver      map[int]*marshallableInt64
	throughputMeterPerReceiver map[int]*ringCounter
	receiversInfo              map[int]receiverDiagnosticInformation

	logProcessedCount  atomic.Int64
	logThroughputMeter *ringCounter
}

func MakePipeline( //nolint:maintidx
	ctx context.Context,
	cfg config.OpenTelemetry,
	pushLogs func(context.Context, []byte) error,
	applyBackPressureFn func() bool,
) (diagnosticFn func(context.Context, types.ArchiveWriter) error, err error) {
	// TODO: no logs & metrics at the same time

	pipeline := &pipelineContext{
		config: cfg,
		telemetry: component.TelemetrySettings{
			Logger:         logger.ZapLogger(),
			TracerProvider: noop.NewTracerProvider(),
			MeterProvider:  noopM.NewMeterProvider(),
			MetricsLevel:   configtelemetry.LevelBasic,
			Resource:       pcommon.NewResource(),
		},
		startedComponents:          make([]component.Component, 0, 4), // 4 should be the minimum number of components
		receiversToRetry:           make(map[int][]string),
		logCounterPerReceiver:      make(map[int]*marshallableInt64),
		throughputMeterPerReceiver: make(map[int]*ringCounter),
		receiversInfo:              make(map[int]receiverDiagnosticInformation, len(cfg.Receivers)),
		logThroughputMeter:         newRingCounter(throughputMeterResolutionSecs), // we want to measure the throughput over the last minute (60s)
	}

	pipeline.l.Lock()
	defer pipeline.l.Unlock()

	logExporter, err := exporterhelper.NewLogs(
		ctx,
		exporter.Settings{TelemetrySettings: pipeline.telemetry},
		"unused",
		func(ctx context.Context, ld plog.Logs) error {
			b, err := new(plog.ProtoMarshaler).MarshalLogs(ld)
			if err != nil {
				return err
			}

			if err = pushLogs(ctx, b); err != nil {
				logger.V(1).Printf("Failed to push logs: %v", err)
				// returning error goes nowhere (not visible anywhere), that why we do the log
				return err
			}

			count := ld.LogRecordCount()
			pipeline.logProcessedCount.Add(int64(count))
			pipeline.logThroughputMeter.Add(count)

			return nil
		},
	)
	if err != nil {
		shutdownAll(pipeline.startedComponents)

		return nil, fmt.Errorf("setup exporter: %w", err)
	}

	if err = logExporter.Start(ctx, nil); err != nil {
		shutdownAll(pipeline.startedComponents)

		return nil, fmt.Errorf("start exporter: %w", err)
	}

	pipeline.startedComponents = append(pipeline.startedComponents, logExporter)

	factoryBatch := batchprocessor.NewFactory()

	logBatcher, err := factoryBatch.CreateLogs(
		ctx,
		processor.Settings{TelemetrySettings: pipeline.telemetry},
		factoryBatch.CreateDefaultConfig(),
		logExporter,
	)
	if err != nil {
		shutdownAll(pipeline.startedComponents)

		return nil, fmt.Errorf("setup batcher: %w", err)
	}

	if err = logBatcher.Start(ctx, nil); err != nil {
		shutdownAll(pipeline.startedComponents)

		return nil, fmt.Errorf("start batcher: %w", err)
	}

	logBackPressureEnforcer, err := processorhelper.NewLogs(
		ctx,
		processor.Settings{TelemetrySettings: pipeline.telemetry},
		nil,
		logBatcher,
		makeEnforceBackPressureFn(applyBackPressureFn),
	)
	if err != nil {
		return nil, fmt.Errorf("setup log back-pressure enforcer: %w", err)
	}

	pipeline.startedComponents = append(pipeline.startedComponents, logBatcher)

	if cfg.GRPC.Enable || cfg.HTTP.Enable {
		factoryReceiver := otlpreceiver.NewFactory()
		receiverCfg := factoryReceiver.CreateDefaultConfig()

		receiverTypedCfg, ok := receiverCfg.(*otlpreceiver.Config)
		if !ok {
			return nil, fmt.Errorf("%w for receiver default config: %T", errUnexpectedType, receiverCfg)
		}

		if cfg.GRPC.Enable {
			receiverTypedCfg.Protocols.GRPC.NetAddr.Endpoint = net.JoinHostPort(cfg.GRPC.Address, strconv.Itoa(cfg.GRPC.Port))
		} else {
			receiverTypedCfg.Protocols.GRPC = nil
		}

		if cfg.HTTP.Enable {
			receiverTypedCfg.Protocols.HTTP.Endpoint = net.JoinHostPort(cfg.HTTP.Address, strconv.Itoa(cfg.HTTP.Port))
		} else {
			receiverTypedCfg.Protocols.HTTP = nil
		}

		otlpLogReceiver, err := factoryReceiver.CreateLogs(
			ctx,
			receiver.Settings{TelemetrySettings: pipeline.telemetry},
			receiverTypedCfg,
			logBackPressureEnforcer,
		)
		if err != nil {
			shutdownAll(pipeline.startedComponents)

			return nil, fmt.Errorf("setup OTLP receiver: %w", err)
		}

		if err = otlpLogReceiver.Start(ctx, nil); err != nil {
			shutdownAll(pipeline.startedComponents)

			return nil, fmt.Errorf("start OTLP receiver: %w", err)
		}

		pipeline.startedComponents = append(pipeline.startedComponents, otlpLogReceiver)
	}

	for i, rcvr := range cfg.Receivers {
		pipeline.logCounterPerReceiver[i] = &marshallableInt64{new(atomic.Int64)}
		pipeline.throughputMeterPerReceiver[i] = newRingCounter(throughputMeterResolutionSecs)

		notExistFiles, err := setupLogReceivers(ctx, pipeline, logBackPressureEnforcer, i, rcvr.Include, []byte(rcvr.OperatorsYAML))
		if err != nil {
			shutdownAll(pipeline.startedComponents)

			return nil, fmt.Errorf("setup log receiver n°%d factories: %w", i+1, err)
		}

		if len(notExistFiles) > 0 {
			pipeline.receiversToRetry[i] = notExistFiles
		}
	}

	if len(pipeline.receiversToRetry) > 0 {
		go retrySetupFileReceivers(ctx, pipeline, logBackPressureEnforcer)
	}

	// Scheduling the shutdown of all the components we've started
	go func() {
		defer crashreport.ProcessPanic()

		<-ctx.Done()

		pipeline.l.Lock()
		defer pipeline.l.Unlock()

		shutdownAll(pipeline.startedComponents)
	}()

	diagnosticFn = func(_ context.Context, writer types.ArchiveWriter) error {
		pipeline.l.Lock()
		receiversInfo := slices.Collect(maps.Values(pipeline.receiversInfo))
		pipeline.l.Unlock()

		diagInfo := diagnosticInformation{
			LogProcessedCount:      pipeline.logProcessedCount.Load(),
			LogThroughputPerMinute: pipeline.logThroughputMeter.Total(),
			BackPressureBlocking:   applyBackPressureFn(),
			Receivers:              receiversInfo,
		}

		return diagInfo.writeToArchive(writer)
	}

	return diagnosticFn, nil
}

func makeEnforceBackPressureFn(shouldApplyBackPressureFn func() bool) processorhelper.ProcessLogsFunc {
	// Since shouldApplyBackPressureFn needs to acquire a lock, we want to avoid calling it too frequently.
	const cacheLifetime = 5 * time.Second

	var (
		lastCacheValue  bool
		lastCacheUpdate time.Time
	)

	applyBackPressureDebounced := func() bool {
		if time.Since(lastCacheUpdate) > cacheLifetime {
			newState := shouldApplyBackPressureFn()
			if newState != lastCacheValue {
				if newState {
					logger.V(1).Printf("Logs back-pressure blocking is now enabled") // TODO: V(2)
				} else {
					logger.V(1).Printf("Logs back-pressure blocking is now disabled") // TODO: V(2)
				}
			}

			lastCacheValue = newState
			lastCacheUpdate = time.Now()
		}

		return lastCacheValue
	}

	return func(_ context.Context, logs plog.Logs) (plog.Logs, error) {
		if applyBackPressureDebounced() {
			return logs, types.ErrBackPressureSignal
		}

		return logs, nil
	}
}

func setupLogReceivers(
	ctx context.Context,
	pipeline *pipelineContext,
	logBackPressureEnforcer consumer.Logs,
	recvIdx int,
	logFiles []string,
	operatorsYaml []byte,
) (notExistFiles []string, err error) {
	fileLogReceiverFactories, readFiles, execFiles, notExistFiles, err := setupLogReceiverFactories(logFiles, operatorsYaml)
	if err != nil {
		return nil, fmt.Errorf("setup log receiver n°%d factories: %w", recvIdx+1, err)
	}

	for logReceiverFactory, logReceiverCfg := range fileLogReceiverFactories {
		logReceiver, err := logReceiverFactory.CreateLogs(
			ctx,
			receiver.Settings{TelemetrySettings: pipeline.telemetry},
			logReceiverCfg,
			wrapWithCounters(logBackPressureEnforcer, pipeline.logCounterPerReceiver[recvIdx].Int64, pipeline.throughputMeterPerReceiver[recvIdx]),
		)
		if err != nil {
			return nil, fmt.Errorf("setup log receiver n°%d: %w", recvIdx+1, err)
		}

		if err = logReceiver.Start(ctx, nil); err != nil {
			return nil, fmt.Errorf("start log receiver n°%d: %w", recvIdx+1, err)
		}

		pipeline.startedComponents = append(pipeline.startedComponents, logReceiver)
	}

	if recvInfo, exists := pipeline.receiversInfo[recvIdx]; exists {
		recvInfo.FileLogReceiverPaths = append(recvInfo.FileLogReceiverPaths, readFiles...)
		recvInfo.ExecLogReceiverPaths = append(recvInfo.ExecLogReceiverPaths, execFiles...)
		recvInfo.IgnoredLogPaths = notExistFiles
		pipeline.receiversInfo[recvIdx] = recvInfo
	} else {
		pipeline.receiversInfo[recvIdx] = receiverDiagnosticInformation{
			LogProcessedCount:      pipeline.logCounterPerReceiver[recvIdx],
			LogThroughputPerMinute: pipeline.throughputMeterPerReceiver[recvIdx],
			FileLogReceiverPaths:   readFiles,
			ExecLogReceiverPaths:   execFiles,
			IgnoredLogPaths:        notExistFiles,
		}
	}

	return notExistFiles, nil
}

// setupLogReceiverFactories builds receiver factories for the given log files,
// accordingly to whether the file is directly readable or not.
// Files that don't exist at the time of the call to this function will be ignored.
func setupLogReceiverFactories(logFiles []string, operatorsYaml []byte) (
	factories map[receiver.Factory]component.Config,
	readableFiles, execFiles, notExistFiles []string,
	err error,
) {
	var ops []operator.Config

	if err = yaml.Unmarshal(operatorsYaml, &ops); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("log receiver operators: %w", err)
	}

	for _, logFile := range logFiles {
		if strings.Contains(logFile, "*") {
			// Assuming the "*" is in the base of the path, we check if we can list files in the parent dir.
			if f, err := os.OpenFile(filepath.Dir(logFile), os.O_RDONLY, 0); err != nil {
				logger.V(1).Printf("Unable to handle glob for log files %q", logFile)
			} else {
				_ = f.Close()

				readableFiles = append(readableFiles, logFile)
			}

			continue
		}

		f, err := os.OpenFile(logFile, os.O_RDONLY, 0) // the mode perm isn't needed for read
		if err != nil {
			if os.IsNotExist(err) {
				logger.V(0).Printf("Log file %q does not exist, will retry in %s", logFile, retrySetupFileReceiversPeriod) // TODO: V(2)

				notExistFiles = append(notExistFiles, logFile)

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
	}

	// Since github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal/consumerretry is internal,
	// we recreate its config type and mapstructure.Decode() it into the receivers' options.
	retryCfg := struct {
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

	factories = make(map[receiver.Factory]component.Config)

	if len(readableFiles) > 0 {
		factory := filelogreceiver.NewFactory()
		fileCfg := factory.CreateDefaultConfig()

		fileTypedCfg, ok := fileCfg.(*filelogreceiver.FileLogConfig)
		if !ok {
			return nil, nil, nil, nil, fmt.Errorf("%w for file log receiver: %T", errUnexpectedType, fileCfg)
		}

		fileTypedCfg.InputConfig.Include = readableFiles
		fileTypedCfg.Operators = ops

		err := mapstructure.Decode(retryCfg, &fileTypedCfg.RetryOnFailure)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to define consumerretry config for filelogreceiver: %w", err)
		}

		factories[factory] = fileTypedCfg

		logger.Printf("Chose file log receiver for file(s) %v", readableFiles) // TODO: remove
	}

	for _, logFile := range execFiles {
		factory := execlogreceiver.NewFactory()
		execCfg := factory.CreateDefaultConfig()

		execTypedCfg, ok := execCfg.(*execlogreceiver.ExecLogConfig)
		if !ok {
			return nil, nil, nil, nil, fmt.Errorf("%w for exec log receiver: %T", errUnexpectedType, execCfg)
		}

		execTypedCfg.InputConfig.Argv = []string{"sudo", "tail", "--follow=name", logFile}
		execTypedCfg.Operators = ops

		err := mapstructure.Decode(retryCfg, &execTypedCfg.RetryOnFailure)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to define consumerretry config for execlogreceiver: %w", err)
		}

		factories[factory] = execTypedCfg

		logger.Printf("Chose exec log receiver for file %q", logFile) // TODO: remove
	}

	return factories, readableFiles, execFiles, notExistFiles, nil
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

func retrySetupFileReceivers(ctx context.Context, pipeline *pipelineContext, logBackPressureEnforcer consumer.Logs) {
	ticker := time.NewTicker(retrySetupFileReceiversPeriod)
	defer ticker.Stop()

	for ctx.Err() == nil {
		var shouldReturn bool

		select {
		case <-ticker.C:
			pipeline.l.Lock()

			for recvIdx, files := range pipeline.receiversToRetry {
				operatorsYaml := pipeline.config.Receivers[recvIdx].OperatorsYAML

				notExistFiles, err := setupLogReceivers(ctx, pipeline, logBackPressureEnforcer, recvIdx, files, []byte(operatorsYaml))
				if err != nil {
					logger.V(1).Printf("Failed to %v", err)

					continue
				}

				if len(notExistFiles) == 0 {
					delete(pipeline.receiversToRetry, recvIdx)
				} else {
					pipeline.receiversToRetry[recvIdx] = notExistFiles
				}
			}

			if len(pipeline.receiversToRetry) == 0 {
				// All log files are being handled, this function has fulfilled its duty.
				shouldReturn = true
			}

			pipeline.l.Unlock()
		case <-ctx.Done():
			return
		}

		if shouldReturn {
			return
		}
	}
}

// shutdownAll stops all the given components (in reverse order).
// It should be called before every unsuccessful return of the log pipeline initialization.
func shutdownAll(components []component.Component) {
	logger.Printf("Shutting down %d log processing components: %s", len(components), formatTypes(components)) // TODO: remove

	// Shutting down first the components that are at the beginning of the log production chain.
	for _, comp := range slices.Backward(components) {
		go func() {
			defer crashreport.ProcessPanic()

			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()

			err := comp.Shutdown(shutdownCtx)
			if err != nil {
				logger.V(1).Printf("Failed to shutdown log processing component %T: %v", comp, err)
			}
		}()
	}
}

// formatTypes acts like a mix of %T and %v, on a slice.
func formatTypes[E any](a []E) string {
	result := "["

	for i, e := range a {
		result += fmt.Sprintf("%T", e)

		if i < len(a)-1 {
			result += " "
		}
	}

	return result + "]"
}

type marshallableInt64 struct {
	*atomic.Int64
}

func (x marshallableInt64) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatInt(x.Load(), 10)), nil
}

type receiverDiagnosticInformation struct {
	LogProcessedCount      *marshallableInt64
	LogThroughputPerMinute *ringCounter
	FileLogReceiverPaths   []string
	ExecLogReceiverPaths   []string
	IgnoredLogPaths        []string
}

type diagnosticInformation struct {
	LogProcessedCount      int64
	LogThroughputPerMinute int
	BackPressureBlocking   bool
	Receivers              []receiverDiagnosticInformation
}

func (diagInfo diagnosticInformation) writeToArchive(writer types.ArchiveWriter) error {
	file, err := writer.Create("log-processing.json")
	if err != nil {
		return err
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(diagInfo)
}
