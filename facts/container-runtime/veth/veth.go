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
	"time"
)

// VethProvider provides a mapping between containers and host network interfaces.
type VethProvider struct {
	HostRootPath string
}

// getContainers returns all running containers PIDs.
func (vp VethProvider) getContainers(ctx context.Context) ([]facts.Container, error) {
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

	containers, err := containerRuntime.Containers(ctx, time.Minute, false)
	if err != nil {
		return nil, err
	}

	return containers, nil
}

// parseOutput parses the output of glouton-veths.
// The output is expected with the format "pid: index" on each line.
// It returns a map of interface indexes on the host indexed by the containers PIDs.
func (vp VethProvider) parseOutput(output string) map[int]int {
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
func (vp VethProvider) Veths(ctx context.Context) (map[string]string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to list interfaces: %w", err)
	}

	interfaceNameByIndex := make(map[int]string)
	for _, iface := range interfaces {
		interfaceNameByIndex[iface.Index] = iface.Name
	}

	containers, err := vp.getContainers(ctx)
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

	args := strings.Fields(fmt.Sprintf("sudo -n glouton-veths %s", pids))

	cmd := exec.Command(args[0], args[1:]...)

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

	return containerIDByInterfaceName, nil
}
