package logger

import (
	"bytes"
	"sync"
	"time"
)

const (
	// Number of line keep at the head/bottom of the buffer.
	defaultHeadCount = 30
	defaultTailCount = 30
)

type buffer struct {
	l         sync.Mutex
	head      [][]byte
	tail      [][]byte
	tailIndex int

	headMaxSize int
	tailMaxSize int
}

func (b *buffer) Write(input []byte) (int, error) {
	return b.write(time.Now(), input)
}

func (b *buffer) SetCapacity(headSize int, tailSize int) {
	if headSize <= 0 || tailSize <= 0 {
		V(1).Printf("invalid capacity size for log buffer: head=%d, tail=%d", headSize, tailSize)

		return
	}

	b.l.Lock()
	defer b.l.Unlock()

	b.tail = nil

	if len(b.head) > headSize {
		b.head = b.head[:headSize]
	}

	b.headMaxSize = headSize
	b.tailMaxSize = tailSize
	b.tailIndex = 0
}

func (b *buffer) write(now time.Time, input []byte) (int, error) {
	ts := now.Format("2006/01/02 15:04:05 ")
	line := make([]byte, 0, len(ts)+len(input))
	line = append(line, []byte(ts)...)
	line = append(line, input...)

	b.l.Lock()
	defer b.l.Unlock()

	if b.headMaxSize == 0 {
		b.headMaxSize = defaultHeadCount
	}

	if b.tailMaxSize == 0 {
		b.tailMaxSize = defaultTailCount
	}

	switch {
	case len(b.head) < b.headMaxSize:
		b.head = append(b.head, line)
	case len(b.tail) < b.tailMaxSize:
		b.tail = append(b.tail, line)
	default:
		b.tail[b.tailIndex] = line
		b.tailIndex = (b.tailIndex + 1) % b.tailMaxSize
	}

	return len(input), nil
}

func (b *buffer) Content() []byte {
	b.l.Lock()
	defer b.l.Unlock()

	if len(b.tail) == 0 {
		return bytes.Join(b.head, []byte{})
	}

	tailSorted := make([][]byte, 0, len(b.tail))

	tailSorted = append(tailSorted, b.tail[b.tailIndex:len(b.tail)]...)
	tailSorted = append(tailSorted, b.tail[0:b.tailIndex]...)

	result := bytes.Join(b.head, []byte{})

	if len(b.tail) >= b.tailMaxSize {
		result = append(result, []byte("[...]\n")...)
	}

	result = append(result, bytes.Join(tailSorted, []byte{})...)

	return result
}
