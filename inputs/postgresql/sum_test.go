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
				Name: "postgresql",
				Tags: map[string]string{"db": "bleemeo"},
				Fields: map[string]any{
					"xact_commit":    1.2,
					"xact_rollback":  3,
					"blk_write_time": 8,
				},
			},
			{
				Name: "postgresql",
				Tags: map[string]string{"db": "postgres"},
				Fields: map[string]any{
					"xact_commit":   1.8,
					"xact_rollback": 7,
					"temp_files":    7.4,
				},
			},
		},
	}

	expected := &internal.StoreAccumulator{
		Measurement: []internal.Measurement{
			{
				Name: "postgresql",
				Tags: map[string]string{"db": "bleemeo"},
				Fields: map[string]any{
					"xact_commit":    1.2,
					"xact_rollback":  3,
					"blk_write_time": 8,
				},
			},
			{
				Name: "postgresql",
				Tags: map[string]string{"db": "postgres"},
				Fields: map[string]any{
					"xact_commit":   1.8,
					"xact_rollback": 7,
					"temp_files":    7.4,
				},
			},
			{
				Name: "postgresql",
				Tags: map[string]string{"sum": "true"},
				Fields: map[string]any{
					"xact_commit":    3.,
					"xact_rollback":  10.,
					"blk_write_time": 8.,
					"temp_files":     7.4,
				},
			},
		},
	}

	sum(acc)

	if diff := cmp.Diff(expected, acc); diff != "" {
		t.Fatalf("Unexpected sum:\n%s", diff)
	}
}
