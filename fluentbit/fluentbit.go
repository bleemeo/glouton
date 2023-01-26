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
const (
	configDir  = "/var/lib/glouton/fluent-bit"
	configFile = configDir + "/fluent-bit.conf"
)

type registerer interface {
	RegisterGatherer(opt registry.RegistrationOption, gatherer prometheus.Gatherer) (int, error)
}

// Load the Fluent Bit config and start a scrapper.
func Load(ctx context.Context, cfg config.Log, reg registerer) error {
	err := writeFluentBitConfig(cfg.Inputs)
	if err != nil {
		return err
	}

	err = createFluentBitScrapper(cfg, reg)
	if err != nil {
		return err
	}

	// Do the reload in a goroutine to not block the agent startup.
	go reloadFluentBit(ctx)

	return nil
}

// Write the Fluent Config.
func writeFluentBitConfig(inputs []config.LogInput) error {
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

// Create a Prometheus scrapper to retrieve the Fluent Bit metrics.
func createFluentBitScrapper(cfg config.Log, reg registerer) error {
	// In Docker and Kubernetes, the Fluent Bit URL is empty because
	// labels are used on the Fluent Bit container or pod instead.
	if cfg.FluentBitURL == "" {
		return nil
	}

	fluentbitURL, err := url.Parse(cfg.FluentBitURL)
	if err != nil {
		return fmt.Errorf("parse Fluent Bit URL: %w", err)
	}

	promScrapper := scrapper.New(fluentbitURL, nil)

	_, err = reg.RegisterGatherer(
		registry.RegistrationOption{
			Description: "Prom exporter " + promScrapper.URL.String(),
			Rules:       promQLRulesFromInputs(cfg.Inputs),
		},
		promScrapper,
	)
	if err != nil {
		return fmt.Errorf("register fluenbit scrapper: %w", err)
	}

	return nil
}

// Return PromQL rules to rename the Fluent Bit metrics to the input metric names.
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

// Reload Fluent Bit to apply the configuration.
// Currently the only way to reload the configuration when Fluent Bit is installed as a package
// is to restart it, see https://github.com/fluent/fluent-bit/issues/365 for updated information.
func reloadFluentBit(ctx context.Context) {
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
