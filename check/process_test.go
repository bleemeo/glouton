package check

import (
	"context"
	"glouton/facts"
	"glouton/types"
	"regexp"
	"testing"
	"time"
)

type mockProcessProvider struct{}

func (ps mockProcessProvider) Processes(ctx context.Context, maxAge time.Duration) (processes map[int]facts.Process, err error) {
	procs := map[int]facts.Process{
		354:  {CmdLine: "", Status: facts.ProcessStatusIOWait},
		5795: {CmdLine: "postgres -c log_temp_files=0 -c shared_buffers=64MB", Status: facts.ProcessStatusSleeping},
		5642: {CmdLine: "/usr/lib/firefox/firefox -contentproc -childID 18", Status: facts.ProcessStatusRunning},
		7568: {CmdLine: "/usr/bin/pulseaudio --daemonize=no --log-target=journal", Status: facts.ProcessStatusZombie},
	}

	return procs, nil
}

func Test_processMainCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		matchProcess   string
		expectedStatus types.Status
	}{
		{
			matchProcess:   "postgres",
			expectedStatus: types.StatusOk,
		},
		{
			matchProcess:   "^/usr/lib/firefox/firefox -contentproc -childID 18$",
			expectedStatus: types.StatusOk,
		},
		{
			matchProcess:   "redis",
			expectedStatus: types.StatusCritical,
		},
		{
			matchProcess:   "pulseaudio .* --log-target=journal",
			expectedStatus: types.StatusCritical,
		},
	}

	for _, test := range tests {
		test := test

		t.Run(test.matchProcess, func(t *testing.T) {
			t.Parallel()

			reg := regexp.MustCompile(test.matchProcess)
			pc := NewProcess(reg, nil, types.MetricAnnotations{}, nil, mockProcessProvider{})

			statusDesc := pc.processMainCheck(context.Background())
			if statusDesc.CurrentStatus != test.expectedStatus {
				t.Errorf("expected status %v, got %v", test.expectedStatus, statusDesc.CurrentStatus)
			}
		})
	}
}
