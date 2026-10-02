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

package syncservicesstate

import (
	"cmp"
	"encoding/json"
	"slices"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
)

// Limits of each list sent to Bleemeo.
//
// The API accepts the lists as they come: it validates neither their length nor their
// size. The only ceiling is the request body limit of Django, 2.5 MB (bleemeo-api leaves
// DATA_UPLOAD_MAX_MEMORY_SIZE to its default), and a body over it is refused whole, on
// every collection. Each list therefore gets 1 MiB, which leaves room for the rest of the
// request, and a number of rows no real agent should reach.
const (
	maxServices      = 1000
	maxServicesBytes = 1 << 20
	maxMetrics       = 1000
	maxMetricsBytes  = 1 << 20
)

// limitServices bounds the services list. It returns the services kept, their JSON
// encoding, and how many services it left out.
//
// Services with a problem to report are kept first: telling a user why a service is not
// monitored is what this list is for. A service whose metrics the user disabled has an
// error too, but it only reports their choice, so it does not get that priority. The
// services kept stay in the order they came in, which is sorted.
func limitServices(states []ServiceState) ([]ServiceState, [][]byte, int) {
	hasProblem := func(s ServiceState) bool { return s.Error != "" && !s.MetricsIgnored }

	return limit(states, maxServices, maxServicesBytes, func(a, b ServiceState) int {
		// A problem first, then the order of the list.
		switch {
		case hasProblem(a) && !hasProblem(b):
			return -1
		case !hasProblem(a) && hasProblem(b):
			return 1
		default:
			return 0
		}
	})
}

// limitMetrics bounds the not-registered metrics list. It returns the rows kept, their JSON
// encoding, and how many rows it left out.
//
// The rows covering the most metrics are kept first. The rows kept stay in the order they
// came in, which is sorted.
func limitMetrics(states []bleemeoTypes.MetricState) ([]bleemeoTypes.MetricState, [][]byte, int) {
	return limit(states, maxMetrics, maxMetricsBytes, func(a, b bleemeoTypes.MetricState) int {
		return cmp.Compare(b.Count, a.Count)
	})
}

// limit keeps rows of list, in priority order, while they fit in maxRows rows and maxBytes
// of JSON. It returns them in their original order with their encoding, which the caller
// reuses rather than encoding them again, and how many rows it left out.
//
// priority must only order rows by importance: ties keep their original order (the sort is
// stable), so the choice is deterministic for a given list.
func limit[T any](list []T, maxRows int, maxBytes int, priority func(a, b T) int) ([]T, [][]byte, int) {
	order := make([]int, len(list))
	for i := range order {
		order[i] = i
	}

	slices.SortStableFunc(order, func(a, b int) int { return priority(list[a], list[b]) })

	minRowSize := minEncodedSize[T]()
	encoded := make([][]byte, len(list))
	kept := 0
	size := len("[]")

	for _, i := range order {
		// Stop once no row can fit any more, rather than encode every remaining one.
		if kept == maxRows || maxBytes-size < minRowSize+1 {
			break
		}

		row, err := json.Marshal(list[i])
		if err != nil {
			// Not reachable with the types used here, which hold only strings, numbers and
			// booleans. Leaving the row out is still better than sending a list the API
			// cannot parse.
			logger.V(1).Printf("Unable to encode a services state row: %v", err)

			continue
		}

		// A comma separates each row from the previous one.
		rowSize := len(row)
		if kept > 0 {
			rowSize++
		}

		// Rows are of different sizes, so a later, smaller one may still fit: keep going
		// rather than stop at the first row that does not.
		if size+rowSize > maxBytes {
			continue
		}

		encoded[i] = row
		kept++
		size += rowSize
	}

	result := make([]T, 0, kept)
	rows := make([][]byte, 0, kept)

	for i, row := range list {
		if encoded[i] != nil {
			result = append(result, row)
			rows = append(rows, encoded[i])
		}
	}

	return result, rows, len(list) - kept
}

// minEncodedSize is a lower bound of the JSON size of any value of T.
//
// The zero value is the shortest encoding but for its booleans: "true" is one character
// shorter than "false". The margin covers that for the few booleans these types have.
func minEncodedSize[T any]() int {
	var zero T

	data, err := json.Marshal(zero)
	if err != nil {
		return 0
	}

	return max(len(data)-8, 0)
}
