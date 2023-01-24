package fluentbit

import (
	"context"
	"fmt"
	"glouton/config"
	"glouton/prometheus/registry"
	"glouton/prometheus/scrapper"
	"glouton/types"
	"net/url"
	"os"

	"github.com/prometheus/client_golang/prometheus"
)

const configFile = "/var/lib/glouton/fluentbit.conf"

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

	return nil
}

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
