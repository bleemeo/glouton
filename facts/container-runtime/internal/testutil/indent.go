// Copyright 2015-2024 Bleemeo
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

package testutil

import "strings"

// Unindent removes all the indentation of a string.
func Unindent(input string) string {
	lines := strings.Split(input, "\n")
	kept := make([]string, 0, len(lines))

	for _, l := range lines {
		l := strings.TrimLeft(l, " \t")
		if len(l) > 0 {
			kept = append(kept, l)
		}
	}

	return strings.Join(kept, "\n") + "\n"
}
