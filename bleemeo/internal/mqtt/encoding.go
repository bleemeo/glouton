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
	"bytes"
	"compress/zlib"
	"encoding/json"
	"glouton/facts"
)

type topinfoEncoder struct {
}

// Encode is NOT thread-safe.
func (enc *topinfoEncoder) Encode(topinfo facts.TopInfo) ([]byte, error) {
	var buffer bytes.Buffer

	w := zlib.NewWriter(&buffer)

	err := json.NewEncoder(w).Encode(topinfo)
	if err != nil {
		w.Close()

		return nil, err
	}

	err = w.Close()
	if err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func (enc *topinfoEncoder) Decode(input []byte) (facts.TopInfo, error) {
	r, err := zlib.NewReader(bytes.NewReader(input))
	if err != nil {
		return facts.TopInfo{}, err
	}

	var topinfo facts.TopInfo

	err = json.NewDecoder(r).Decode(&topinfo)
	r.Close()

	return topinfo, err
}
