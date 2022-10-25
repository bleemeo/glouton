// Copyright 2015-2022 Bleemeo
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

package agent

import (
	"testing"
)

func Test_isValidConfigFile(t *testing.T) {
	tests := []struct {
		fileName string
		want     bool
	}{
		{
			fileName: "glouton.conf",
			want:     true,
		},
		{
			fileName: "90-local.conf",
			want:     true,
		},
		{
			fileName: "..2022_02_16_13_08_02.846905773",
			want:     true,
		},
		{
			fileName: "glouton.conf.swp",
			want:     false,
		},
		{
			fileName: "glouton.conf.swo",
			want:     false,
		},
		{
			fileName: "glouton.conf.swn",
			want:     false,
		},
		{
			fileName: "glouton.conf~",
			want:     false,
		},
		{
			fileName: "glouton.save",
			want:     false,
		},
	}

	for _, test := range tests {
		t.Run(test.fileName, func(t *testing.T) {
			if got := isValidConfigFile(test.fileName); got != test.want {
				t.Errorf("isValidConfigFile(%s) is %v, expected %v", test.fileName, got, test.want)
			}
		})
	}
}
