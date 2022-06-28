// This script lists the containers with their associated virtual interface on the host.
//
// To get the interface associated to a container, it uses nsenter:
// - Get the container PIDs (e.g. 1234)
//
// - Use nsenter to list the interfaces inside the container
// nsenter -t 1234 -n ip link show type veth
// 14: eth0@if15: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UP mode DEFAULT group default
// link/ether 02:42:ac:12:00:06 brd ff:ff:ff:ff:ff:ff link-netns ns-2622
//
// - Get the corresponding interface on the host.
// eth0@if15 means the container uses the interface with index 15 on the host.
// 15: veth95e9c75@if14: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue master br-48eb47945400 state UP group default
// -> this container is linked to the interface veth95e9c75.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"glouton/config"
	"glouton/facts"
	"glouton/facts/container-runtime/containerd"
	"glouton/facts/container-runtime/docker"
	"glouton/facts/container-runtime/merge"
	"glouton/facts/container-runtime/types"
	"glouton/logger"
	"net"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const vethFile = "/var/lib/glouton/veth.out"

var (
	errNoInterfaceMatched  = errors.New("no interface index matched")
	errContainerNotRunning = errors.New("not running")
)

// getContainers returns all running containers.
func getContainers(ctx context.Context) ([]facts.Container, error) {
	hostRootPath := "/"

	cfg := &config.Configuration{}
	if cfg.String("container.type") != "" {
		hostRootPath = cfg.String("df.host_mount_point")
	}

	isContainerIgnored := func(c facts.Container) bool { return false }

	dockerRuntime := &docker.Docker{
		DockerSockets:      docker.DefaultAddresses(hostRootPath),
		IsContainerIgnored: isContainerIgnored,
	}

	containerdRuntime := &containerd.Containerd{
		Addresses:          containerd.DefaultAddresses(hostRootPath),
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

// getContainerIfIndex returns the interface index inside a container.
func getContainerIfIndex(container facts.Container) (int, error) {
	if container.PID() == 0 {
		// The container is not running, skip it.
		return 0, errContainerNotRunning
	}

	cmd := fmt.Sprintf("nsenter -t %d -n ip link show", container.PID())
	args := strings.Fields(cmd)

	res := exec.Command(args[0], args[1:]...) //nolint:gosec

	stdout, err := res.Output()
	if err != nil {
		stderr := ""
		if exitErr := &(exec.ExitError{}); errors.As(err, &exitErr) {
			stderr = string(exitErr.Stderr)
		}

		return 0, fmt.Errorf("nsenter: %w: %s", err, stderr)
	}

	ifRegex := regexp.MustCompile(`.*eth0@if(\d+).*`)

	matches := ifRegex.FindSubmatch(stdout)
	if len(matches) < 2 {
		return 0, errNoInterfaceMatched
	}

	ifIndex, err := strconv.Atoi(string(matches[1]))
	if err != nil {
		return 0, fmt.Errorf("failed to parse interface index: %w", err)
	}

	return ifIndex, nil
}

// getContainersInterfaces returns a list of container IDs with their
// associated interface on the host.
func getContainersInterfaces(ctx context.Context) ([]facts.Veth, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to list interfaces: %w", err)
	}

	interfaceNameByIndex := make(map[int]string)
	for _, iface := range interfaces {
		interfaceNameByIndex[iface.Index] = iface.Name
	}

	containers, err := getContainers(ctx)
	if err != nil {
		return nil, err
	}

	veths := make([]facts.Veth, 0, len(containers))

	for _, container := range containers {
		ifIndex, err := getContainerIfIndex(container)
		if err != nil {
			// The container may be running with the host network.
			logger.Printf("Failed to get interface index for %s: %s", container.ContainerName(), err)

			continue
		}

		interfaceName, ok := interfaceNameByIndex[ifIndex]
		if !ok {
			logger.Printf("Failed to get interface name for %s: %s", container.ContainerName(), err)
		}

		newVeth := facts.Veth{
			Name:        interfaceName,
			ContainerID: container.ID(),
		}
		veths = append(veths, newVeth)
	}

	return veths, nil
}

func run(ctx context.Context) error {
	veths, err := getContainersInterfaces(ctx)
	if err != nil {
		return err
	}

	marshaled, err := json.Marshal(veths)
	if err != nil {
		return fmt.Errorf("failed to marshal interfaces: %w", err)
	}

	err = os.WriteFile(vethFile, marshaled, 0o600)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	gloutonUser, err := user.Lookup("glouton")
	if err != nil {
		return fmt.Errorf("failed to get glouton user: %w", err)
	}

	uid, err := strconv.Atoi(gloutonUser.Uid)
	if err != nil {
		return fmt.Errorf("failed to convert uid to int: %w", err)
	}

	gid, err := strconv.Atoi(gloutonUser.Gid)
	if err != nil {
		return fmt.Errorf("failed to convert gid to int: %w", err)
	}

	if err := os.Chown(vethFile, uid, gid); err != nil {
		return fmt.Errorf("failed to chown file: %w", err)
	}

	return nil
}

func main() {
	ctx := context.Background()
	if err := run(ctx); err != nil {
		logger.Printf("Failed to run: %s", err)
	}
}
