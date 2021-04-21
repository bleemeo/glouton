package testutil

import (
	"errors"
	"fmt"
	"glouton/facts"
)

var errDuplicatedID = errors.New("duplicated ID")

// ContainersToMap convert a list of containers to a map of container with ID as key.
// Return error if there is a ID conflict.
func ContainersToMap(containers []facts.Container) (map[string]facts.Container, error) {
	results := make(map[string]facts.Container, len(containers))

	for _, c := range containers {
		if results[c.ID()] != nil {
			return nil, fmt.Errorf("%w %v", errDuplicatedID, c.ID())
		}

		results[c.ID()] = c
	}

	return results, nil
}
