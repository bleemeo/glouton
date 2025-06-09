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
	"bytes"
	"compress/zlib"
	"encoding/json"
	"io"
	"os"
	"sync"
)

type encoder struct {
	l          sync.Mutex
	bufferPool sync.Pool
	zlibWriter *zlib.Writer
}

// Encode is thread-safe.
func (e *encoder) EncodeObject(obj any) ([]byte, error) {
	backingBuffer := e.getBuffer()
	buffer := bytes.NewBuffer(backingBuffer)

	e.l.Lock()
	defer e.l.Unlock()

	if e.zlibWriter == nil {
		e.zlibWriter = zlib.NewWriter(buffer)
	} else {
		e.zlibWriter.Reset(buffer)
	}

	err := json.NewEncoder(e.zlibWriter).Encode(obj)
	if err != nil {
		return buffer.Bytes(), err
	}

	err = e.zlibWriter.Close()
	if err != nil {
		return buffer.Bytes(), err
	}

	return buffer.Bytes(), nil
}

func (e *encoder) EncodeBytes(payload []byte) ([]byte, error) {
	backingBuffer := e.getBuffer()
	buffer := bytes.NewBuffer(backingBuffer)

	e.l.Lock()
	defer e.l.Unlock()

	if e.zlibWriter == nil {
		e.zlibWriter = zlib.NewWriter(buffer)
	} else {
		e.zlibWriter.Reset(buffer)
	}

	n, err := e.zlibWriter.Write(payload)
	if err != nil {
		return buffer.Bytes(), err
	}

	if n != len(payload) {
		return buffer.Bytes(), os.ErrClosed
	}

	err = e.zlibWriter.Close()
	if err != nil {
		return buffer.Bytes(), err
	}

	return buffer.Bytes(), nil
}

func (e *encoder) getBuffer() []byte {
	pbuffer, ok := e.bufferPool.Get().(*[]byte)

	var buffer []byte
	if ok {
		buffer = (*pbuffer)[:0]
	}

	return buffer
}

func (e *encoder) PutBuffer(v []byte) {
	if v == nil {
		return
	}

	// Don't kept too large buffer in the pool
	if len(v) > 256*1024 {
		return
	}

	e.bufferPool.Put(&v)
}

func decode(input []byte, obj any) error {
	decoder, err := zlib.NewReader(bytes.NewReader(input))
	if err != nil {
		return err
	}

	err = json.NewDecoder(decoder).Decode(obj)
	if err != nil {
		return err
	}

	_, err = io.Copy(io.Discard, decoder) //nolint:gosec
	if err != nil {
		return err
	}

	err = decoder.Close()

	return err
}
