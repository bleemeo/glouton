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

package logsource

import (
	"testing"

	"github.com/bleemeo/glouton/config"
)

// TestBuildOperatorsFromKnownFormat smoke-tests resolving a
// known_log_formats-style entry by name into stanza operator.Config values;
// fuller behavior is covered by otel/logprocessing's tests of the same
// exported functions.
func TestBuildOperatorsFromKnownFormat(t *testing.T) {
	t.Parallel()

	knownFormats := map[string][]config.OTELOperator{
		"apache_access": {
			{
				"type":  "regex_parser",
				"regex": `^(?P<http_response_status_code>\d+)$`,
			},
		},
	}

	expanded, err := ExpandLogFormats(knownFormats)
	if err != nil {
		t.Fatal("ExpandLogFormats returned an error:", err)
	}

	ops, err := BuildOperators(expanded["apache_access"])
	if err != nil {
		t.Fatal("BuildOperators returned an error:", err)
	}

	if len(ops) != 1 {
		t.Fatalf("Expected exactly 1 built operator, got %d", len(ops))
	}
}
