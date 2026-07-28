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
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"

	"github.com/bleemeo/glouton/config"

	"github.com/go-viper/mapstructure/v2"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
)

var (
	errIncludeNotStr = errors.New("include value must be a string")
	errIsUnknown     = errors.New("is unknown")
	errIsRecursive   = errors.New("is recursive")
)

// ExpandOperators replaces 'template' operators with the well-known format they reference.
// These 'template' operators must define a single "include" key, like so:
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

// ExpandLogFormats expands every named format in formats, allowing one level
// of cross-referencing between them. Referenced formats must be defined
// above references to them.
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
				// https://github.com/open-telemetry/opentelemetry-collector/blob/ac7c0f2f4cd8fa05ccc7def96e997eabc2c44f33/confmap/confmap.go#L226
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

// QuietParserErrors defaults parser operators to on_error="send_quiet" when they
// don't set on_error explicitly. Stanza's default is "send", which logs one ERROR
// per line that fails to parse. For a high-volume source whose lines don't all
// match (e.g. multi-line Postgres logs), that floods the logs and the logger's
// de-duplication cache (see logger/zap.go) -- it only moves the parse-failure log
// down to debug level, it still forwards the entry.
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

		// Every stanza parser (regex_parser, json_parser, time_parser,
		// severity_parser, key_value_parser, ...) logs one error per line that
		// fails to parse when on_error is left at the default "send".
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

// BuildOperators decodes rawOperators (plain YAML, as config.OTELOperator)
// into stanza operator.Config values, the shape both otel/logprocessing and
// otel/logmetrics feed into filelogreceiver/execlogreceiver.
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
