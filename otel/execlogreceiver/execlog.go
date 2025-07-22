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

// Package execlogreceiver implements a receiver that can be used by the
// OpenTelemetry collector to receive logs from the output of a program
// using the stanza log agent
package execlogreceiver

import (
	"time"

	"github.com/bleemeo/glouton/otel/execlogreceiver/execlog"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/adapter"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/receiver"
)

// Code is inspired by a mix of namedpipereceiver & filelogreceiver from OpenTelemetry Collector.

var Type = component.MustNewType("execlog") //nolint:gochecknoglobals

const (
	LogsStability = component.StabilityLevelBeta
)

// NewFactory creates a factory for filelog receiver.
func NewFactory() receiver.Factory {
	return adapter.NewFactory(ReceiverType{}, LogsStability)
}

// ReceiverType implements stanza.LogReceiverType
// to create a process reader receiver.
type ReceiverType struct{}

// Type is the receiver type.
func (f ReceiverType) Type() component.Type {
	return Type
}

// CreateDefaultConfig creates a config with type and version.
func (f ReceiverType) CreateDefaultConfig() component.Config {
	return createDefaultConfig()
}

func createDefaultConfig() *ExecLogConfig {
	c := &ExecLogConfig{
		BaseConfig: adapter.BaseConfig{
			Operators: []operator.Config{},
		},
		InputConfig: *execlog.NewConfig(),
	}

	// We can't use NewDefaultConfig() because consumerretry.NewDefaultConfig() is inside a internal package
	c.BaseConfig.RetryOnFailure.Enabled = true
	c.BaseConfig.RetryOnFailure.InitialInterval = 1 * time.Second
	c.BaseConfig.RetryOnFailure.MaxInterval = 30 * time.Second
	c.BaseConfig.RetryOnFailure.MaxElapsedTime = 5 * time.Minute

	return c
}

// BaseConfig gets the base config from config, for now.
func (f ReceiverType) BaseConfig(cfg component.Config) adapter.BaseConfig {
	return cfg.(*ExecLogConfig).BaseConfig //nolint:forcetypeassert
}

// ExecLogConfig defines configuration for the filelog receiver.
type ExecLogConfig struct {
	adapter.BaseConfig `mapstructure:",squash"`

	InputConfig execlog.Config `mapstructure:",squash"`
}

// InputConfig unmarshals the input operator.
func (f ReceiverType) InputConfig(cfg component.Config) operator.Config {
	return operator.NewConfig(&cfg.(*ExecLogConfig).InputConfig) //nolint:forcetypeassert
}
