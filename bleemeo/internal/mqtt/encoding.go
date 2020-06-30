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
	"encoding/json"
	"glouton/facts"

	"github.com/klauspost/compress/zstd"
)

type topinfoEncoder struct {
	encoder *zstd.Encoder
	decoder *zstd.Decoder
	buffer  bytes.Buffer
}

// Encode is NOT thread-safe.
func (enc *topinfoEncoder) Encode(topinfo facts.TopInfo) ([]byte, error) {
	if enc.encoder == nil {
		enc.encoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	}

	enc.buffer.Reset()
	enc.encoder.Reset(&enc.buffer)

	err := json.NewEncoder(enc.encoder).Encode(topinfo)
	if err != nil {
		return nil, err
	}

	err = enc.encoder.Flush()
	if err != nil {
		return nil, err
	}

	clone := make([]byte, enc.buffer.Len())
	copy(clone, enc.buffer.Bytes())

	return clone, nil
}

func (enc *topinfoEncoder) Decode(input []byte) (facts.TopInfo, error) {
	if enc.decoder == nil {
		enc.decoder, _ = zstd.NewReader(nil)
	}

	err := enc.decoder.Reset(bytes.NewReader(input))
	if err != nil {
		return facts.TopInfo{}, err
	}

	var topinfo facts.TopInfo

	err = json.NewDecoder(enc.decoder).Decode(&topinfo)

	return topinfo, err
}
