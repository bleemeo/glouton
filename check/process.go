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
	"regexp"
	"time"

	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
)

// Processes are updated only if they are older than processMaxAge.
const processMaxAge = 10 * time.Second

type processProvider interface {
	Processes(ctx context.Context, maxAge time.Duration) (processes map[int]facts.Process, err error)
}

type ProcessCheck struct {
	*baseCheck
	ps           processProvider
	processRegex *regexp.Regexp
}

func NewProcess(
	matchProcess string,
	labels map[string]string,
	annotations types.MetricAnnotations,
	ps processProvider,
) (*ProcessCheck, error) {
	processRegex, err := regexp.Compile(matchProcess)
	if err != nil {
		return nil, fmt.Errorf("failed to compile regex %s: %w", matchProcess, err)
	}

	pc := ProcessCheck{
		ps:           ps,
		processRegex: processRegex,
	}

	pc.baseCheck = newBase("", nil, false, pc.processMainCheck, labels, annotations)

	return &pc, nil
}

// processMainCheck returns StatusOk if at least one of the process that matched wasn't in
// a zombie state, else it returns StatusCritical.
func (pc *ProcessCheck) processMainCheck(ctx context.Context) types.StatusDescription {
	procs, err := pc.ps.Processes(ctx, processMaxAge)
	if err != nil {
		logger.V(1).Printf("Failed to get processes: %v", err)
	}

	var zombieProc facts.Process

	for _, proc := range procs {
		if pc.processRegex.MatchString(proc.CmdLine) {
			if proc.Status == facts.ProcessStatusZombie {
				zombieProc = proc

				continue
			}

			return types.StatusDescription{
				CurrentStatus:     types.StatusOk,
				StatusDescription: "Process found: " + proc.CmdLine,
			}
		}
	}

	if zombieProc.CmdLine != "" {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: "Process found in zombie state: " + zombieProc.CmdLine,
		}
	}

	return types.StatusDescription{
		CurrentStatus:     types.StatusCritical,
		StatusDescription: "No process matched",
	}
}
