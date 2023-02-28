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

package fluentbit

import (
	_ "embed"
	"fmt"
	containerTypes "glouton/facts/container-runtime/types"
	"os"
	"strings"
)

const (
	configDir   = "/var/lib/glouton/fluent-bit/config"
	configFile  = configDir + "/fluent-bit.conf"
	parsersFile = configDir + "/parsers.conf"
	dbDir       = "/var/lib/glouton/fluent-bit/db"
	dbFile      = dbDir + "/logs.db"
)

//go:embed parsers.conf
var parsersConfig string

// The service config enables the monitoring endpoint.
const serviceConfig = `# DO NOT EDIT, this file is managed by Glouton.

[SERVICE]
    Parsers_File %s
`

// Input to tail a log file with a parser and associate it to a tag.
const inputTailWithParserConfig = `
[INPUT]
    Name    tail
    DB      %s
    Parser  %s
    Path    %s
    Tag     %s
`

// Input to tail a log file and associate it to a tag.
const inputTailNoParserConfig = `
[INPUT]
    Name    tail
    DB      %s
    Path    %s
    Tag     %s
`

// Rewrite tag filter duplicates an input with another tag.
const filterRewriteConfig = `
[FILTER]
    Name    rewrite_tag
    Match   %s
    Rule    log .* %s true
`

// Grep filters lines matching a regular expression.
const filterGrepConfig = `
[FILTER]
    Name    grep
    Match   %s
    Regex   log %s
`

// Null output drops all lines received.
const outputNullConfig = `
[OUTPUT]
    Name    null
    Match   %s
    Alias   %s
`

// Write the static Fluent Bit config.
func writeStaticConfig() error {
	err := os.MkdirAll(dbDir, 0o744)
	if err != nil {
		return fmt.Errorf("create Fluent Bit database directory: %w", err)
	}

	err = os.MkdirAll(configDir, 0o744)
	if err != nil {
		return fmt.Errorf("create Fluent Bit config directory: %w", err)
	}

	err = os.WriteFile(parsersFile, []byte(parsersConfig), 0o600)
	if err != nil {
		return fmt.Errorf("write Fluent Bit config: %w", err)
	}

	return nil
}

// Write the Fluent Bit config corresponding to the inputs.
func writeDynamicConfig(inputs []input) error {
	fluentbitConfig := inputsToFluentBitConfig(inputs)

	//nolint:gosec // The file needs to be readable by Fluent Bit.
	err := os.WriteFile(configFile, []byte(fluentbitConfig), 0o644)
	if err != nil {
		return fmt.Errorf("write Fluent Bit config: %w", err)
	}

	return nil
}

// Convert the log inputs to a Fluent Bit config.
func inputsToFluentBitConfig(inputs []input) string {
	var configText strings.Builder

	configText.WriteString(fmt.Sprintf(serviceConfig, parsersFile))

	for _, input := range inputs {
		inputTag := "original_input_" + input.Path

		var inputConfig string

		switch input.Runtime {
		case containerTypes.DockerRuntime:
			// Use docker parser to interpret the JSON formatted data.
			inputConfig = fmt.Sprintf(inputTailWithParserConfig, dbFile, "docker-escaped", input.Path, inputTag)
		case containerTypes.ContainerDRuntime:
			// ContainerD uses the cri-o log format.
			inputConfig = fmt.Sprintf(inputTailWithParserConfig, dbFile, "cri-log", input.Path, inputTag)
		default:
			// Outside of containers, interpret the logs as unstructured data.
			inputConfig = fmt.Sprintf(inputTailNoParserConfig, dbFile, input.Path, inputTag)
		}

		// Configure the input to read the log file.
		configText.WriteString(inputConfig)

		for _, filter := range input.Filters {
			filterTag := filter.Metric + "_tag"

			// Duplicate the original input with another tag dedicated to this filter.
			configText.WriteString(fmt.Sprintf(filterRewriteConfig, inputTag, filterTag))
			// Filter the line matching the regular expression.
			configText.WriteString(fmt.Sprintf(filterGrepConfig, filterTag, filter.Regex))
			// Create a NULL output that drops lines from the previous filter.
			// The number of lines received by this output is the number of line
			// that matched the regular expression.
			configText.WriteString(fmt.Sprintf(outputNullConfig, filterTag, filter.Metric))
		}
	}

	return configText.String()
}
