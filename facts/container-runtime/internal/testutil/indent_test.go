// Copyright 2015-2023 Bleemeo
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

package testutil_test

import (
	"glouton/facts/container-runtime/internal/testutil"
	"testing"
)

// TestUnindent check that helper function unindent works as intended.
func TestUnindent(t *testing.T) {
	indented := `
		12:cpuset:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2
		11:memory:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
		10:pids:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope`

	want := `12:cpuset:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2
11:memory:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
10:pids:/docker/72cf779c7429b33b04f296a98fc9be928c82c5537e333589bec734a884cbb2d2/init.scope
`

	got := testutil.Unindent(indented)
	if got != want {
		t.Errorf("got = %#v, want %#v", got, want)
	}
}
