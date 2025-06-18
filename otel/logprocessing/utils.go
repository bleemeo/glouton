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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/go-viper/mapstructure/v2"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	logFileSizesCacheKey    = "LogFileSizes"
	logFileMetadataCacheKey = "LogFileMetadata"
)

type fileSizer interface {
	sizesByFile() (map[string]int64, error)
}

func getLastFileSizesFromCache(state bleemeoTypes.State) (lastFileSizes map[string]int64) {
	err := state.Get(logFileSizesCacheKey, &lastFileSizes)
	if err != nil {
		logger.V(1).Printf("Can't find log file sizes in cache: %v", err)
	}

	return lastFileSizes
}

func saveLastFileSizesToCache[FS fileSizer](state bleemeoTypes.State, sizers []FS) {
	lastFileSizes := make(map[string]int64)

	for _, recv := range sizers {
		sizesByFile, err := recv.sizesByFile()
		if err != nil {
			logger.V(1).Printf("Can't get log file sizes: %v", err)

			continue
		}

		for logFile, size := range sizesByFile {
			lastFileSizes[logFile] = size
		}
	}

	err := state.Set(logFileSizesCacheKey, lastFileSizes)
	if err != nil {
		logger.V(1).Printf("Failed to save last log file sizes to cache: %v", err)
	}
}

func getFileMetadataFromCache(state bleemeoTypes.State) (map[string]map[string][]byte, error) {
	var metadataMap map[string]map[string][]byte

	err := state.Get(logFileMetadataCacheKey, &metadataMap)
	if err != nil {
		return nil, err
	}

	if metadataMap == nil { // it may not exist in the state cache yet
		metadataMap = make(map[string]map[string][]byte)
	}

	return metadataMap, nil
}

func saveFileMetadataToCache(state bleemeoTypes.State, metadata map[string]map[string][]byte) {
	err := state.Set(logFileMetadataCacheKey, metadata)
	if err != nil {
		logger.V(1).Printf("Failed to save log file metadata to cache: %v", err)
	}
}

func mergeLastFileSizes(receivers []*logReceiver, containerRecv *containerReceiver) []fileSizer {
	sizers := make([]fileSizer, len(receivers)+1)

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

// shutdownAll stops all the given components (in reverse order).
// It should be called before every unsuccessful return of the log pipeline initialization.
func shutdownAll(components []component.Component) {
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
func stopReceivers(receivers []*logReceiver) {
	for _, recv := range receivers {
		recv.l.Lock()
		shutdownAll(recv.startedComponents)
		recv.l.Unlock()
	}
}

func errorf(format string, a ...any) error {
	return fmt.Errorf(format, a...) //nolint:err113
}

func logWarnings(errs ...error) {
	logger.V(1).Printf("Log processing warning: %v", errs)
}

func shouldUnmarshalYAMLToMapstructure(t reflect.Type) bool {
	const otelPackagePrefix = "github.com/open-telemetry/opentelemetry-collector-contrib/"

	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}

	switch pkgPath := t.PkgPath(); {
	case strings.HasPrefix(pkgPath, otelPackagePrefix):
		// We only want to apply this particular way of unmarshalling to types that come from OpenTelemetry...
		return true
	case pkgPath == "":
		// ...but we also need to apply it to builtin types that may contain OpenTelemetry types.
		return true
	default:
		return false
	}
}

// obsoleteUnmarshaler is a copy of gopkg.in/yaml.v3.obsoleteUnmarshaler
// and is implemented by types that bring their own unmarshalling logic,
// like github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator.Config.
type obsoleteUnmarshaler interface {
	UnmarshalYAML(unmarshal func(any) error) error
}

func unmarshalMapstructureHook(from reflect.Value, to reflect.Value) (any, error) {
	// The purpose of this mapstructure hook is to call the UnmarshalYAML() method
	// on types that define it in order to construct themselves correctly,
	// while being not unmarshalling YAML, but decoding a slice of maps to a slice of [operator.Config].
	if !shouldUnmarshalYAMLToMapstructure(to.Type()) {
		return from.Interface(), nil // returning the data as-is
	}

	if yamlUnmarshaler, ok := to.Addr().Interface().(obsoleteUnmarshaler); ok {
		err := yamlUnmarshaler.UnmarshalYAML(func(v any) error {
			decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
				Result: v,
				// We aim to align the decoding behavior with opentelemetry-collector:
				// https://github.com/open-telemetry/opentelemetry-collector/blob/8cf42f3cf789bceca4149180737ac9a5b26684e8/confmap/confmap.go#L221
				DecodeHook: mapstructure.ComposeDecodeHookFunc(
					mapstructure.StringToSliceHookFunc(","),
					mapstructure.StringToTimeDurationHookFunc(),
					unmarshalMapstructureHook,
				),
			})
			if err != nil {
				return fmt.Errorf("error creating decoder: %w", err)
			}

			return decoder.Decode(from.Interface())
		})
		if err != nil {
			return nil, err
		}

		return to.Interface(), nil
	}

	return from.Interface(), nil // returning the data as-is
}

func buildOperators(rawOperators []config.OTELOperator) ([]operator.Config, error) {
	var operators []operator.Config

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:     &operators,
		DecodeHook: unmarshalMapstructureHook,
	})
	if err != nil {
		return nil, fmt.Errorf("error creating decoder: %w", err)
	}

	err = decoder.Decode(rawOperators)
	if err != nil {
		return nil, err
	}

	return operators, nil
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

type CommandRunner interface {
	Run(ctx context.Context, option gloutonexec.Option, name string, arg ...string) ([]byte, error)
	StartWithPipes(ctx context.Context, option gloutonexec.Option, name string, arg ...string) (stdoutPipe io.ReadCloser, stderrPipe io.ReadCloser, wait func() error, err error)
}

type otlpReceiverDiagnosticInformation struct {
	GRPCEnabled            bool
	HTTPEnabled            bool
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
	ReceiverKind           receiverKind
	Attributes             ContainerAttributes
}

type diagnosticInformation struct {
	LogProcessedCount             int64
	LogThroughputPerMinute        int
	PushErrorsThroughputPerMinute int
	ProcessingStatus              string
	OTLPReceiver                  *otlpReceiverDiagnosticInformation
	Receivers                     map[string]receiverDiagnosticInformation
	ContainerReceivers            map[string]containerDiagnosticInformation
	WatchedServices               map[string][]receiverDiagnosticInformation
	KnownLogFormats               map[string][]config.OTELOperator
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
