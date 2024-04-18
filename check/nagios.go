// Copyright 2015-2023 Bleemeo
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

package check

import (
	"context"
	"fmt"
	"github.com/bleemeo/glouton/types"
	"os/exec"

	"github.com/google/shlex"
)

// NagiosCheck perform a Nagios check.
type NagiosCheck struct {
	*baseCheck

	nagiosCommand string
}

// NewNagios create a new Nagios check.
//
// For each persistentAddresses (in the format "IP:port") this checker will maintain a TCP connection open, if broken (and unable to re-open),
// the check will be immediately run.
func NewNagios(
	nagiosCommand string,
	persistentAddresses []string,
	persistentConnection bool,
	labels map[string]string,
	annotations types.MetricAnnotations,
) *NagiosCheck {
	nc := &NagiosCheck{
		nagiosCommand: nagiosCommand,
	}

	var mainTCPAddress string

	if len(persistentAddresses) > 0 {
		mainTCPAddress = persistentAddresses[0]
	}

	nc.baseCheck = newBase(mainTCPAddress, persistentAddresses, persistentConnection, nc.nagiosMainCheck, labels, annotations)

	return nc
}

func (nc *NagiosCheck) nagiosMainCheck(context.Context) types.StatusDescription {
	part, err := shlex.Split(nc.nagiosCommand)
	if err != nil {
		return types.StatusDescription{
			CurrentStatus:     types.StatusUnknown,
			StatusDescription: fmt.Sprintf("UNKNOWN - failed to parse command line: %v", err),
		}
	}

	if len(part) == 0 {
		return types.StatusDescription{
			CurrentStatus:     types.StatusUnknown,
			StatusDescription: fmt.Sprintf("UNKNOWN - command %#v looks empty", nc.nagiosCommand),
		}
	}

	cmd := exec.Command(part[0], part[1:]...) //nolint:gosec
	output, err := cmd.CombinedOutput()
	result := types.StatusDescription{
		CurrentStatus:     types.StatusOk,
		StatusDescription: string(output),
	}

	if exitError, ok := err.(*exec.ExitError); ok {
		result.CurrentStatus = types.FromNagios(exitError.ExitCode())
	} else if err != nil {
		result.CurrentStatus = types.StatusUnknown
		result.StatusDescription = fmt.Sprintf("UNKNOWN - unable to run command %#v: %v", nc.nagiosCommand, err)
	}

	return result
}
