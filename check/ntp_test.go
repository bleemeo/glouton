// Copyright 2015-2025 Bleemeo
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

package check

import "testing"

func TestLeapVersionMode(t *testing.T) {
	v := encodeLeapVersionMode(0, 3, 3)
	if v != 0x1b {
		t.Errorf("encodeLeapVersionMode() == %v, want %v", v, 0x1b)
	}

	li, vn, mode := decodeLeapVersionMode(0x1b)
	if li != 0 {
		t.Errorf("leap indicator == %v, want 0", li)
	}

	if vn != 3 {
		t.Errorf("version number == %v, want 0", vn)
	}

	if mode != 3 {
		t.Errorf("mode == %v, want 0", mode)
	}
}
