package types

import (
	"context"
	"glouton/facts"
	"glouton/types"
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
	Metrics(ctx context.Context) ([]types.MetricPoint, error)
}
