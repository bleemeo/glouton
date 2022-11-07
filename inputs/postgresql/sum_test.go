package postgresql

import (
	"glouton/inputs/internal"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSum(t *testing.T) {
	acc := &internal.StoreAccumulator{
		Measurement: []internal.Measurement{
			{
				Name: "postgresql",
				Tags: map[string]string{"db": "bleemeo"},
				Fields: map[string]interface{}{
					"xact_commit":    1.2,
					"xact_rollback":  3,
					"blk_write_time": 8,
				},
			},
			{
				Name: "postgresql",
				Tags: map[string]string{"db": "postgres"},
				Fields: map[string]interface{}{
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
				Fields: map[string]interface{}{
					"xact_commit":    1.2,
					"xact_rollback":  3,
					"blk_write_time": 8,
				},
			},
			{
				Name: "postgresql",
				Tags: map[string]string{"db": "postgres"},
				Fields: map[string]interface{}{
					"xact_commit":   1.8,
					"xact_rollback": 7,
					"temp_files":    7.4,
				},
			},
			{
				Name: "postgresql",
				Tags: map[string]string{"sum": "true"},
				Fields: map[string]interface{}{
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
