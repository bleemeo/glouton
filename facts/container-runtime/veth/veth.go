package veth

import (
	"context"
	"errors"
	"fmt"
	"glouton/facts"
	"glouton/facts/container-runtime/containerd"
	"glouton/facts/container-runtime/docker"
	"glouton/facts/container-runtime/merge"
	"glouton/facts/container-runtime/types"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Provider provides a mapping between containers and host network interfaces.
type Provider struct {
	HostRootPath string

	// Keep container and interface mapping in cache.
	l                          sync.Mutex
	containerIDByInterfaceName map[string]string
	lastRefreshAt              time.Time
}

// getContainers returns all running containers PIDs.
func (vp *Provider) getContainers(ctx context.Context, maxAge time.Duration) ([]facts.Container, error) {
	isContainerIgnored := func(c facts.Container) bool { return false }

	dockerRuntime := &docker.Docker{
		DockerSockets:      docker.DefaultAddresses(vp.HostRootPath),
		IsContainerIgnored: isContainerIgnored,
	}

	containerdRuntime := &containerd.Containerd{
		Addresses:          containerd.DefaultAddresses(vp.HostRootPath),
		IsContainerIgnored: isContainerIgnored,
	}

	containerRuntime := &merge.Runtime{
		Runtimes: []types.RuntimeInterface{
			dockerRuntime,
			containerdRuntime,
		},
		ContainerIgnored: isContainerIgnored,
	}

	containers, err := containerRuntime.Containers(ctx, maxAge, false)
	if err != nil {
		return nil, err
	}

	return containers, nil
}

// parseOutput parses the output of glouton-veths.
// The output is expected with the format "pid: index" on each line.
// It returns a map of interface indexes on the host indexed by the containers PIDs.
func (vp *Provider) parseOutput(output string) map[int]int {
	lines := strings.Split(output, "\n")
	interfaceIndexByPID := make(map[int]int, len(lines))

	for _, line := range lines {
		res := strings.Split(line, ": ")
		if len(res) < 2 {
			continue
		}

		pid, err := strconv.Atoi(res[0])
		if err != nil {
			continue
		}

		interfaceIndex, err := strconv.Atoi(res[1])
		if err != nil {
			continue
		}

		interfaceIndexByPID[pid] = interfaceIndex
	}

	return interfaceIndexByPID
}

// Veths returns a map of containerIDs indexed by interface name.
// The interfaces are refreshed only if the cache is older than maxAge.
func (vp *Provider) Veths(maxAge time.Duration) (map[string]string, error) {
	vp.l.Lock()
	timeSinceRefresh := time.Since(vp.lastRefreshAt)
	cache := vp.containerIDByInterfaceName
	vp.l.Unlock()

	if timeSinceRefresh < maxAge {
		return cache, nil
	}

	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to list interfaces: %w", err)
	}

	interfaceNameByIndex := make(map[int]string)
	for _, iface := range interfaces {
		interfaceNameByIndex[iface.Index] = iface.Name
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	containers, err := vp.getContainers(ctx, maxAge)
	if err != nil {
		return nil, err
	}

	pids := make([]string, 0, len(containers))

	for _, container := range containers {
		if container.PID() == 0 {
			// The container is not running, skip it.
			continue
		}

		pids = append(pids, strconv.Itoa(container.PID()))
	}

	args := append([]string{"sudo", "-n", "glouton-veths"}, pids...)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec

	stdout, err := cmd.Output()
	if err != nil {
		stderr := ""
		if exitErr := &(exec.ExitError{}); errors.As(err, &exitErr) {
			stderr = string(exitErr.Stderr)
		}

		return nil, fmt.Errorf("%w: %s", err, stderr)
	}

	interfaceIndexByPID := vp.parseOutput(string(stdout))
	containerIDByInterfaceName := make(map[string]string, len(interfaceIndexByPID))

	for _, container := range containers {
		index, ok := interfaceIndexByPID[container.PID()]
		if !ok {
			continue
		}

		name, ok := interfaceNameByIndex[index]
		if !ok {
			continue
		}

		containerIDByInterfaceName[name] = container.ID()
	}

	// Refresh cache.
	vp.l.Lock()
	vp.containerIDByInterfaceName = containerIDByInterfaceName
	vp.lastRefreshAt = time.Now()
	vp.l.Unlock()

	return containerIDByInterfaceName, nil
}
