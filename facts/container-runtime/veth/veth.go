// Copyright 2015-2025 Bleemeo
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

package veth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	ctypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"
)

const cacheUpdateInterval = 10 * time.Minute

// Provider provides a mapping between containers and host network interfaces.
type Provider struct {
	HostRootPath string
	Runner       *gloutonexec.Runner
	Runtime      ctypes.RuntimeInterface

	// Keep container and interface mapping in cache.
	l                          sync.Mutex
	containerIDByInterfaceName map[string]string
	lastUpdateAt               time.Time
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

	// The cache is refreshed periodically to recover from temporary errors.
	if time.Since(p.lastUpdateAt) < cacheUpdateInterval {
		// Try to get the container ID from the cache.
		containerID, ok := p.containerIDByInterfaceName[interfaceName]
		if ok {
			return containerID, nil
		}
	}

	p.lastUpdateAt = time.Now()

	if err := p.updateCache(); err != nil {
		// The cache might still have been initialized with MissingContainerID
		// for interfaces with a peer in another namespace.
		logger.V(1).Printf("Failed to update the veth cache: %s", err)
	}

	containerID, ok := p.containerIDByInterfaceName[interfaceName]
	if ok {
		return containerID, nil
	}

	// If we fail to get the interface after a cache update, fallback on a simpler veth detection.
	if strings.HasPrefix(interfaceName, "veth") {
		containerID = types.MissingContainerID
	} else {
		containerID = ""
	}

	if p.containerIDByInterfaceName == nil {
		p.containerIDByInterfaceName = make(map[string]string)
	}

	// Save container ID in cache, even if it might be wrong.
	// The cache is refreshed periodically so this is not a problem.
	p.containerIDByInterfaceName[interfaceName] = containerID

	return containerID, nil
}

// updateCache updates the veth cache.
func (p *Provider) updateCache() error {
	logger.V(2).Println("Updating the veth cache")

	p.containerIDByInterfaceName = nil

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

	p.containerIDByInterfaceName = containerIDByInterfaceName

	// List container PIDs.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	containers, err := p.Runtime.Containers(ctx, time.Minute, false)
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
	stdout, err := p.Runner.Run(ctx, gloutonexec.Option{RunAsRoot: true}, "/usr/lib/glouton/glouton-veths", pids...)
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

		for strIndex := range strings.FieldsSeq(res[1]) {
			index, err := strconv.Atoi(strIndex)
			if err != nil {
				continue
			}

			interfaceIndexByPID[pid] = append(interfaceIndexByPID[pid], index)
		}
	}

	return interfaceIndexByPID
}

func (p *Provider) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	p.l.Lock()
	defer p.l.Unlock()

	file, err := archive.Create("interface-container-association.txt")
	if err != nil {
		return err
	}

	cacheIndent, err := json.MarshalIndent(p.containerIDByInterfaceName, "", "  ")
	if err != nil {
		return err
	}

	fmt.Fprintf(file, "Last update at %v\n", p.lastUpdateAt)
	fmt.Fprintln(file, string(cacheIndent))

	return nil
}
