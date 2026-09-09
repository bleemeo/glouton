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

//go:build !windows

package varnish

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"
	"unsafe"

	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/influxdata/telegraf/config"
	"github.com/influxdata/telegraf/plugins/inputs/varnish"
)

// runnerFields are the plugin's two command runners: one for varnishstat, one for
// varnishadm (only used when reading backend metrics, which MetricVersion 1 doesn't).
var runnerFields = []string{"run", "admRun"} //nolint:gochecknoglobals

var errNoRunnerField = errors.New("telegraf's varnish plugin has no command runner to replace")

// Runner runs a command. Implemented by gloutonexec.Runner, and by a fake in the tests.
type Runner interface {
	Run(ctx context.Context, option gloutonexec.Option, name string, arg ...string) ([]byte, error)
}

// useGloutonRunner makes the plugin run varnishstat through Glouton's command runner
// instead of exec-ing it itself.
//
// What this buys is a varnishstat that exists at all, since the agent image contains
// none: the runner chroots into the mount namespace holding one, the host's for a Varnish
// installed on the machine and the container's own for a containerised one. This is what
// inputs/smart already does for smartctl (see its run_cmd.go, which reaches the same
// private hook with go:linkname because there the plugin keeps it in a package variable
// rather than a field).
//
// Sudo is left to the runner: it only prepends one when Glouton isn't already root, which
// is why the sudoers rule keeps matching for a host install (sudo -n /usr/bin/varnishstat
// -1) while the containerized agent, running as root, needs no rule at all.
func useGloutonRunner(input *varnish.Varnish, runner Runner, containerPID int) error {
	replacement := reflect.ValueOf(runCmd(runner, containerPID))
	value := reflect.ValueOf(input).Elem()

	for _, name := range runnerFields {
		field := value.FieldByName(name)
		if !field.IsValid() {
			return fmt.Errorf("%w: field %q is gone", errNoRunnerField, name)
		}

		if !replacement.Type().AssignableTo(field.Type()) {
			return fmt.Errorf("%w: field %q is a %s", errNoRunnerField, name, field.Type())
		}

		// The field is private, so it can only be written through its address -- the same
		// way inputs/nats reaches the plugin's http client. An unnamed func type is
		// assignable to the field's named one, so no reflect.MakeFunc is needed.
		reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(replacement)
	}

	return nil
}

// runCmd returns the function the plugin calls in place of its own varnishRunner. Its
// signature is the plugin's private "runner" type, which cannot be named here.
//
// containerPID, when non-zero, runs the varnishstat of that container rather than the
// machine's -- see New.
func runCmd(runner Runner, containerPID int) func(string, bool, []string, config.Duration) (*bytes.Buffer, error) {
	return func(binary string, useSudo bool, args []string, timeout config.Duration) (*bytes.Buffer, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout))
		defer cancel()

		// RunAsRoot rather than the plugin's own sudo handling, so that being root (in the
		// agent container, or on a host where Glouton runs as root) skips sudo instead of
		// needing it installed. GraceDelay matches inputs/smart: varnishstat gets a chance
		// to exit on its own before being killed.
		//
		// The two namespace options are exclusive: RunOnHost only when there is no
		// container to go into, since asking for both would be asking for opposite things.
		output, err := runner.Run(
			ctx,
			gloutonexec.Option{ //nolint:exhaustruct
				RunAsRoot:      useSudo,
				RunOnHost:      containerPID == 0,
				InContainerPID: containerPID,
				GraceDelay:     5 * time.Second,
			},
			binary,
			args...,
		)
		if err != nil {
			return bytes.NewBuffer(output), fmt.Errorf("running %s %v: %w", binary, args, err)
		}

		return bytes.NewBuffer(output), nil
	}
}
