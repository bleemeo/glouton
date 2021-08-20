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
	"io"
	"io/ioutil"
)

type mqttEncoder struct {
	encoder *zlib.Writer
	buffer  bytes.Buffer
}

// Encode is NOT thread-safe.
func (enc *mqttEncoder) Encode(obj interface{}) ([]byte, error) {
	if enc.encoder == nil {
		enc.encoder = zlib.NewWriter(&enc.buffer)
	}

	enc.buffer.Reset()
	enc.encoder.Reset(&enc.buffer)

	err := json.NewEncoder(enc.encoder).Encode(obj)
	if err != nil {
		return nil, err
	}

	err = enc.encoder.Close()
	if err != nil {
		return nil, err
	}

	clone := make([]byte, enc.buffer.Len())
	copy(clone, enc.buffer.Bytes())

	return clone, nil
}

func (enc *mqttEncoder) Decode(input []byte, obj interface{}) error {
	decoder, err := zlib.NewReader(bytes.NewReader(input))
	if err != nil {
		return err
	}

	err = json.NewDecoder(decoder).Decode(obj)
	if err != nil {
		return err
	}

	_, err = io.Copy(ioutil.Discard, decoder) //nolint:gosec
	if err != nil {
		return err
	}

	err = decoder.Close()

	return err
}
