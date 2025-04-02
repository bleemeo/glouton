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

package logger

import (
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	// Number of line keep at the head/bottom of the buffer.
	defaultHeadSize = 5000
	defaultTailSize = 5000
	tailsCount      = 6
)

type state int

const (
	stateUninitilized state = iota
	stateWriteHead
	stateWritingTail
)

var errUnreachable = errors.New("unreachable code")

type buffer struct {
	l                sync.Mutex
	head             *bytes.Buffer
	tails            []*bytes.Buffer
	tailIndex        int
	writer           *flate.Writer
	writerSize       int
	state            state
	droppedFirstTail bool

	headMaxSize int
	tailMaxSize int
}

func (b *buffer) Write(input []byte) (int, error) {
	return b.write(time.Now(), input)
}

func (b *buffer) SetCapacity(headSizeBytes int, tailSizeBytes int) {
	if headSizeBytes <= 0 || tailSizeBytes <= 0 {
		V(1).Printf("invalid capacity size for log buffer: head=%d, tail=%d", headSizeBytes, tailSizeBytes)

		return
	}

	b.l.Lock()
	defer b.l.Unlock()

	b.headMaxSize = headSizeBytes
	b.tailMaxSize = tailSizeBytes / tailsCount
}

func (b *buffer) CompressedSize() int {
	b.l.Lock()
	defer b.l.Unlock()

	if b.state == stateUninitilized {
		return 0
	}

	compressedSize := b.head.Len()
	for i := range b.tails {
		compressedSize += b.tails[i].Len()
	}

	return compressedSize
}

func (b *buffer) reset() {
	b.tails = make([]*bytes.Buffer, tailsCount)
	for i := range b.tails {
		// Assume that best compression is 90%
		b.tails[i] = bytes.NewBuffer(make([]byte, 0, b.tailMaxSize/10))
	}

	b.head = nil
	b.writer = nil
	b.tailIndex = -1
	b.writerSize = 0
	b.droppedFirstTail = false
}

func (b *buffer) write(now time.Time, input []byte) (int, error) {
	ts := now.Format("2006/01/02 15:04:05.999999999 ")
	line := make([]byte, 0, len(ts)+len(input))
	line = append(line, []byte(ts)...)
	line = append(line, input...)

	prefixSize := len(line) - len(input)

	b.l.Lock()
	defer b.l.Unlock()

	if b.headMaxSize == 0 {
		b.headMaxSize = defaultHeadSize
	}

	if b.tailMaxSize == 0 {
		b.tailMaxSize = defaultTailSize
	}

	var err error

	switch b.state {
	case stateUninitilized:
		b.reset()
		b.head = bytes.NewBuffer(nil)

		b.writer, err = flate.NewWriter(b.head, -1)
		if err != nil {
			return 0, err
		}

		b.state = stateWriteHead

		fallthrough
	case stateWriteHead:
		if b.writerSize < b.headMaxSize {
			n, err := b.writer.Write(line)
			b.writerSize += n

			return n - prefixSize, err
		}

		if err := b.newTails(); err != nil {
			return 0, err
		}

		b.state = stateWritingTail

		fallthrough
	case stateWritingTail:
		if b.writerSize > b.tailMaxSize {
			if err := b.newTails(); err != nil {
				return 0, err
			}
		}

		n, err := b.writer.Write(line)
		b.writerSize += n

		return n - prefixSize, err
	default:
		return 0, errUnreachable
	}
}

func (b *buffer) newTails() error {
	err := b.writer.Flush()
	if err != nil {
		return err
	}

	idx := (b.tailIndex + 1) % len(b.tails)
	b.tails[idx].Reset()

	b.writer, err = flate.NewWriter(b.tails[idx], -1)
	if err != nil {
		return err
	}

	b.writerSize = 0
	if b.tailIndex == len(b.tails)-1 {
		b.droppedFirstTail = true
	}

	b.tailIndex = idx

	return nil
}

func (b *buffer) Content() []byte {
	b.l.Lock()
	defer b.l.Unlock()

	_ = b.writer.Flush()

	switch b.state {
	case stateWriteHead:
		r := flate.NewReader(bytes.NewReader(b.head.Bytes()))

		data, err := io.ReadAll(r)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			data = append(data, []byte(fmt.Sprintf("\ndecode err in head: %v\n", err))...)
		}

		return data
	case stateWritingTail:
		results := bytes.NewBuffer(nil)

		r := flate.NewReader(bytes.NewReader(b.head.Bytes()))

		_, err := io.Copy(results, r) //nolint:gosec
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			fmt.Fprintf(results, "\ndecode err in closed head: %v\n", err)

			return results.Bytes()
		}

		if b.droppedFirstTail {
			results.WriteString("[...]\n")
		}

		for n := 1; n <= len(b.tails); n++ {
			idx := (b.tailIndex + n) % len(b.tails)

			if b.tails[idx] == nil || b.tails[idx].Len() == 0 {
				continue
			}

			r := flate.NewReader(bytes.NewReader(b.tails[idx].Bytes()))

			_, err := io.Copy(results, r) //nolint: gosec
			if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
				fmt.Fprintf(results, "\ndecode err in tail: %v\n", err)

				return results.Bytes()
			}
		}

		return results.Bytes()
	case stateUninitilized:
		fallthrough
	default:
		return nil
	}
}
