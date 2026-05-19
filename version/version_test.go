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

package version

import (
	"testing"
)

const (
	testVersion20_07_20 = "20.07.20.160738"
	testVersion20_07_22 = "20.07.22.160738"
	testVersionDev      = "0.1"
)

type testCase struct {
	v           string
	base        string
	expectation bool
}

func TestCompare(t *testing.T) {
	vals := []testCase{
		{v: testVersion20_07_20, base: "15.01.15.123456", expectation: true},
		{v: testVersion20_07_20, base: "20.01.15.123456", expectation: true},
		{v: testVersion20_07_20, base: "20.07.15.123456", expectation: true},
		{v: testVersion20_07_20, base: "20.07.20.123456", expectation: true},
		{v: testVersion20_07_22, base: "21.05.15.123456", expectation: false},
		{v: testVersion20_07_22, base: "20.08.15.123456", expectation: false},
		{v: testVersion20_07_22, base: "20.07.23.123456", expectation: false},
		{v: "20.07.22.180000", base: "20.07.22.160000", expectation: true},
		{v: "20.07.22.181500", base: "20.07.22.181000", expectation: true},
		{v: "20.07.22.181550", base: "20.07.22.181520", expectation: true},
		{v: testVersionDev, base: "20.07.20.123456", expectation: true},
		{v: testVersionDev, base: testVersionDev, expectation: true},
		{v: testVersion20_07_22, base: "future version", expectation: false},
		{v: testVersion20_07_22, base: testVersionDev, expectation: false},
	}

	for _, val := range vals {
		if Compare(val.v, val.base) != val.expectation {
			t.Errorf("Compare(%#v, %#v) = %v, want %v", val.v, val.base, Compare(val.v, val.base), val.expectation)
		}
	}
}
