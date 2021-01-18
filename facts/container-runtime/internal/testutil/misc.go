package testutil

import (
	"fmt"
	"glouton/facts"
)

// ContainersToMap convert a list of containers to a map of container with ID as key.
// Return error if there is a ID conflict.
func ContainersToMap(containers []facts.Container) (map[string]facts.Container, error) {
	results := make(map[string]facts.Container, len(containers))

	for _, c := range containers {
		if results[c.ID()] != nil {
			return nil, fmt.Errorf("duplicated ID %v", c.ID())
		}

		results[c.ID()] = c
	}

	return results, nil
}
