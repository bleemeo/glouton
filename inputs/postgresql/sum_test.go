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

package postgresql

import (
	"testing"

	"github.com/bleemeo/glouton/inputs/internal"

	"github.com/google/go-cmp/cmp"
)

func TestSum(t *testing.T) {
	acc := &internal.StoreAccumulator{
		Measurement: []internal.Measurement{
			{
				Name: inputName,
				Tags: map[string]string{"db": "bleemeo"},
				Fields: map[string]any{
					fieldXactCommit:   1.2,
					fieldXactRollback: 3,
					fieldBlkWriteTime: 8,
				},
			},
			{
				Name: inputName,
				Tags: map[string]string{"db": "postgres"},
				Fields: map[string]any{
					fieldXactCommit:   1.8,
					fieldXactRollback: 7,
					fieldTempFiles:    7.4,
				},
			},
		},
	}

	expected := &internal.StoreAccumulator{
		Measurement: []internal.Measurement{
			{
				Name: inputName,
				Tags: map[string]string{"db": "bleemeo"},
				Fields: map[string]any{
					fieldXactCommit:   1.2,
					fieldXactRollback: 3,
					fieldBlkWriteTime: 8,
				},
			},
			{
				Name: inputName,
				Tags: map[string]string{"db": "postgres"},
				Fields: map[string]any{
					fieldXactCommit:   1.8,
					fieldXactRollback: 7,
					fieldTempFiles:    7.4,
				},
			},
			{
				Name: inputName,
				Tags: map[string]string{"sum": "true"},
				Fields: map[string]any{
					fieldXactCommit:   3.,
					fieldXactRollback: 10.,
					fieldBlkWriteTime: 8.,
					fieldTempFiles:    7.4,
				},
			},
		},
	}

	sum(acc)

	if diff := cmp.Diff(expected, acc); diff != "" {
		t.Fatalf("Unexpected sum:\n%s", diff)
	}
}
