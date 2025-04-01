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

package execlog

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/cenkalti/backoff/v5"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/split"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/trim"
	"go.opentelemetry.io/collector/component"
)

const operatorType = "exec_input"

func init() { //nolint:gochecknoinits
	operator.Register(operatorType, func() operator.Builder { return NewConfig() })
}

// NewConfig creates a new input config with default values.
func NewConfig() *Config {
	return NewConfigWithID(operatorType)
}

// NewConfigWithID creates a new input config with default values.
func NewConfigWithID(operatorID string) *Config {
	return &Config{
		InputConfig: helper.NewInputConfig(operatorID, operatorType),
		BaseConfig: BaseConfig{
			Encoding:   "utf8",
			MaxLogSize: 1024 * 1024,
		},
	}
}

type Runner interface {
	StartWithPipes(ctx context.Context, option gloutonexec.Option, name string, arg ...string) (
		stdoutPipe, stderrPipe io.ReadCloser,
		wait func() error,
		err error,
	)
}

// Config is the configuration of a stdin input operator.
type Config struct {
	helper.InputConfig `mapstructure:",squash"`
	BaseConfig         `mapstructure:",squash"`
}

type BaseConfig struct {
	Argv          []string     `mapstructure:"path"`
	Encoding      string       `mapstructure:"encoding"`
	SplitConfig   split.Config `mapstructure:"multiline,omitempty"`
	TrimConfig    trim.Config  `mapstructure:",squash"`
	MaxLogSize    int          `mapstructure:"max_log_size"`
	CommandRunner Runner       `mapstructure:"command_runner"`
	RunAsRoot     bool         `mapstructure:"run_as_root"`
}

// Build will build a exec input operator.
func (c *Config) Build(set component.TelemetrySettings) (operator.Operator, error) {
	inputOperator, err := c.InputConfig.Build(set)
	if err != nil {
		return nil, err
	}

	enc, err := lookupEncoding(c.Encoding)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup encoding %q: %w", c.Encoding, err)
	}

	splitFunc, err := c.SplitConfig.Func(enc, true, c.BaseConfig.MaxLogSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create split function: %w", err)
	}

	backOff := backoff.NewExponentialBackOff()
	backOff.RandomizationFactor = 0.1
	backOff.MaxInterval = 30 * time.Second

	return &Input{
		InputOperator: inputOperator,

		commandRunner: c.CommandRunner,
		runAsRoot:     c.RunAsRoot,
		buffer:        make([]byte, c.BaseConfig.MaxLogSize),
		argv:          c.Argv,
		splitFunc:     splitFunc,
		trimFunc:      c.TrimConfig.Func(),
		backoff:       backOff,
	}, nil
}
