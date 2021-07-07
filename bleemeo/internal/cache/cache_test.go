package cache

import (
	"glouton/agent/state"
	"glouton/bleemeo/types"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func Test_MergeFirstSeenAt(t *testing.T) {
	state, err := state.Load("not_found")
	now := time.Now().Add(-10 * time.Minute).Truncate(time.Second)

	if err != nil {
		t.Errorf("%v", err)

		return
	}

	cache := Load(state)

	want := []types.Metric{
		{
			ID:          "1",
			FirstSeenAt: now,
		},
		{
			ID:          "2",
			FirstSeenAt: now,
		},
		{
			ID:          "3",
			FirstSeenAt: now,
		},
	}

	cache.SetMetrics(want)

	cache.SetMetrics([]types.Metric{
		{
			ID:          "1",
			FirstSeenAt: now.Add(5 * time.Minute),
		},
		{
			ID:          "2",
			FirstSeenAt: now.Add(4 * time.Minute),
		},
		{
			ID:          "3",
			FirstSeenAt: now.Add(3 * time.Minute),
		},
	})

	got := cache.Metrics()

	res := cmp.Diff(got, want)

	if res != "" {
		t.Errorf("FirstSeenAt Merge did not occure correctly:\n%s", res)
	}

}
