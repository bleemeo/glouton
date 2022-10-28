package types

import (
	"context"
	"glouton/config"
	"glouton/facts"
	"glouton/types"
	"path/filepath"
	"strings"
	"time"
)

// RuntimeInterface is the interface that container runtime provide.
type RuntimeInterface interface {
	CachedContainer(containerID string) (c facts.Container, found bool)
	ContainerLastKill(containerID string) time.Time
	Exec(ctx context.Context, containerID string, cmd []string) ([]byte, error)
	Containers(ctx context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, err error)
	Events() <-chan facts.ContainerEvent
	IsRuntimeRunning(ctx context.Context) bool
	ProcessWithCache() facts.ContainerRuntimeProcessQuerier
	Run(ctx context.Context) error
	RuntimeFact(ctx context.Context, currentFact map[string]string) map[string]string
	LastUpdate() time.Time
	Metrics(ctx context.Context, now time.Time) ([]types.MetricPoint, error)
	MetricsMinute(ctx context.Context, now time.Time) ([]types.MetricPoint, error)
}

// ExpandRuntimeAddresses adds the host root to the socket addresses if PrefixHostRoot is true.
func ExpandRuntimeAddresses(runtime config.ContainerRuntimeAddresses, hostRoot string) []string {
	if !runtime.PrefixHostRoot {
		return runtime.Addresses
	}

	if hostRoot == "" || hostRoot == "/" {
		return runtime.Addresses
	}

	addresses := make([]string, 0, len(runtime.Addresses)*2)

	for _, path := range runtime.Addresses {
		addresses = append(addresses, path)

		if path == "" {
			// This is a special value that means "use default of the runtime".
			// Prefixing with the hostRoot don't make sense.
			continue
		}

		switch {
		case strings.HasPrefix(path, "unix://"):
			path = strings.TrimPrefix(path, "unix://")
			addresses = append(addresses, "unix://"+filepath.Join(hostRoot, path))
		case strings.HasPrefix(path, "/"): // ignore non-absolute path. This will also ignore URL (like http://localhost:3000)
			addresses = append(addresses, filepath.Join(hostRoot, path))
		}
	}

	return addresses
}
