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

	"github.com/bleemeo/glouton/facts"
)

// TestParseBoolLabelAcceptsYesNo guards parseBoolLabel's reuse of config.ParseBool: it must accept
// "yes"/"no" (case-insensitively), not just strconv.ParseBool's stricter true/false/1/0/t/f set.
func TestParseBoolLabelAcceptsYesNo(t *testing.T) {
	t.Parallel()

	ctr := facts.FakeContainer{FakeContainerName: "test"}

	testCases := []struct {
		value string
		want  *bool
	}{
		{value: "yes", want: new(true)},
		{value: "YES", want: new(true)},
		{value: "no", want: new(false)},
		{value: "true", want: new(true)},
		{value: "false", want: new(false)},
		{value: "not-a-bool", want: nil},
	}

	for _, tc := range testCases {
		got := parseBoolLabel(ctr, map[string]string{"k": tc.value}, "k")
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("parseBoolLabel(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}

	if got := parseBoolLabel(ctr, map[string]string{}, "k"); got != nil {
		t.Errorf("Expected nil for a missing label, got %v", *got)
	}
}
