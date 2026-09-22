package influxdb

import "testing"

// TestQueryMetricsComeFromTheEngine pins the source: httpd.queryReq would count query
// requests and time the HTTP handling around them, which is not what 3.x's query log
// measures and would make one metric name mean two things.
func TestQueryMetricsComeFromTheEngine(t *testing.T) {
	server := serveLine(t, lineV1)
	store := gatherOnce(t, server.URL)

	var queries, sum, count float64

	for _, m := range store.Measurement {
		if v, ok := m.Fields[fieldQueries]; ok {
			queries, _ = v.(float64)
		}

		if v, ok := m.Fields[fieldQueryDurationSum]; ok {
			sum, _ = v.(float64)
		}

		if v, ok := m.Fields[fieldQueryCount]; ok {
			count, _ = v.(float64)
		}
	}

	// The fixture's queryExecutor: queriesExecuted 4, queriesFinished 4,
	// queryDurationNs 5955249. Its httpd says queryReq 4 and queryReqDurationNs 7581958,
	// so the duration is what tells the two sources apart.
	if queries != 4 {
		t.Errorf("%s = %v, want 4 (queryExecutor.queriesExecuted)", fieldQueries, queries)
	}

	if want := 5955249.0 / 1e9; sum != want {
		t.Errorf("%s = %v, want %v (queryExecutor.queryDurationNs, not httpd's %v)",
			fieldQueryDurationSum, sum, want, 7581958.0/1e9)
	}

	if count != 4 {
		t.Errorf("%s = %v, want 4 (queryExecutor.queriesFinished)", fieldQueryCount, count)
	}
}
