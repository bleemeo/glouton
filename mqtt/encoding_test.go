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

package mqtt

import (
	"context"
	"encoding/json"
	"glouton/facts"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// getTopinfo return a topinfo from the system running the test.
func getTopinfo() facts.TopInfo {
	provider := facts.NewProcess(facts.NewPsUtilLister(""), nil)

	topinfo, err := provider.TopInfo(context.Background(), 0)
	if err != nil {
		panic(err)
	}

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
	encoder := &Encoder{}

	var (
		decoded  facts.TopInfo
		decoded2 facts.TopInfo
	)

	encoded, err := encoder.Encode(topinfo)
	if err != nil {
		t.Fatal(err)
	}

	err = encoder.Decode(encoded, &decoded)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(topinfo, decoded); diff != "" {
		t.Errorf("decoded does not match: %v", diff)

		return
	}

	encoded2, err := encoder.Encode(facts.TopInfo{})
	if err != nil {
		t.Fatal(err)
	}

	err = encoder.Decode(encoded, &decoded)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(topinfo, decoded) {
		t.Errorf("encoded output buffer seems to be reused between Encode call")
	}

	err = encoder.Decode(encoded2, &decoded2)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(facts.TopInfo{}, decoded2) {
		t.Errorf("encoded output buffer seems to be reused between Encode call")
	}

	t.Logf("compressed size = %d", len(encoded))
}

func BenchmarkTopinfoEncoding(b *testing.B) {
	topinfo := getTopinfo()
	encoder := &Encoder{}

	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		_, err := encoder.Encode(topinfo)
		if err != nil {
			b.Error(err)
		}
	}
}

func BenchmarkTopinfoDecoding(b *testing.B) {
	topinfo := getTopinfo()
	encoder := &Encoder{}

	encoded, err := encoder.Encode(topinfo)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		var value facts.TopInfo

		err := encoder.Decode(encoded, &value)
		if err != nil {
			b.Error(err)
		}
	}
}
