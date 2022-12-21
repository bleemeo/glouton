package synchronizer

import (
	"context"
)

func (s *Synchronizer) syncConfig(
	ctx context.Context,
	fullSync bool,
	onlyEssential bool,
) (updateThresholds bool, err error) {
	// The config is not essential.
	if onlyEssential {
		return false, nil
	}

	// TODO

	return false, nil
}
