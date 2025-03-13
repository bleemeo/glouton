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

package client

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/bleemeo/glouton/facts"

	"github.com/google/go-cmp/cmp"
)

// getTopinfo return a topinfo from the system running the test.
func getTopinfo() facts.TopInfo {
	provider := facts.NewProcess(facts.NewPsUtilLister(""), nil)
	topinfo := provider.TopInfo()

	// not all field are encoded by JSON. Do one json encode/decode pass to
	// drop field not sent
	tmp, err := json.Marshal(topinfo)
	if err != nil {
		panic(err)
	}

	topinfo = facts.TopInfo{}

	err = json.Unmarshal(tmp, &topinfo)
	if err != nil {
		panic(err)
	}

	return topinfo
}

func TestTopinfoEncoding(t *testing.T) {
	topinfo := getTopinfo()

	cases := []facts.TopInfo{
		topinfo,
		{},
		{
			Time:   12345679,
			Uptime: 1,
			Loads:  []float64{8},
			Users:  3,
		},
	}

	enc := &encoder{}

	for _, withPool := range []bool{true} {
		for idx, value := range cases {
			name := fmt.Sprintf("case-%d-withpool-%v", idx, withPool)

			t.Run(name, func(t *testing.T) {
				var decoded facts.TopInfo

				encoded, err := enc.Encode(value)
				if err != nil {
					t.Fatal(err)
				}

				err = decode(encoded, &decoded)
				if err != nil {
					t.Fatal(err)
				}

				if diff := cmp.Diff(value, decoded); diff != "" {
					t.Errorf("decoded does not match (-want +got):\n%s", diff)
				}

				if withPool {
					enc.PutBuffer(encoded)
				}

				t.Logf("compressed size = %d", len(encoded))
			})
		}
	}
}

func BenchmarkTopinfoEncoding(b *testing.B) {
	topinfo := getTopinfo()
	enc := &encoder{}

	b.ResetTimer()

	for b.Loop() {
		result, err := enc.Encode(topinfo)
		if err != nil {
			b.Error(err)
		}

		enc.PutBuffer(result)
	}
}

func BenchmarkTopinfoDecoding(b *testing.B) {
	topinfo := getTopinfo()
	enc := &encoder{}

	encoded, err := enc.Encode(topinfo)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()

	for b.Loop() {
		var value facts.TopInfo

		err := decode(encoded, &value)
		if err != nil {
			b.Error(err)
		}
	}
}
