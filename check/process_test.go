package check

import (
	"context"
	"glouton/facts"
	"glouton/types"
	"testing"
	"time"
)

type mockProcessProvider struct{}

func (ps mockProcessProvider) Processes(ctx context.Context, maxAge time.Duration) (processes map[int]facts.Process, err error) {
	procs := map[int]facts.Process{
		354: {
			CmdLine: "",
			Status:  facts.ProcessStatusIOWait,
		},
		5795: {
			CmdLine: "postgres -c log_temp_files=0 -c shared_buffers=64MB",
			Status:  facts.ProcessStatusSleeping,
		},
		5642: {
			CmdLine: "/usr/lib/firefox/firefox -contentproc -childID 18",
			Status:  facts.ProcessStatusRunning,
		},
		7568: {
			CmdLine: "/usr/bin/pulseaudio --daemonize=no --log-target=journal",
			Status:  facts.ProcessStatusZombie,
		},
		9565: {
			CmdLine: "/usr/bin/containerd-shim-runc-v2 -namespace moby -id 8848c0d57022221092f275da20f0127f5a7d3892dcf86765ea8a084e8e72f687 -address /run/containerd/containerd.sock",
			Status:  facts.ProcessStatusZombie,
		},
		9572: {
			CmdLine: "/usr/bin/containerd-shim-runc-v2 -namespace moby -id 1ce70b670dfaa585eda0e6690328ea091813e46361edd2174bd0b04ece026573 -address /run/containerd/containerd.sock",
			Status:  facts.ProcessStatusZombie,
		},
		10067: {
			CmdLine: "/usr/bin/containerd-shim-runc-v2 -namespace moby -id 04b809455c35a0ae87f1f8475117bed6425585b9303c3fdb591c197dc2dd7923 -address /run/containerd/containerd.sock",
			Status:  facts.ProcessStatusIOWait,
		},
	}

	return procs, nil
}

func Test_processMainCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		matchProcess   string
		expectedStatus types.StatusDescription
	}{
		{
			name:         "ok-simple",
			matchProcess: "postgres",
			expectedStatus: types.StatusDescription{
				CurrentStatus:     types.StatusOk,
				StatusDescription: "Process found: postgres -c log_temp_files=0 -c shared_buffers=64MB",
			},
		},
		{
			name:         "ok-regex",
			matchProcess: "^/usr/lib/firefox/firefox -contentproc -childID 18$",
			expectedStatus: types.StatusDescription{
				CurrentStatus:     types.StatusOk,
				StatusDescription: "Process found: /usr/lib/firefox/firefox -contentproc -childID 18",
			},
		},
		{
			name:         "ok-with-zombies",
			matchProcess: `/usr/bin/containerd-shim-runc-v2 -namespace moby -id \w+ -address /run/containerd/containerd.sock`,
			expectedStatus: types.StatusDescription{
				CurrentStatus:     types.StatusOk,
				StatusDescription: "Process found: /usr/bin/containerd-shim-runc-v2 -namespace moby -id 04b809455c35a0ae87f1f8475117bed6425585b9303c3fdb591c197dc2dd7923 -address /run/containerd/containerd.sock",
			},
		},
		{
			name:         "critical-not-found",
			matchProcess: "redis",
			expectedStatus: types.StatusDescription{
				CurrentStatus:     types.StatusCritical,
				StatusDescription: "No process matched",
			},
		},
		{
			name:         "critical-zombie",
			matchProcess: "pulseaudio .* --log-target=journal",
			expectedStatus: types.StatusDescription{
				CurrentStatus:     types.StatusCritical,
				StatusDescription: "Process found in zombie state: /usr/bin/pulseaudio --daemonize=no --log-target=journal",
			},
		},
	}

	for _, test := range tests {
		test := test

		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			pc, err := NewProcess(test.matchProcess, nil, types.MetricAnnotations{}, nil, mockProcessProvider{})
			if err != nil {
				t.Errorf("Failed to create process: %v", err)
			}

			status := pc.processMainCheck(context.Background())
			if status.CurrentStatus != test.expectedStatus.CurrentStatus {
				t.Errorf("Expected status %v, got %v", test.expectedStatus.CurrentStatus, status.CurrentStatus)
			} else if status.StatusDescription != test.expectedStatus.StatusDescription {
				t.Errorf("Expected status description %v, got %v", test.expectedStatus.StatusDescription, status.StatusDescription)
			}
		})
	}
}