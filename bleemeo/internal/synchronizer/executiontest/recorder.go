// Copyright 2015-2026 Bleemeo
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

// Package executiontest helps testing an entity without the Synchronizer.
package executiontest

import (
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
)

// Recorder records the synchronizations an entity requests and the entities it fails. A test's
// fake execution embeds it and defines the other calls it needs: any call neither defines panics.
type Recorder struct {
	types.SynchronizationExecution

	// Linked holds each linked synchronization requested, as {target, trigger}.
	Linked [][2]types.EntityName
	// Requested tells, for each entity requested, whether the last request was full.
	Requested map[types.EntityName]bool
	// Failed holds the reason each failed entity was failed with.
	Failed map[types.EntityName]error
}

func (r *Recorder) RequestLinkedSynchronization(target types.EntityName, trigger types.EntityName) error {
	r.Linked = append(r.Linked, [2]types.EntityName{target, trigger})

	return nil
}

func (r *Recorder) RequestSynchronization(entityName types.EntityName, requestFull bool) {
	if r.Requested == nil {
		r.Requested = make(map[types.EntityName]bool)
	}

	r.Requested[entityName] = requestFull
}

func (r *Recorder) FailOtherEntity(entityName types.EntityName, reason error) error {
	if r.Failed == nil {
		r.Failed = make(map[types.EntityName]error)
	}

	r.Failed[entityName] = reason

	return nil
}
