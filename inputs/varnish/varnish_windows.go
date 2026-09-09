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

//go:build windows

package varnish

import (
	"context"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/influxdata/telegraf"
)

// Runner runs a command. Only declared so New keeps one signature across platforms;
// nothing on Windows uses it.
type Runner interface {
	Run(ctx context.Context, option gloutonexec.Option, name string, arg ...string) ([]byte, error)
}

// New returns a Varnish input. Varnish isn't supported on Windows, telegraf's
// own varnish plugin is a no-op stub on this platform.
func New(_ Runner, _ int, _ string) (telegraf.Input, registry.RegistrationOption, error) {
	return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput
}
