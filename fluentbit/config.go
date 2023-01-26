package fluentbit

import (
	"fmt"
	"glouton/config"
	"strings"
)

// The service config enables the monitoring endpoint.
const serviceConfig = `# DO NOT EDIT, this file is managed by Glouton.

[SERVICE]
  HTTP_Server  On
  HTTP_Listen  0.0.0.0
  HTTP_PORT    2020
`

// Basic input to tail a log file and associate it to a tag.
const inputTailConfig = `
[INPUT]
  Name    tail
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

// Convert the log inputs to a Fluent Bit config.
func inputsToFluentBitConfig(inputs []config.LogInput) string {
	var configText strings.Builder

	configText.WriteString(serviceConfig)

	for _, input := range inputs {
		inputTag := "original_input_" + input.Path

		// Configure the input to read the log file.
		configText.WriteString(fmt.Sprintf(inputTailConfig, input.Path, inputTag))

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
