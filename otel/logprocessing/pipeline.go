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
	"net"
	"os"
	"slices"
	"strconv"
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
	"go.opentelemetry.io/otel/metric"
	noopM "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
	"gopkg.in/yaml.v3"
)

const (
	throughputMeterResolutionSecs = 60
	shutdownTimeout               = 5 * time.Second
)

var errUnexpectedType = errors.New("unexpected type")

func MakePipeline(
	ctx context.Context,
	cfg config.OpenTelemetry,
	pushLogs func(context.Context, []byte) error,
	applyBackPressureFn func() bool,
) (diagnosticFn func(context.Context, types.ArchiveWriter) error, err error) {
	// TODO: no logs & metrics at the same time

	// startedComponents represents all the components that must be shut down at the end of the context's lifecycle.
	startedComponents := make([]component.Component, 0, 4) // 4 should be the minimum number of components

	telemetry := component.TelemetrySettings{
		Logger:               logger.ZapLogger(),
		TracerProvider:       noop.NewTracerProvider(),
		MeterProvider:        noopM.NewMeterProvider(),
		LeveledMeterProvider: func(_ configtelemetry.Level) metric.MeterProvider { return noopM.NewMeterProvider() },
		MetricsLevel:         configtelemetry.LevelBasic,
		Resource:             pcommon.NewResource(),
	}

	var logProcessedCount atomic.Int64

	// We want to measure the throughput over the last minute (60s)
	logThroughputMeter := newRingCounter(throughputMeterResolutionSecs)

	logExporter, err := exporterhelper.NewLogs(
		ctx,
		exporter.Settings{TelemetrySettings: telemetry},
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
			logProcessedCount.Add(int64(count))
			logThroughputMeter.Inc(count)

			return nil
		},
	)
	if err != nil {
		shutdownAll(startedComponents)

		return nil, fmt.Errorf("setup exporter: %w", err)
	}

	if err = logExporter.Start(ctx, nil); err != nil {
		shutdownAll(startedComponents)

		return nil, fmt.Errorf("start exporter: %w", err)
	}

	startedComponents = append(startedComponents, logExporter)

	factoryBatch := batchprocessor.NewFactory()

	logBatcher, err := factoryBatch.CreateLogs(
		ctx,
		processor.Settings{TelemetrySettings: telemetry},
		factoryBatch.CreateDefaultConfig(),
		logExporter,
	)
	if err != nil {
		shutdownAll(startedComponents)

		return nil, fmt.Errorf("setup batcher: %w", err)
	}

	if err = logBatcher.Start(ctx, nil); err != nil {
		shutdownAll(startedComponents)

		return nil, fmt.Errorf("start batcher: %w", err)
	}

	logBackPressureEnforcer, err := processorhelper.NewLogs(
		ctx,
		processor.Settings{TelemetrySettings: telemetry},
		nil,
		logBatcher,
		makeEnforceBackPressureFn(applyBackPressureFn),
	)
	if err != nil {
		return nil, fmt.Errorf("setup log back-pressure enforcer: %w", err)
	}

	startedComponents = append(startedComponents, logBatcher)

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
			receiver.Settings{TelemetrySettings: telemetry},
			receiverTypedCfg,
			logBackPressureEnforcer,
		)
		if err != nil {
			shutdownAll(startedComponents)

			return nil, fmt.Errorf("setup OTLP receiver: %w", err)
		}

		if err = otlpLogReceiver.Start(ctx, nil); err != nil {
			shutdownAll(startedComponents)

			return nil, fmt.Errorf("start OTLP receiver: %w", err)
		}

		startedComponents = append(startedComponents, otlpLogReceiver)
	}

	receiversInfo := make([]receiverDiagnosticInformation, len(cfg.Receivers))

	for i, rcvr := range cfg.Receivers {
		fileLogReceiverFactories, readFiles, execFiles, ignoredFiles, err := setupLogReceiverFactories(rcvr.Include, []byte(rcvr.OperatorsYAML))
		if err != nil {
			shutdownAll(startedComponents)

			return nil, fmt.Errorf("setup log receiver n°%d factories: %w", i+1, err)
		}

		logCounter := marshallableInt64{new(atomic.Int64)}
		throughputMeter := newRingCounter(throughputMeterResolutionSecs)

		for logReceiverFactory, logReceiverCfg := range fileLogReceiverFactories {
			logReceiver, err := logReceiverFactory.CreateLogs(
				ctx,
				receiver.Settings{TelemetrySettings: telemetry},
				logReceiverCfg,
				wrapWithCounters(ctx, logBackPressureEnforcer, logCounter.Int64, throughputMeter, telemetry),
			)
			if err != nil {
				shutdownAll(startedComponents)

				return nil, fmt.Errorf("setup log receiver n°%d: %w", i+1, err)
			}

			if err = logReceiver.Start(ctx, nil); err != nil {
				shutdownAll(startedComponents)

				return nil, fmt.Errorf("start log receiver n°%d: %w", i+1, err)
			}

			startedComponents = append(startedComponents, logReceiver)
		}

		receiversInfo[i] = receiverDiagnosticInformation{
			LogProcessedCount:      logCounter,
			LogThroughputPerMinute: throughputMeter,
			FileLogReceiverPaths:   readFiles,
			ExecLogReceiverPaths:   execFiles,
			IgnoredLogPaths:        ignoredFiles,
		}
	}

	// Scheduling the shutdown of all the components we've started
	go func() {
		defer crashreport.ProcessPanic()

		<-ctx.Done()

		shutdownAll(startedComponents)
	}()

	diagnosticFn = func(_ context.Context, writer types.ArchiveWriter) error {
		diagInfo := diagnosticInformation{
			LogProcessedCount:      logProcessedCount.Load(),
			LogThroughputPerMinute: logThroughputMeter.Total(),
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

// setupLogReceiverFactories builds receiver factories for the given log files,
// accordingly to whether the file is directly readable or not.
// Files that don't exist at the time of the call to this function will be ignored.
func setupLogReceiverFactories(logFiles []string, operatorsYaml []byte) (
	factories map[receiver.Factory]component.Config,
	readableFiles, execFiles, ignoredFiles []string,
	err error,
) {
	var ops []operator.Config

	if err := yaml.Unmarshal(operatorsYaml, &ops); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("log receiver operators: %w", err)
	}

	for _, logFile := range logFiles {
		f, err := os.OpenFile(logFile, os.O_RDONLY, 0) // the mode perm isn't needed for read
		if err != nil {
			if os.IsNotExist(err) {
				logger.V(0).Printf("Log file %q does not exist, ignoring it.", logFile) // TODO: V(2)

				ignoredFiles = append(ignoredFiles, logFile)

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

	return factories, readableFiles, execFiles, ignoredFiles, nil
}

func wrapWithCounters(ctx context.Context, next consumer.Logs, counter *atomic.Int64, throughputMeter *ringCounter, telemetry component.TelemetrySettings) consumer.Logs {
	logCounter, err := processorhelper.NewLogs(
		ctx,
		processor.Settings{TelemetrySettings: telemetry},
		nil,
		next,
		func(_ context.Context, logs plog.Logs) (plog.Logs, error) {
			count := logs.LogRecordCount()
			counter.Add(int64(count))
			throughputMeter.Inc(count)

			return logs, nil
		},
	)
	if err != nil {
		logger.V(1).Printf("Failed to wrap component with log counters: %v", err)

		return next // give up wrapping it and just use it like so
	}

	return logCounter
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
	LogProcessedCount      marshallableInt64
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
