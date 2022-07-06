package veth

import (
	"context"
	"errors"
	"fmt"
	ctypes "glouton/facts/container-runtime/types"
	"glouton/logger"
	"glouton/types"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Provider provides a mapping between containers and host network interfaces.
type Provider struct {
	HostRootPath string
	Runtime      ctypes.RuntimeInterface

	// Keep container and interface mapping in cache.
	l                          sync.Mutex
	containerIDByInterfaceName map[string]string
}

type link struct {
	name      string
	index     int
	hasNSPeer bool
}

// ContainerID returns the ID of the container that owns the given interface.
// Returns en empty string if the interface doesn't belong to a container.
func (p *Provider) ContainerID(interfaceName string) (string, error) {
	p.l.Lock()
	defer p.l.Unlock()

	// Try to get the container ID from the cache.
	containerID, ok := p.containerIDByInterfaceName[interfaceName]
	if ok {
		return containerID, nil
	}

	if err := p.updateCache(); err != nil {
		logger.V(2).Printf("Failed to update veth cache: %s", err)

		// If we failed to update the cache, fallback on a simpler veth detection.
		// This can happen glouton doesn't have the the permission to run glouton-veths as root.
		if strings.HasPrefix(interfaceName, "veth") {
			return types.MissingContainerID, nil
		}
	}

	containerID, ok = p.containerIDByInterfaceName[interfaceName]
	if ok {
		return containerID, nil
	}

	// The interface is not in the cache after a refresh, this should not happen.
	logger.V(2).Printf("Could not find interface %s in cache", interfaceName)

	p.containerIDByInterfaceName[interfaceName] = ""

	return "", nil
}

// updateCache updates the veth cache.
func (p *Provider) updateCache() error {
	links, err := linkList()
	if err != nil {
		return err
	}

	// Initialize the cache with MissingContainerID for interfaces with a peer in another namespace.
	containerIDByInterfaceName := make(map[string]string, len(links))
	interfaceNameByIndex := make(map[int]string, len(links))

	for _, link := range links {
		if link.hasNSPeer {
			containerIDByInterfaceName[link.name] = types.MissingContainerID
		} else {
			containerIDByInterfaceName[link.name] = ""
		}

		interfaceNameByIndex[link.index] = link.name
	}

	// List container PIDs.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	containers, err := p.Runtime.Containers(ctx, time.Minute, true)
	if err != nil {
		return err
	}

	pids := make([]string, 0, len(containers))

	for _, container := range containers {
		if container.PID() == 0 {
			// The container is not running, skip it.
			continue
		}

		pids = append(pids, strconv.Itoa(container.PID()))
	}

	// Get the container interface indexes.
	// Use sudo only if not currently running as root.
	// The Glouton docker container uses busybox which doesn't have sudo.
	var args []string
	if os.Getuid() != 0 {
		args = append(args, "sudo", "-n")
	}

	args = append(args, append([]string{"/usr/lib/glouton/glouton-veths"}, pids...)...)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec

	stdout, err := cmd.Output()
	if err != nil {
		stderr := ""
		if exitErr := &(exec.ExitError{}); errors.As(err, &exitErr) {
			stderr = string(exitErr.Stderr)
		}

		return fmt.Errorf("%w: %s", err, stderr)
	}

	interfaceIndexByPID := parseOutput(string(stdout))

	// Multiple containers can have the same interface (for example using
	// --network container:another-container). We need to sort the containers
	// by creation date in descending order to make sure an interface is always
	// associated with the oldest container.
	sort.Slice(containers, func(i, j int) bool {
		return containers[i].CreatedAt().After(containers[j].CreatedAt())
	})

	// Update the cache with the container IDs found.
	for _, container := range containers {
		indexes, ok := interfaceIndexByPID[container.PID()]
		if !ok {
			continue
		}

		for _, index := range indexes {
			name, ok := interfaceNameByIndex[index]
			if !ok {
				continue
			}

			containerIDByInterfaceName[name] = container.ID()
		}
	}

	p.containerIDByInterfaceName = containerIDByInterfaceName

	return nil
}

// parseOutput parses the output of glouton-veths.
// The output is expected with the format "pid: index1 [index2]..." on each line.
// It returns a map of interface indexes on the host indexed by the containers PIDs.
func parseOutput(output string) map[int][]int {
	lines := strings.Split(output, "\n")
	interfaceIndexByPID := make(map[int][]int, len(lines))

	for _, line := range lines {
		// line = "pid: index1 index2 ..."
		res := strings.Split(line, ": ")
		if len(res) < 2 {
			continue
		}

		// res = [pid, index1, index2, ...]
		pid, err := strconv.Atoi(res[0])
		if err != nil {
			continue
		}

		for _, strIndex := range strings.Fields(res[1]) {
			index, err := strconv.Atoi(strIndex)
			if err != nil {
				continue
			}

			interfaceIndexByPID[pid] = append(interfaceIndexByPID[pid], index)
		}
	}

	return interfaceIndexByPID
}
