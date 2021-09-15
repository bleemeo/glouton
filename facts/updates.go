package facts

import (
	"context"
	"time"
)

type updateFacter struct {
	HostRootPath string
	Environ      []string
	InContainer  bool
}

// PendingSystemUpdateFreshness return the indicative value for last update of the preferred method.
// It could be zero if the preferred method don't have any cache or we can't determine the freshness value.
func PendingSystemUpdateFreshness(ctx context.Context, inContainer bool, hostRootPath string) time.Time {
	if hostRootPath == "" && inContainer {
		return time.Time{}
	}

	uf := updateFacter{
		HostRootPath: hostRootPath,
		InContainer:  inContainer,
	}

	return uf.freshness()
}

// PendingSystemUpdate return the number of pending update & pending security update for the system.
// If the value of a field is -1, it means that value is unknown.
func PendingSystemUpdate(ctx context.Context, inContainer bool, hostRootPath string) (pendingUpdates int, pendingSecurityUpdates int) {
	if hostRootPath == "" && inContainer {
		return -1, -1
	}

	uf := updateFacter{
		HostRootPath: hostRootPath,
		InContainer:  inContainer,
	}

	return uf.pendingUpdates(ctx)
}
