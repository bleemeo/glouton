package facts

import (
	"context"
)

type updateFacter struct {
	HostRootPath string
	Environ      []string
	InContainer  bool
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
