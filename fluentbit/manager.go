// Copyright 2015-2024 Bleemeo
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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	crTypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/prometheus/scrapper"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/prometheus/client_golang/prometheus"
)

const updateInterval = time.Minute

type registerer interface {
	RegisterGatherer(opt registry.RegistrationOption, gatherer prometheus.Gatherer) (int, error)
}

type Manager struct {
	config   config.Log
	registry registerer
	runtime  crTypes.RuntimeInterface
	runner   *gloutonexec.Runner
	// Whether an error has been logged for an input.
	errorLoggedForInput map[string]bool

	l            sync.Mutex
	loadedInputs []input
}

type input struct {
	Path    string
	Runtime string
	Filters []config.LogFilter
}

func validateConfig(cfg config.Log) []error {
	var warnings []error

	for _, input := range cfg.Inputs {
		if input.Path != "" && input.ContainerName != "" {
			err := fmt.Sprintf(
				`log inputs support either "path" or "container_name", not both, container "%s" will be ignored`,
				input.ContainerName,
			)

			warnings = append(warnings, fmt.Errorf("%w: %s", config.ErrInvalidValue, err))
		}

		if input.Path != "" && len(input.Selectors) > 0 {
			err := `log inputs support either "path" or "selectors", not both, selectors will be ignored`
			warnings = append(warnings, fmt.Errorf("%w: %s", config.ErrInvalidValue, err))
		}
	}

	return warnings
}

func (m *Manager) Run(ctx context.Context) error {
	err := writeStaticConfig()
	if err != nil {
		return err
	}

	err = m.createFluentBitScrapper()
	if err != nil {
		return err
	}

	for ctx.Err() == nil {
		err := m.update(ctx)
		if err != nil {
			logger.V(1).Printf("Failed to update Fluent Bit config: %s", err)
		}

		select {
		case <-time.After(updateInterval):
		case <-ctx.Done():
		}
	}

	return ctx.Err()
}

// Update the Fluent Bit config.
func (m *Manager) update(ctx context.Context) error {
	inputs, err := m.processConfigInputs(ctx)
	if err != nil {
		return err
	}

	m.l.Lock()
	defer m.l.Unlock()

	if m.needConfigChange(inputs) {
		err = writeDynamicConfig(inputs)
		if err != nil {
			return err
		}

		err = reloadFluentBit(ctx, m.runner)
		if err != nil {
			return err
		}

		m.loadedInputs = inputs
	}

	return nil
}

// Return whether the Fluent Bit config needs to be modified.
// The manager lock must be held.
func (m *Manager) needConfigChange(inputs []input) bool {
	if len(inputs) != len(m.loadedInputs) {
		return true
	}

	for i := range inputs {
		if inputs[i].Path != m.loadedInputs[i].Path {
			return true
		}
	}

	return false
}

// Process the inputs to find the log files corresponding to the configured container inputs.
func (m *Manager) processConfigInputs(ctx context.Context) ([]input, error) {
	containers, err := m.runtime.Containers(ctx, time.Minute, false)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	inputs := make([]input, 0, len(m.config.Inputs))

	for _, configInput := range m.config.Inputs {
		paths, runtime := m.inputLogPaths(configInput, containers)
		if len(paths) == 0 {
			continue
		}

		inputs = append(inputs, input{
			Path:    strings.Join(paths, ","),
			Runtime: runtime,
			Filters: configInput.Filters,
		})
	}

	return inputs, nil
}

// Return the log paths and the runtime for a log input.
func (m *Manager) inputLogPaths(input config.LogInput, containers []facts.Container) ([]string, string) {
	// The configured path has priority over the container name and selectors.
	if input.Path != "" {
		return []string{filepath.Join(m.config.HostRootPrefix, input.Path)}, ""
	}

	logPaths := make([]string, 0, 1)
	runtime := ""

	for _, container := range containers {
		// If both container name and selectors are present, the container must match both.
		matchName := input.ContainerName != "" && container.ContainerName() == input.ContainerName
		matchSelectors := len(input.Selectors) > 0 && containerMatchesSelectors(container, input.Selectors)

		if (len(input.Selectors) == 0 && matchName || input.ContainerName == "" && matchSelectors ||
			matchName && matchSelectors) && container.LogPath() != "" {
			logPaths = append(logPaths, filepath.Join(m.config.HostRootPrefix, container.LogPath()))
			runtime = container.RuntimeName()
		}
	}

	// Log message only once when no path was found for an input.
	if len(logPaths) == 0 && !m.errorLoggedForInput[formatInput(input)] {
		m.errorLoggedForInput[formatInput(input)] = true

		logger.V(0).Printf("Failed to find log file for input %s, logs won't be processed", formatInput(input))
	}

	// Sort the path to be able to compare them with the previous paths.
	sort.Strings(logPaths)

	return logPaths, runtime
}

// Format an input to a string.
func formatInput(input config.LogInput) string {
	if input.Path != "" {
		return input.Path
	}

	str := input.ContainerName
	if len(input.Selectors) > 0 {
		str += fmt.Sprintf("%+v", input.Selectors)
	}

	return str
}

// Return true if the container's labels or annotations match the selectors.
func containerMatchesSelectors(container facts.Container, selectors map[string]string) bool {
	matchLabels := labelsMatchSelectors(container.Labels(), selectors)
	matchAnnotations := labelsMatchSelectors(container.Annotations(), selectors)

	return matchLabels || matchAnnotations
}

// Return true if the labels match all the selectors.
func labelsMatchSelectors(labels map[string]string, selectors map[string]string) bool {
	for name, value := range selectors {
		if labels[name] != value {
			return false
		}
	}

	return true
}

// Reload Fluent Bit to apply the configuration.
// Currently the only way to reload the configuration when Fluent Bit is installed as a package
// is to restart it, see https://github.com/fluent/fluent-bit/issues/365 for updated information.
func reloadFluentBit(ctx context.Context, runner *gloutonexec.Runner) error {
	runOption := gloutonexec.Option{RunAsRoot: true}

	// Skip reloading on systems without systemctl.
	// In Docker and Kubernetes, Fluent Bit will detect the config change and reload by itself.
	if _, err := runner.LookPath("systemctl", runOption); err != nil {
		logger.V(2).Printf("Skipping Fluent Bit reload because systemctl is not present: %s", err)

		return nil
	}

	_, err := runner.Run(ctx, runOption, "systemctl", "restart", "bleemeo-agent-logs")
	if err != nil {
		if exitErr := &(exec.ExitError{}); errors.As(err, &exitErr) {
			err = fmt.Errorf("%w: %s", err, string(exitErr.Stderr))
		}

		return fmt.Errorf("failed to restart Fluent Bit: %w", err)
	}

	return nil
}

// Create a Prometheus scrapper to retrieve the Fluent Bit metrics.
func (m *Manager) createFluentBitScrapper() error {
	// In Docker and Kubernetes, the Fluent Bit URL is empty because
	// labels are used on the Fluent Bit container or pod instead.
	if m.config.FluentBitURL == "" {
		return nil
	}

	fluentbitURL, err := url.Parse(m.config.FluentBitURL)
	if err != nil {
		return fmt.Errorf("parse Fluent Bit URL: %w", err)
	}

	promScrapper := scrapper.New(fluentbitURL, nil)

	_, err = m.registry.RegisterGatherer(
		registry.RegistrationOption{
			Description: "Prom exporter for fluentbit " + promScrapper.URL.String(),
		},
		promScrapper,
	)
	if err != nil {
		return fmt.Errorf("register fluenbit scrapper: %w", err)
	}

	return nil
}

// PromQLRulesFromInputs returns the PromQL rules for an input.
func PromQLRulesFromInputs(inputs []config.LogInput) map[string]string {
	rules := make(map[string]string, len(inputs))

	// fluentbit_output_proc_records_total is a counter which resets every time Fluent Bit
	// restarts, we need to apply a rate so it can be usable. Then we remove the "name"
	// label which is duplicated with the new metric name ("__name__") using a sum.
	const rule = `sum(rate(fluentbit_output_proc_records_total{name="%s"}[1m])) without (name,scrape_instance,scrape_job)`

	for _, input := range inputs {
		for _, filter := range input.Filters {
			rules[filter.Metric] = fmt.Sprintf(rule, filter.Metric)
		}
	}

	return rules
}

func (m *Manager) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("fluent-bit.txt")
	if err != nil {
		return err
	}

	m.l.Lock()
	defer m.l.Unlock()

	fmt.Fprintf(file, "Loaded %d inputs.\n\n", len(m.loadedInputs))

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(m.loadedInputs)
}
