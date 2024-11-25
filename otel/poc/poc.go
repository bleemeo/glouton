// Copyright 2015-2023 Bleemeo
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

package poc

import (
	"context"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/execlogreceiver"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configtelemetry"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	batchprocessor "go.opentelemetry.io/collector/processor/batchprocessor"
	"go.opentelemetry.io/collector/receiver"
	otlpreceiver "go.opentelemetry.io/collector/receiver/otlpreceiver"
	"go.opentelemetry.io/otel/metric"
	noopM "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
	"gopkg.in/yaml.v3"
)

func MakePipeline(ctx context.Context, cfg config.POC, f func(context.Context, []byte) error) error {
	// TODO: no shutdown
	// TODO: no logs & metrics at the same time
	// TODO: probably issue when configuration is reloaded (might be fixed when shutdown is correctly implemented)
	// TODO: buffering / back pressure not implemented (we don't send the ErrBackPressureSignal back to client that send us logs)
	var err error

	factoryReceiver := otlpreceiver.NewFactory()
	factoryBatch := batchprocessor.NewFactory()

	telemetry := component.TelemetrySettings{
		Logger:               logger.ZapLogger(),
		TracerProvider:       noop.NewTracerProvider(),
		MeterProvider:        noopM.NewMeterProvider(),
		LeveledMeterProvider: func(_ configtelemetry.Level) metric.MeterProvider { return noopM.NewMeterProvider() },
		MetricsLevel:         configtelemetry.LevelBasic,
		Resource:             pcommon.NewResource(),
	}

	logExporter, err := exporterhelper.NewLogs(
		ctx,
		exporter.Settings{TelemetrySettings: telemetry},
		"unused",
		func(ctx context.Context, ld plog.Logs) error {
			_ = ctx

			var wtf plog.ProtoMarshaler

			b, err := wtf.MarshalLogs(ld)
			if err != nil {
				return err
			}

			if err := f(ctx, b); err != nil {
				logger.Printf("TODO: error: %v", err)
				// returning error goes nowhere (not visible anywhere), that why we do the log
				return err
			}

			return nil
		},
	)
	if err != nil {
		return err
	}

	if err := logExporter.Start(ctx, nil); err != nil {
		return err
	}

	logBatcher, err := factoryBatch.CreateLogs(
		ctx,
		processor.Settings{TelemetrySettings: telemetry},
		factoryBatch.CreateDefaultConfig(),
		logExporter,
	)
	if err != nil {
		return err
	}

	if err := logBatcher.Start(ctx, nil); err != nil {
		return err
	}

	expCfg := factoryReceiver.CreateDefaultConfig().(*otlpreceiver.Config) //nolint: forcetypeassert // TODO type assertion check
	expCfg.Protocols.GRPC.NetAddr.Endpoint = cfg.GRPCAddress               // TODO: config
	// expCfg.Protocols.HTTP.Endpoint = "localhost:4318"

	otlpLogReceiver, err := factoryReceiver.CreateLogs(
		ctx,
		receiver.Settings{TelemetrySettings: telemetry},
		expCfg,
		logBatcher,
	)
	if err != nil {
		return err
	}

	if err := otlpLogReceiver.Start(ctx, nil); err != nil {
		return err
	}

	if len(cfg.LogFile) > 0 {
		// TODO: use filelogreceiver if Glouton had access to the file
		factoryExecLog := execlogreceiver.NewFactory()
		execCfg := factoryExecLog.CreateDefaultConfig().(*execlogreceiver.ExecLogConfig) //nolint: forcetypeassert // TODO type assertion check
		execCfg.InputConfig.Argv = []string{"tail", "-f", cfg.LogFile[0]}

		var ops []operator.Config

		if err := yaml.Unmarshal([]byte(cfg.OperatorsYAML), &ops); err != nil {
			return err
		}

		execCfg.Operators = ops

		filelogReceiver, err := factoryExecLog.CreateLogs(
			ctx,
			receiver.Settings{TelemetrySettings: telemetry},
			execCfg,
			logBatcher,
		)
		if err != nil {
			return err
		}

		if err := filelogReceiver.Start(ctx, nil); err != nil {
			return err
		}
	}

	return nil
}
