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
	"context"
	"errors"
	"testing"

	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/google/go-cmp/cmp"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/varnish"
)

// fakeRunner records what it was asked to run and answers with a canned varnishstat -1
// output.
type fakeRunner struct {
	calls  []fakeCall
	output []byte
	err    error
}

type fakeCall struct {
	option gloutonexec.Option
	name   string
	args   []string
}

func (r *fakeRunner) Run(_ context.Context, option gloutonexec.Option, name string, arg ...string) ([]byte, error) {
	r.calls = append(r.calls, fakeCall{option: option, name: name, args: arg})

	return r.output, r.err
}

// TestPluginKeepsItsRunnerFields is the guard against a Telegraf upgrade renaming or
// retyping the private fields useGloutonRunner writes to. Nothing else would notice:
// New only logs when it can't find them, and the plugin then silently goes back to
// exec-ing varnishstat inside the agent's own filesystem, where there is none.
func TestPluginKeepsItsRunnerFields(t *testing.T) {
	input, ok := telegraf_inputs.Inputs["varnish"]
	if !ok {
		t.Skip("Telegraf was built without its varnish plugin")
	}

	varnishInput, ok := input().(*varnish.Varnish)
	if !ok {
		t.Fatalf("telegraf's varnish plugin is a %T", input())
	}

	if err := useGloutonRunner(varnishInput, &fakeRunner{}); err != nil { //nolint:exhaustruct
		t.Errorf("useGloutonRunner() = %v\n"+
			"Telegraf's varnish plugin changed: find what replaced %v in its Varnish struct "+
			"(plugins/inputs/varnish/varnish.go) and update runnerFields, or varnishstat will "+
			"be run inside the agent's filesystem again", err, runnerFields)
	}
}

// TestNewUsesTheCommandRunner checks that a gather really goes through Glouton's command
// runner, with the options that make it reach the host's varnishstat.
func TestNewUsesTheCommandRunner(t *testing.T) {
	runner := &fakeRunner{ //nolint:exhaustruct
		// Two MAIN counters, in the "field value rate description" shape varnishstat -1
		// prints. Enough for the plugin's parser to produce a metric.
		output: []byte("MAIN.cache_hit    1000    1.00 Cache hits\nMAIN.uptime    3600    1.00 Uptime\n"),
	}

	input, _, err := New(runner)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	acc := &internal.StoreAccumulator{}
	gatherErr := input.Gather(acc)

	// Checked before the error: a gather that went around the runner exec's the real
	// varnishstat, so what it returns depends on the machine the test runs on, while
	// "it never called the runner" is the failure that actually matters.
	if len(runner.calls) == 0 {
		t.Fatalf("the gather did not go through Glouton's command runner (Gather() = %v)", gatherErr)
	}

	if gatherErr != nil {
		t.Fatalf("Gather() = %v", gatherErr)
	}

	call := runner.calls[0]

	if call.name != "/usr/bin/varnishstat" {
		t.Errorf("ran %q, want /usr/bin/varnishstat", call.name)
	}

	// -1 is the machine-readable listing, and the only form the packaged sudoers rule
	// allows (packaging/common/glouton.sudoers).
	if !cmp.Equal(call.args, []string{"-1"}) {
		t.Errorf("ran with %v, want [-1]", call.args)
	}

	// RunOnHost is the whole point: without it the command is looked up in the agent's
	// filesystem, which carries no varnishstat.
	if !call.option.RunOnHost {
		t.Error("RunOnHost is not set, the host's varnishstat won't be used")
	}

	// The runner is asked for root and decides whether a sudo is needed, rather than the
	// plugin prepending one that the agent image doesn't even contain.
	if !call.option.RunAsRoot {
		t.Error("RunAsRoot is not set, a non-root Glouton would be denied the shared memory")
	}

	if len(acc.Measurement) == 0 {
		t.Error("the runner's output produced no measurement, the reply isn't being parsed")
	}
}

var errCommandNotFound = errors.New("exec: varnishstat: not found")

// TestNewSurvivesARunnerError checks a failing varnishstat is reported as a gather error
// rather than a panic or a silent success.
func TestNewSurvivesARunnerError(t *testing.T) {
	runner := &fakeRunner{err: errCommandNotFound} //nolint:exhaustruct

	input, _, err := New(runner)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	acc := &internal.StoreAccumulator{}
	gatherErr := input.Gather(acc)

	if gatherErr == nil && len(acc.Errors) == 0 {
		t.Error("a failing varnishstat produced neither an error nor an accumulator error")
	}

	if len(acc.Measurement) != 0 {
		t.Errorf("a failing varnishstat still reported %v", acc.Measurement)
	}
}
