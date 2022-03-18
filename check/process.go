package check

import (
	"context"
	"fmt"
	"glouton/facts"
	"glouton/inputs"
	"glouton/logger"
	"glouton/types"
	"regexp"
	"time"
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
	acc inputs.AnnotationAccumulator,
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

	pc.baseCheck = newBase("", nil, false, pc.processMainCheck, labels, annotations, acc)

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
				StatusDescription: fmt.Sprintf("Process found: %s", proc.CmdLine),
			}
		}
	}

	if zombieProc.CmdLine != "" {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: fmt.Sprintf("Process found in zombie state: %s", zombieProc.CmdLine),
		}
	}

	return types.StatusDescription{
		CurrentStatus:     types.StatusCritical,
		StatusDescription: "No process matched",
	}
}
