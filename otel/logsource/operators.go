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

package logsource

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"

	"github.com/go-viper/mapstructure/v2"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/adapter"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/entry"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/pipeline"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
)

var (
	errIncludeNotStr         = errors.New("include value must be a string")
	errIsUnknown             = errors.New("is unknown")
	errIsRecursive           = errors.New("is recursive")
	errNoOperatorsInPipeline = errors.New("network operator pipeline has no operators")
)

// ExpandOperators replaces 'template' operators (which must define a single "include" key) with the well-known format they reference; example:
//
//	{
//		   "include": "some-format"
//	}
func ExpandOperators(ops []config.OTELOperator, knownIncludes map[string][]config.OTELOperator, denyRecursiveInclude bool) ([]config.OTELOperator, error) {
	result := make([]config.OTELOperator, 0, len(ops))

	for _, rawOp := range ops {
		if include, ok := rawOp["include"]; ok && len(rawOp) == 1 {
			includeStr, ok := include.(string)
			if !ok {
				return nil, fmt.Errorf("%w, not %T", errIncludeNotStr, include)
			}

			included, ok := knownIncludes[includeStr]
			if !ok {
				return nil, fmt.Errorf("include reference %q %w", includeStr, errIsUnknown)
			}

			if denyRecursiveInclude {
				for _, op := range included {
					if _, hasInclude := op["include"]; hasInclude {
						return nil, fmt.Errorf("include reference %q %w", includeStr, errIsRecursive)
					}
				}
			}

			result = append(result, included...)
		} else {
			result = append(result, rawOp)
		}
	}

	return result, nil
}

// ExpandLogFormats expands every named format in formats, allowing one level of cross-referencing between them.
func ExpandLogFormats(formats map[string][]config.OTELOperator) (map[string][]config.OTELOperator, error) {
	result := make(map[string][]config.OTELOperator, len(formats))

	var err error

	for format, ops := range formats {
		result[format], err = ExpandOperators(ops, formats, true)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", format, err)
		}
	}

	return result, nil
}

func shouldUnmarshalYAMLToMapstructure(t reflect.Type) bool {
	const otelPackagePrefix = "github.com/open-telemetry/opentelemetry-collector-contrib/"

	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}

	switch pkgPath := t.PkgPath(); {
	case strings.HasPrefix(pkgPath, otelPackagePrefix):
		return true // OpenTelemetry types
	case pkgPath == "":
		return true // builtin types, may contain OpenTelemetry types
	default:
		return false
	}
}

// obsoleteUnmarshaler is a copy of gopkg.in/yaml.v3.obsoleteUnmarshaler, implemented by types with custom unmarshalling logic.
type obsoleteUnmarshaler interface {
	UnmarshalYAML(unmarshal func(any) error) error
}

func unmarshalMapstructureHook(from reflect.Value, to reflect.Value) (any, error) {
	// Calls UnmarshalYAML() on types that define it, even though we aren't unmarshalling YAML directly.
	if !shouldUnmarshalYAMLToMapstructure(to.Type()) {
		return from.Interface(), nil // returning the data as-is
	}

	if yamlUnmarshaler, ok := to.Addr().Interface().(obsoleteUnmarshaler); ok {
		err := yamlUnmarshaler.UnmarshalYAML(func(v any) error {
			decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
				Result: v,
				// Aligns decoding behavior with opentelemetry-collector's confmap.
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

	return from.Interface(), nil // return the data as-is
}

// QuietParserErrors defaults parser operators to on_error="send_quiet" unless set explicitly, since Stanza's default logs one ERROR per failed line.
func QuietParserErrors(ops []config.OTELOperator) []config.OTELOperator {
	const (
		typeKey      = "type"
		onErrorKey   = "on_error"
		sendQuiet    = "send_quiet"
		parserSuffix = "_parser"
	)

	out := make([]config.OTELOperator, len(ops))

	for i, op := range ops {
		out[i] = op

		// Only parser-type operators log per-line errors.
		if typ, _ := op[typeKey].(string); !strings.HasSuffix(typ, parserSuffix) {
			continue
		}

		if _, set := op[onErrorKey]; set {
			continue
		}

		// Clone so we never mutate the shared known-format definitions.
		cloned := make(config.OTELOperator, len(op)+1)
		maps.Copy(cloned, op)

		cloned[onErrorKey] = sendQuiet
		out[i] = cloned
	}

	return out
}

// BuildOperators decodes rawOperators into stanza operator.Config values, as fed into filelogreceiver/execlogreceiver.
func BuildOperators(rawOperators []config.OTELOperator) ([]operator.Config, error) {
	rawOperators = QuietParserErrors(rawOperators)

	operators := make([]operator.Config, 0, len(rawOperators))

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:     &operators,
		DecodeHook: unmarshalMapstructureHook,
	})
	if err != nil {
		return nil, fmt.Errorf("creating decoder: %w", err)
	}

	err = decoder.Decode(rawOperators)
	if err != nil {
		return nil, err
	}

	return operators, nil
}

// wrapWithOperators returns a consumer.Logs that runs operators (a receiver's plain transform chain --
// see managedSource.operators -- with no input/emitter operator of its own) against every incoming
// batch before delegating to next. It exists because operators is otherwise only ever applied by being
// embedded into a filelogreceiver/execlogreceiver's own adapter.BaseConfig (see SetupLogReceiverFactories):
// that works for include/container-tail sources, but a from_listeners (network) source's plog.Logs
// arrives pre-materialized, with no stanza receiver of its own to carry operators through.
//
// Returns next unchanged, with a no-op cleanup, when operators is empty or next is nil: this must stay a
// true zero-cost passthrough, since most from_listeners receivers set no operators at all. On any build
// error it likewise falls back to next, warning instead of failing -- matching buildReceiverOperators'
// existing convention -- since a malformed operator config should degrade a receiver back to today's
// behavior (operators skipped), not break its listener.
//
// The returned consumer.Logs is not safe for concurrent ConsumeLogs calls on its own: stanza operators
// aren't documented safe for concurrent Process/ProcessBatch on one instance, and neither is this
// function's own Batch()/OutChannel() drain protocol below. Callers sharing one from_listeners port
// across receivers must serialize calls into each receiver's own wrapped consumer themselves.
func wrapWithOperators(operators []operator.Config, set component.TelemetrySettings, next consumer.Logs) (consumer.Logs, func()) {
	noop := func() {}

	if len(operators) == 0 || next == nil {
		return next, noop
	}

	emitter := helper.NewSynchronousLogEmitter(set, func(ctx context.Context, entries []*entry.Entry) {
		if err := next.ConsumeLogs(ctx, adapter.ConvertEntries(entries)); err != nil {
			logger.V(1).Printf("logsource: network operator pipeline: forwarding converted batch: %v", err)
		}
	})

	pipe, err := pipeline.Config{Operators: operators, DefaultOutput: emitter}.Build(set)
	if err != nil {
		logger.V(1).Printf("logsource: failed to build network operator pipeline, operators won't apply: %v", err)

		return next, noop
	}

	if err := pipe.Start(nil); err != nil {
		logger.V(1).Printf("logsource: failed to start network operator pipeline, operators won't apply: %v", err)

		return next, noop
	}

	entryPoint := pipe.Operators()
	if len(entryPoint) == 0 {
		logger.V(1).Printf("logsource: network operator pipeline built with no operators, operators won't apply: %v", errNoOperatorsInPipeline)

		_ = pipe.Stop()

		return next, noop
	}

	fromPdata := adapter.NewFromPdataConverter(set, 1) // workerCount=1: callers already serialize ConsumeLogs, so extra workers only add goroutines.
	fromPdata.Start()

	bridged, err := consumer.NewLogs(func(ctx context.Context, ld plog.Logs) error {
		return runThroughOperators(ctx, fromPdata, entryPoint[0], ld)
	})
	if err != nil {
		logger.V(1).Printf("logsource: failed to build network operator pipeline consumer, operators won't apply: %v", err)

		fromPdata.Stop()
		_ = pipe.Stop()

		return next, noop
	}

	cleanup := func() {
		fromPdata.Stop()
		_ = pipe.Stop()
	}

	return bridged, cleanup
}

// runThroughOperators converts ld to stanza entries via fromPdata, feeds them through entryPoint (whose
// downstream DefaultOutput synchronously forwards the transformed result -- see wrapWithOperators), and
// waits for every (ResourceLogs x ScopeLogs) pair's converted entries before returning, so a caller
// relying on this call's completion (e.g. an OTLP gRPC/HTTP request handler) isn't racing the conversion.
func runThroughOperators(ctx context.Context, fromPdata *adapter.FromPdataConverter, entryPoint operator.Operator, ld plog.Logs) error {
	expected := 0
	for _, rls := range ld.ResourceLogs().All() {
		expected += rls.ScopeLogs().Len()
	}

	if expected == 0 {
		return nil
	}

	if err := fromPdata.Batch(ld); err != nil {
		return fmt.Errorf("converting batch for network operator pipeline: %w", err)
	}

	var errs error

	for range expected {
		entries, ok := <-fromPdata.OutChannel()
		if !ok {
			break
		}

		if err := entryPoint.ProcessBatch(ctx, entries); err != nil {
			errs = errors.Join(errs, err)
		}
	}

	return errs
}
