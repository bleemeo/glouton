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
	"strings"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/go-viper/mapstructure/v2"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
)

const (
	logFileSizesCacheKey    = "LogFileSizes"
	logFileMetadataCacheKey = "LogFileMetadata"
)

func getLastFileSizesFromCache(state bleemeoTypes.State) (lastFileSizes map[string]int64) {
	err := state.Get(logFileSizesCacheKey, &lastFileSizes)
	if err != nil {
		logger.V(1).Printf("Can't find log file sizes in cache: %v", err)
	}

	return lastFileSizes
}

func saveLastFileSizesToCache(state bleemeoTypes.State, receivers []*logReceiver) {
	lastFileSizes := make(map[string]int64)

	for _, recv := range receivers {
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

func shouldUnmarshalYamlToMapstructure(t reflect.Type) bool {
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
	if !shouldUnmarshalYamlToMapstructure(to.Type()) {
		return from.Interface(), nil // returning the data as-is
	}

	if yamlUnmarshaler, ok := to.Addr().Interface().(obsoleteUnmarshaler); ok {
		err := yamlUnmarshaler.UnmarshalYAML(func(v any) error {
			decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
				Result:     v,
				DecodeHook: unmarshalMapstructureHook,
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

func buildOperators(rawOperators []map[string]any) ([]operator.Config, error) {
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

type diagnosticInformation struct {
	LogProcessedCount      int64
	LogThroughputPerMinute int
	ProcessingStatus       string
	OTLPReceiver           *otlpReceiverDiagnosticInformation
	Receivers              map[string]receiverDiagnosticInformation
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
