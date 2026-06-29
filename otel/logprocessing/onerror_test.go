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

package logprocessing

import (
	"testing"

	"github.com/bleemeo/glouton/config"
)

func TestQuietParserErrors(t *testing.T) {
	t.Parallel()

	in := []config.OTELOperator{
		{"type": "regex_parser", "regex": "^(?<x>.+)$"}, // no on_error → send_quiet
		{"type": "json_parser"},                         // no on_error → send_quiet
		{"type": "time_parser"},                         // no on_error → send_quiet
		{"type": "severity_parser"},                     // no on_error → send_quiet
		{"type": "json_parser", "on_error": "drop"},     // explicit → left untouched
		{"type": "add", "field": "body", "value": "x"},  // not a parser → untouched
	}

	out := quietParserErrors(in)

	// Every parser type without an explicit on_error is defaulted to send_quiet.
	for _, i := range []int{0, 1, 2, 3} {
		if got := out[i]["on_error"]; got != "send_quiet" {
			t.Errorf("operator %d (%v) on_error = %v, want send_quiet", i, out[i]["type"], got)
		}
	}

	if got := out[4]["on_error"]; got != "drop" {
		t.Errorf("explicit on_error = %v, want it preserved (drop)", got)
	}

	if _, set := out[5]["on_error"]; set {
		t.Errorf("non-parser operator should not get an on_error")
	}

	// The shared input definitions must not be mutated (they're reused across receivers).
	if _, set := in[0]["on_error"]; set {
		t.Errorf("input operator was mutated; it must be cloned before adding on_error")
	}
}
