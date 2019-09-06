// Copyright 2015-2019 Bleemeo
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

package mqtt

import (
	"encoding/json"
	"testing"
)

func TestForceDecimalFloat(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{in: 0, want: "0.0"},
		{in: 4.2, want: "4.2"},
		{in: -4.2, want: "-4.2"},
		{in: 4.0, want: "4.0"},
		{in: 87984687654.0, want: "87984687654.0"},
		{in: 0.0000001, want: "1e-7"},
		{in: 1e20, want: "100000000000000000000.0"},
		{in: 1e22, want: "1e+22"},
	}
	for _, c := range cases {
		gotByte, err := forceDecimalFloat(c.in).MarshalJSON()
		if err != nil {
			t.Errorf("Error with case %v: %v", c, err)
		} else {
			got := string(gotByte)
			if got != c.want {
				t.Errorf("forceDecimalFloat(%v).MarshalJSON() == %#v, want %#v", c.in, got, c.want)
			}
			var f float64
			_ = json.Unmarshal(gotByte, &f)
			if f != c.in {
				t.Errorf("Unmarshal(%#v) == %v, want %v", got, f, c.in)
			}
		}
	}
}
