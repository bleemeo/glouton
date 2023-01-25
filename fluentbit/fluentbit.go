package fluentbit

import (
	"context"
	"errors"
	"fmt"
	"glouton/config"
	"glouton/logger"
	"glouton/prometheus/registry"
	"glouton/prometheus/scrapper"
	"glouton/types"
	"net/url"
	"os"
	"os/exec"

	"github.com/prometheus/client_golang/prometheus"
)

// TODO: Do we want to support Windows?
const configFile = "/var/lib/glouton/fluent-bit.conf"

type registerer interface {
	RegisterGatherer(opt registry.RegistrationOption, gatherer prometheus.Gatherer) (int, error)
}

// Load the config to Fluentbit.
func Load(ctx context.Context, config config.Log, reg registerer) error {
	// Write Fluenbit config.
	fluentbitConfig := inputsToFluentbitConfig(config.Inputs)

	//nolint:gosec // The file needs to be readable by Fluentbit.
	err := os.WriteFile(configFile, []byte(fluentbitConfig), 0o644)
	if err != nil {
		return fmt.Errorf("write fluentbit config: %w", err)
	}

	// Create Prometheus scrapper.
	fluentbitURL, err := url.Parse(config.FluentbitURL)
	if err != nil {
		return fmt.Errorf("parse fluentbit URL: %w", err)
	}

	promScrapper := scrapper.New(fluentbitURL, nil)

	_, err = reg.RegisterGatherer(
		registry.RegistrationOption{
			Description: "Prom exporter " + promScrapper.URL.String(),
			Rules:       promQLRulesFromInputs(config.Inputs),
		},
		promScrapper,
	)
	if err != nil {
		return fmt.Errorf("register fluenbit scrapper: %w", err)
	}

	// Reload Fluentbit to apply the configuration.
	// Do it in a goroutine to not block the agent startup.
	go reloadConfig(ctx)

	return nil
}

// Return PromQL rules to rename the Fluentbit metrics to the input metric names.
func promQLRulesFromInputs(inputs []config.LogInput) []types.SimpleRule {
	rules := make([]types.SimpleRule, 0, len(inputs))

	for _, input := range inputs {
		for _, filter := range input.Filters {
			rules = append(rules, types.SimpleRule{
				TargetName:  filter.Metric,
				PromQLQuery: fmt.Sprintf(`fluentbit_output_proc_records_total{name="%s"}`, filter.Metric),
			})
		}
	}

	return rules
}

// Reload Fluentbit config.
// Currently the only way to reload the configuration when Fluent Bit is installed as a package
// is to restart it, see https://github.com/fluent/fluent-bit/issues/365 for updated information.
func reloadConfig(ctx context.Context) {
	// Skip reloading on systems without systemctl.
	// In Docker and Kubernetes, Fluent Bit will detect the config change and reload by itself.
	if _, err := os.Stat("/usr/bin/systemctl"); err != nil {
		logger.V(2).Printf("Skipping Fluent Bit reload because systemctl is not present: %s", err)

		return
	}

	_, err := exec.CommandContext(ctx, "sudo", "-n", "/usr/bin/systemctl", "restart", "fluent-bit").Output()
	if err != nil {
		stderr := err.Error()
		if exitErr := &(exec.ExitError{}); errors.As(err, &exitErr) {
			stderr += ": " + string(exitErr.Stderr)
		}

		logger.V(0).Printf("Failed to restart Fluent Bit: %s", stderr)
	}
}
