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
	"fmt"
	"sync/atomic"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/logsource"

	"github.com/go-viper/mapstructure/v2"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/filterprocessor"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

// fanoutSink is what backs a WantSource sink for a logsource.ReceiverManager
// -owned source (a config receiver or a container-label source): its own
// filter stage, instrumented for diagnostics, feeding into the shared
// pipeline (pipeline.go).
type fanoutSink struct {
	kind            logsource.SourceKind
	logCounter      *atomic.Int64
	throughputMeter *logsource.RingCounter
}

// WantSource implements logsource.SinkProvider: this package wants a
// logsource.ReceiverManager-owned source iff its fully-resolved SendLogs
// decision is true (ReceiverManager already combined the receiver/container's
// own send_logs with OpenTelemetry.SendLogs/glouton.log_enable -- WantSource
// doesn't re-derive it), in which case it returns a sink that applies this
// source's own filters before feeding into the shared export pipeline.
//
// Filters are built unconditionally, as part of constructing this sink --
// this is what fixes the legacy logReceiver.update()'s latent bug, where
// setupFilters only ran after resolving at least one file from "include"
// (so a network-only receiver's filters: were silently never applied): here,
// there's no file-discovery step to gate on in the first place, filtering
// always happens before WantSource returns, regardless of whether the source
// is a file, a container tail, or a network push.
func (man *Manager) WantSource(ctx context.Context, src logsource.ResolvedSource) (consumer.Logs, bool) {
	if !src.SendLogs {
		return nil, false
	}

	switch src.Kind {
	case logsource.SourceReceiver:
		filters, err := decodeReceiverFilters(man.config.Receivers[src.ReceiverName])
		if err != nil {
			logWarnings(errorf("log receiver %q: decoding filters: %w", src.ReceiverName, err))
		}

		return man.wrapWithFilter(ctx, logsource.SourceReceiver, "recv-"+src.ReceiverName, filters)
	case logsource.SourceContainerLabel:
		filters := man.resolveContainerFilter(src.Container)

		return man.wrapWithFilter(ctx, logsource.SourceContainerLabel, "ctr-"+src.Container.ID(), filters)
	default:
		return nil, false
	}
}

// receiverFilterFields narrow-decodes a config receiver's own "filters" key,
// the only field WantSource itself needs from it: include/container_name/
// container_selectors/network/log_format/operators are all already resolved
// by logsource.ReceiverManager, upstream of the fan-out.
type receiverFilterFields struct {
	Filters config.OTELFilters `mapstructure:"filters"`
}

func decodeReceiverFilters(raw config.LogReceiver) (config.OTELFilters, error) {
	var fields receiverFilterFields

	if err := mapstructure.Decode(raw, &fields); err != nil {
		return nil, fmt.Errorf("decoding receiver config: %w", err)
	}

	return fields.Filters, nil
}

// resolveContainerFilter resolves ctr's shipping-only log filter: its own
// glouton.log_filter label if it names a known filter, else the
// OpenTelemetry.ContainerFilter[containerName] fallback. This mirrors
// logsource.resolveContainerLogFormat's log_format resolution, except
// log_filter is shipping-only, so logsource.ReceiverManager exposes the raw
// label (ResolvedSource doesn't carry it) but leaves resolving it to this
// package (see logsource/container_labels.go's containerLabels.LogFilter doc
// comment).
func (man *Manager) resolveContainerFilter(ctr facts.Container) config.OTELFilters {
	containerName := ctr.ContainerName()

	if name, found := facts.LabelsAndAnnotations(ctr)[logsource.ContainerLabelPrefix+"log_filter"]; found {
		if filters, found := man.config.KnownLogFilters[name]; found {
			return filters
		}

		logger.V(1).Printf("Container %s (%s) requires an unknown log filter: %q", containerName, ctr.ID(), name)
	}

	if name, found := man.containerFilter[containerName]; found {
		return man.config.KnownLogFilters[name]
	}

	return nil
}

// wrapWithFilter builds name's filter processor and feeds it into the shared
// pipeline (pipeline.go's exporter/batcher/backpressure/global-filter/
// resource-attribute chain) -- the same filter-then-pipeline wiring for both
// a config receiver and a container-label source, built once, synchronously,
// at WantSource call time. A build/start failure is logged and reported as
// "not interested" (WantSource has no error return of its own).
func (man *Manager) wrapWithFilter(ctx context.Context, kind logsource.SourceKind, name string, filters config.OTELFilters) (consumer.Logs, bool) {
	filterCfg, warn, err := buildLogFilterConfig(filters)
	if err != nil {
		logWarnings(errorf("log source %q: building filters: %w", name, err))

		return nil, false
	}

	if warn != nil {
		logWarnings(errorf("log source %q: %w", name, warn))
	}

	logCounter := new(atomic.Int64)
	throughputMeter := logsource.NewRingCounter(throughputMeterResolutionSecs)

	factoryFilter := filterprocessor.NewFactory()

	man.pipeline.l.Lock()

	logFilter, err := factoryFilter.CreateLogs(
		ctx,
		processor.Settings{
			ID:                component.NewIDWithName(factoryFilter.Type(), "log-filter-otel-"+name),
			TelemetrySettings: withoutDebugLogs(man.pipeline.telemetry),
		},
		filterCfg,
		logsource.WrapWithInstrumentation(man.pipeline.getInput(), logCounter, throughputMeter),
	)
	if err != nil {
		man.pipeline.l.Unlock()
		logWarnings(errorf("log source %q: setup log filter: %w", name, err))

		return nil, false
	}

	if err := logFilter.Start(ctx, nil); err != nil {
		man.pipeline.l.Unlock()
		logWarnings(errorf("log source %q: start log filter: %w", name, err))

		return nil, false
	}

	man.pipeline.startedComponents = append(man.pipeline.startedComponents, logFilter)
	man.pipeline.l.Unlock()

	// man.l is acquired separately (never nested with man.pipeline.l, in
	// either order) to avoid a lock-ordering inversion with
	// HandleLogsFromDynamicSources, which acquires man.l before
	// man.pipeline.l.
	man.l.Lock()
	man.fanoutSinks[name] = &fanoutSink{kind: kind, logCounter: logCounter, throughputMeter: throughputMeter}
	man.l.Unlock()

	return logFilter, true
}

// fanoutSourceDiagnosticsLocked snapshots every WantSource-built sink's
// counters for DiagnosticArchive. Callers must hold man.l.
func (man *Manager) fanoutSourceDiagnosticsLocked() map[string]fanoutSourceDiagnostic {
	out := make(map[string]fanoutSourceDiagnostic, len(man.fanoutSinks))

	for name, sink := range man.fanoutSinks {
		kind := "receiver"
		if sink.kind == logsource.SourceContainerLabel {
			kind = "container_label"
		}

		out[name] = fanoutSourceDiagnostic{
			Kind:                   kind,
			LogProcessedCount:      sink.logCounter.Load(),
			LogThroughputPerMinute: sink.throughputMeter.Total(),
		}
	}

	return out
}
