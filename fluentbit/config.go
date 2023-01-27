package fluentbit

import (
	"fmt"
	"os"
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

// Write the Fluent Config.
func writeFluentBitConfig(inputs []input) error {
	fluentbitConfig := inputsToFluentBitConfig(inputs)

	// Even if Glouton only manages a single config file, we have to put it
	// in its own directory so the directory can be mounted on Docker and Kubernetes.
	// Mounting a single file doesn't seem to work well with inotify and Fluent Bit
	// doesn't detect that its config has been modified.
	err := os.MkdirAll(configDir, 0o744)
	if err != nil {
		return fmt.Errorf("create Fluent Bit config directory: %w", err)
	}

	//nolint:gosec // The file needs to be readable by Fluent Bit.
	err = os.WriteFile(configFile, []byte(fluentbitConfig), 0o644)
	if err != nil {
		return fmt.Errorf("write Fluent Bit config: %w", err)
	}

	return nil
}

// Convert the log inputs to a Fluent Bit config.
func inputsToFluentBitConfig(inputs []input) string {
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
