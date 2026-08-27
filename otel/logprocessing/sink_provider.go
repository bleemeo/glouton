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

// fanoutSink backs a WantSource sink for a ReceiverManager-owned source: its own filter stage feeding the
// shared pipeline. comp is kept so ReleaseSource can shut it down individually.
type fanoutSink struct {
	kind            logsource.SourceKind
	comp            component.Component
	logCounter      *atomic.Int64
	throughputMeter *logsource.RingCounter
}

// WantSource implements logsource.SinkProvider: returns a sink with filters applied if SendLogs is true.
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
		// This container's logs are already shipped from the service path (containerRecv), so wanting
		// them here too would ship every line twice. Only this provider's interest is declined, not the
		// source itself: logmetrics resolves a container's glouton.log_metrics rule exclusively from
		// SourceContainerLabel, so suppressing the whole source upstream would silently stop those
		// metrics. When logmetrics declines too, ReceiverManager starts no tail at all (its fanout is
		// nil), which is what actually removes the duplicate.
		if man.containerRecv.isTailing(src.Container.ID()) {
			return nil, false
		}

		filters := man.resolveContainerFilter(src.Container)

		return man.wrapWithFilter(ctx, logsource.SourceContainerLabel, "ctr-"+src.Container.ID(), filters)
	default:
		return nil, false
	}
}

// receiverFilterFields decodes a config receiver's "filters" key, the only field WantSource needs
// (everything else is already resolved upstream by logsource.ReceiverManager).
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

// resolveContainerFilter resolves ctr's log filter: its own glouton.log_filter label if it names a known
// filter, else the OpenTelemetry.ContainerFilter[containerName] fallback.
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

// resolveContainerFormat is resolveContainerFilter's counterpart for log formats: ctr's own
// glouton.log_format label if it names a known format, else the OpenTelemetry.ContainerFormat
// [containerName] fallback. nil when the container asks for neither.
func (man *Manager) resolveContainerFormat(ctr facts.Container) []config.OTELOperator {
	containerName := ctr.ContainerName()

	if name, found := facts.LabelsAndAnnotations(ctr)[logsource.ContainerLabelPrefix+"log_format"]; found {
		if operators, found := man.knownLogFormats[name]; found {
			return operators
		}

		logger.V(1).Printf("Container %s (%s) requires an unknown log format: %q", containerName, ctr.ID(), name)
	}

	if name, found := man.config.ContainerFormat[containerName]; found {
		return man.knownLogFormats[name]
	}

	return nil
}

// wrapWithFilter builds name's filter processor and feeds it into the shared pipeline. A build/start
// failure is logged and reported as "not interested" (WantSource has no error return of its own).
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

	// Taken only after man.pipeline.l is released just above: this function never holds both at once.
	// Where both are genuinely needed (handleProcessingLifecycle's shutdown, DiagnosticArchive), the
	// order is always man.l first -- nesting them the other way around would deadlock against those.
	man.l.Lock()
	man.fanoutSinks[name] = &fanoutSink{kind: kind, comp: logFilter, logCounter: logCounter, throughputMeter: throughputMeter}
	man.l.Unlock()

	return logFilter, true
}

// ReleaseSource implements logsource.SinkProvider: it shuts down and forgets container's filter processor,
// if any. Called when a SourceContainerLabel container disappears, so its processor doesn't leak on recreation.
func (man *Manager) ReleaseSource(ctx context.Context, container facts.Container) {
	if container == nil {
		return
	}

	name := "ctr-" + container.ID()

	man.l.Lock()

	sink, found := man.fanoutSinks[name]
	if found {
		delete(man.fanoutSinks, name)
	}
	man.l.Unlock()

	if !found {
		return
	}

	logger.V(2).Printf("logprocessing: releasing container %s (filter processor stopped)", container.ID())

	man.pipeline.l.Lock()
	man.pipeline.startedComponents = removeComponent(man.pipeline.startedComponents, sink.comp)
	man.pipeline.l.Unlock()

	stopComponents([]component.Component{sink.comp})
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
