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

package syncregistrationstatus

import (
	"cmp"
	"encoding/json"
	"slices"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
)

// Limits of each list sent to Bleemeo, so that the value stays small whatever the host: the API
// bounds neither. Django's DATA_UPLOAD_MAX_MEMORY_SIZE does not apply either: Django REST
// framework reads a JSON body as a stream, and Django only checks the size of request.body.
const (
	maxServices      = 1000
	maxServicesBytes = 1 << 20
	maxMetrics       = 1000
	maxMetricsBytes  = 1 << 20
)

// limitServices bounds the services list, keeping first the services with a problem (a
// service disabled by configuration is not one). It returns the services kept, in their
// order, their encoding, and how many it left out.
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

// limitMetrics bounds the not-registered metrics list, keeping first the rows covering the
// most metrics. It returns the rows kept, in their order, their encoding, and how many it
// left out.
func limitMetrics(states []bleemeoTypes.MetricState) ([]bleemeoTypes.MetricState, [][]byte, int) {
	return limit(states, maxMetrics, maxMetricsBytes, func(a, b bleemeoTypes.MetricState) int {
		return cmp.Compare(b.Count, a.Count)
	})
}

// limit keeps rows in priority order while they fit in maxRows rows and maxBytes of JSON,
// and returns them in their original order with their encoding. The sort is stable, so the
// cut is deterministic.
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
			// Not reachable with these types; leaving the row out still beats an unparsable list.
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

// minEncodedSize is a lower bound of the JSON size of a T: its zero value, less a margin for
// booleans, "true" being shorter than "false".
func minEncodedSize[T any]() int {
	var zero T

	data, err := json.Marshal(zero)
	if err != nil {
		return 0
	}

	return max(len(data)-8, 0)
}
