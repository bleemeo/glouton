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
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/otel/logsource"
	"github.com/bleemeo/glouton/types"

	"github.com/go-viper/mapstructure/v2"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/filterprocessor"
	"go.opentelemetry.io/collector/component"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	logFileSizesCacheKey    = "LogFileSizes"
	logFileMetadataCacheKey = "LogFileMetadata"
	persistStorageType      = "glouton_log_metadata_storage"
)

var (
	errUnexpectedType = errors.New("unexpected type")
	errUnknownField   = errors.New("some unknown field(s) were found")
)

// mergeLastFileSizes gathers every receiver's FileSizer for
// logsource.SaveLastFileSizesToCache.
func mergeLastFileSizes(receivers []*logReceiver, containerRecv *containerReceiver) []logsource.FileSizer {
	sizers := make([]logsource.FileSizer, len(receivers)+1)

	for i, recv := range receivers {
		sizers[i] = recv
	}

	sizers[len(sizers)-1] = containerRecv

	return sizers
}

func validateContainerOperators(containerOps map[string]string, opsConfigs map[string][]config.OTELOperator) map[string]string {
	for ctrName, opName := range containerOps {
		if opsConfigs[opName] == nil {
			logger.V(1).Printf("Container %q requires the log processing operator %q, which is not defined", ctrName, opName)

			delete(containerOps, ctrName)
		}
	}

	return containerOps
}

func validateContainerFilters(containerFilter map[string]string, filtersConfigs map[string]config.OTELFilters) map[string]string {
	for ctrName, filterName := range containerFilter {
		if filtersConfigs[filterName] == nil {
			logger.V(1).Printf("Container %q requires the log processing filter %q, which is not defined", ctrName, filterName)

			delete(containerFilter, ctrName)
		}
	}

	return containerFilter
}

// stopComponents stops all the given components (in reverse order).
func stopComponents(components []component.Component) {
	wg := new(sync.WaitGroup)
	wg.Add(len(components))

	// Shutting down first the components that are at the beginning of the log production chain.
	for _, comp := range slices.Backward(components) {
		go func() {
			defer crashreport.ProcessPanic()
			defer wg.Done()

			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()

			err := comp.Shutdown(shutdownCtx)
			if err != nil {
				logger.V(1).Printf("Failed to shutdown log processing component %T: %v", comp, err)
			}
		}()
	}

	wg.Wait()
}

// stopReceivers shutdowns all the components started by the given receivers,
// while taking care of the receivers lock synchronization.
func stopReceivers(receivers []*logReceiver, removePersistentExtsFn func([]component.ID)) {
	for _, recv := range receivers {
		recv.l.Lock()

		stopComponents(recv.startedComponents)
		removePersistentExtsFn(recv.registeredExtensions)

		recv.l.Unlock()
	}
}

func errorf(format string, a ...any) error {
	return fmt.Errorf(format, a...) //nolint:err113
}

func logWarnings(errs ...error) {
	logger.V(1).Printf("Log processing warning: %v", errs)
}

// withoutDebugLogs increases the level of the logger to "info", so as to avoid debug logs
// (especially those from the ottl package, which occur for each record going through the component).
func withoutDebugLogs(telSet component.TelemetrySettings) component.TelemetrySettings {
	telSet.Logger = telSet.Logger.WithOptions(zap.IncreaseLevel(zapcore.InfoLevel))

	return telSet
}

func buildLogFilterConfig(filtersCfg config.OTELFilters) (*filterprocessor.Config, error, error) {
	defaultCfg := filterprocessor.NewFactory().CreateDefaultConfig()

	filterProcCfg, ok := defaultCfg.(*filterprocessor.Config)
	if !ok {
		return nil, nil, fmt.Errorf("%w for filterprocessor config: %T", errUnexpectedType, defaultCfg) //nolint: nilnil
	}

	if len(filtersCfg) == 0 {
		return filterProcCfg, nil, nil
	}

	var decoderMeta mapstructure.Metadata

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Metadata: &decoderMeta,
		Result:   &filterProcCfg.Logs, //nolint: staticcheck
	})
	if err != nil {
		return nil, nil, fmt.Errorf("initializing decoder: %w", err) //nolint: nilnil
	}

	err = decoder.Decode(filtersCfg)
	if err != nil {
		return nil, nil, err //nolint: nilnil
	}

	var warning error

	if len(decoderMeta.Unused) != 0 {
		warning = fmt.Errorf("%w: %s", errUnknownField, strings.Join(decoderMeta.Unused, ", "))
	}

	return filterProcCfg, warning, filterProcCfg.Validate()
}

// expandOperators, expandLogFormats, buildOperators and quietParserErrors now
// live in otel/logsource (shared with otel/logmetrics, see that package's own
// doc comment), thin wrappers here so every existing call site in this
// package (and its tests) keeps working unchanged.
func expandOperators(ops []config.OTELOperator, knownIncludes map[string][]config.OTELOperator, denyRecursiveInclude bool) ([]config.OTELOperator, error) {
	return logsource.ExpandOperators(ops, knownIncludes, denyRecursiveInclude)
}

func expandLogFormats(formats map[string][]config.OTELOperator) (map[string][]config.OTELOperator, error) {
	return logsource.ExpandLogFormats(formats)
}

func buildOperators(rawOperators []config.OTELOperator) ([]operator.Config, error) {
	return logsource.BuildOperators(rawOperators)
}

func quietParserErrors(ops []config.OTELOperator) []config.OTELOperator {
	return logsource.QuietParserErrors(ops)
}

// diffBetween returns the elements from s1 that are absent from m2.
func diffBetween[K comparable, V any](s1 []K, m2 map[K]V) []K {
	var diff []K

loop1:
	for _, e1 := range s1 {
		if _, found := m2[e1]; found {
			continue loop1
		}

		diff = append(diff, e1)
	}

	return diff
}

// CommandRunner is otel/logsource.CommandRunner, aliased here so existing call
// sites in this package don't need to spell out the otel/logsource import.
type CommandRunner = logsource.CommandRunner

type Facter interface {
	Facts(ctx context.Context, maxAge time.Duration) (facts map[string]string, err error)
}

type sourceDiagnostic struct {
	ContainerName    string
	ContainerID      string
	ServiceKey       discovery.NameInstance
	IsFromService    bool
	SkipReason       string
	ServiceLogPaths  []string
	ContainerLogPath string
	SetupError       string
}

type otlpReceiverDiagnosticInformation struct {
	// Receivers is the list of log.network.receivers entries this feature
	// pulls externally-pushed logs from (log.opentelemetry.network.receivers).
	Receivers              []string
	LogProcessedCount      int64
	LogThroughputPerMinute int
}

type journaldReceiverDiagnosticInformation struct {
	LogProcessedCount      int64
	LogThroughputPerMinute int
}

type receiverDiagnosticInformation struct {
	LogProcessedCount      int64
	LogThroughputPerMinute int
	FileLogReceiverPaths   []string
	ExecLogReceiverPaths   []string
	IgnoredFilePaths       []string
}

type containerDiagnosticInformation struct {
	LogProcessedCount      int64
	LogThroughputPerMinute int
	LogFilePath            string
	LogFileRealPath        string
	ReceiverKind           logsource.ReceiverKind
	Attributes             ContainerAttributes
}

type diagnosticSummary struct {
	LogProcessedCount               int64
	LogThroughputPerMinute          int
	ProcessingStatus                string
	ContainerStartedComponents      []string
	PipelineStartedComponentsCount  int
	PerServiceStartedComponentCount map[string]int
}

type diagnosticReceiver struct {
	OTLPReceiver       *otlpReceiverDiagnosticInformation
	JournaldReceiver   *journaldReceiverDiagnosticInformation
	Receivers          map[string]receiverDiagnosticInformation
	ContainerReceivers map[string]containerDiagnosticInformation
	WatchedServices    map[string][]receiverDiagnosticInformation
}

type diagnosticReceiverSetup struct {
	SkippedSource     []sourceDiagnostic
	WatchedServices   map[string]sourceDiagnostic
	WatchedContainers map[string]sourceDiagnostic
}

type diagnosticInformation struct {
	summary         diagnosticSummary
	receivers       diagnosticReceiver
	receiversSetup  diagnosticReceiverSetup
	KnownLogFormats map[string][]config.OTELOperator
	KnownLogFilters map[string]config.OTELFilters
}

func (diagInfo diagnosticInformation) writeToArchive(writer types.ArchiveWriter) error {
	file, err := writer.Create("log-processing/summary.json")
	if err != nil {
		return err
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	if err := enc.Encode(diagInfo.summary); err != nil {
		return err
	}

	if err := diagInfo.receivers.writeToArchive(writer); err != nil {
		return err
	}

	if err := diagInfo.receiversSetup.writeToArchive(writer); err != nil {
		return err
	}

	file, err = writer.Create("log-processing/known_formats.json")
	if err != nil {
		return err
	}

	enc = json.NewEncoder(file)
	enc.SetIndent("", "  ")

	if err := enc.Encode(diagInfo.KnownLogFormats); err != nil {
		return err
	}

	file, err = writer.Create("log-processing/known_filters.json")
	if err != nil {
		return err
	}

	enc = json.NewEncoder(file)
	enc.SetIndent("", "  ")

	if err := enc.Encode(diagInfo.KnownLogFilters); err != nil {
		return err
	}

	return nil
}

func (receiverDiag diagnosticReceiver) writeToArchive(writer types.ArchiveWriter) error {
	file, err := writer.Create("log-processing/receivers.json")
	if err != nil {
		return err
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(receiverDiag)
}

func (receiverDiag diagnosticReceiverSetup) writeToArchive(writer types.ArchiveWriter) error {
	file, err := writer.Create("log-processing/receivers-setup.json")
	if err != nil {
		return err
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(receiverDiag)
}

func flattenLogPaths(data []discovery.ServiceLogReceiver) []string {
	result := make([]string, 0, len(data))

	for _, row := range data {
		result = append(result, row.FilePath)
	}

	return result
}
