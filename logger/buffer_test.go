//nolint:scopelint,cyclop
package logger

import (
	"bytes"
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func Test_buffer(t *testing.T) {
	re := regexp.MustCompile(`this is line #(\d+)`)
	b := &buffer{}

	const (
		maxLine  = 100000
		headSize = 10000
		tailSize = 20000
	)

	b.SetCapacity(headSize, tailSize)

	var tailAlreadyPresent bool

	for n := 0; n < maxLine; n++ {
		line := fmt.Sprintf("this is line #%d. This random is to reduce compression %d%d%d\n", n, rand.Int(), rand.Int(), rand.Int()) //nolint: gosec

		n, err := b.write(time.Now(), []byte(line))
		if err != nil {
			t.Fatal(err)
		}

		if n != len([]byte(line)) {
			t.Fatalf("Write() = %d, want %d", n, len([]byte(line)))
		}

		if b.state == stateWritingTail && !tailAlreadyPresent {
			tailAlreadyPresent = true

			content := b.Content()
			if bytes.Contains(content, []byte("[...]")) {
				t.Errorf("content already had elipis marked but shouldn't")
			}
		}
	}

	for readNumber := 1; readNumber < 3; readNumber++ {
		t.Run(fmt.Sprintf("readnumber=%d", readNumber), func(t *testing.T) {
			var (
				hadError         bool
				hadElipsisMarker bool
			)

			seenNumber := make(map[int]bool)

			content := b.Content()
			for i, line := range strings.Split(string(content), "\n") {
				if line == "" {
					continue
				}

				if line == "[...]" {
					hadElipsisMarker = true

					continue
				}

				match := re.FindStringSubmatch(line)
				if match == nil {
					t.Errorf("line %#v (#%d) don't match RE", line, i)

					hadError = true

					continue
				}

				n, err := strconv.ParseInt(match[1], 10, 0)
				if err != nil {
					t.Error(err)

					hadError = true

					continue
				}

				seenNumber[int(n)] = true
			}

			for _, n := range []int{0, 1, 2} {
				if !seenNumber[n] {
					t.Errorf("line #%d should be present, but isn't", n)
					hadError = true
				}
			}

			var (
				headEndAt   int
				tailStartAt int
			)

			for n := 0; n < maxLine; n++ {
				if !seenNumber[n] && headEndAt == 0 {
					headEndAt = n
				}

				if headEndAt != 0 && seenNumber[n] {
					tailStartAt = n

					break
				}
			}

			if headEndAt == 0 {
				t.Errorf("no absent value. Buffer is too large / test don't write enough")
				hadError = true
			}

			if tailStartAt == 0 {
				t.Errorf("no starting tail. This isn't expected. firstAbsent=%d", headEndAt)
				hadError = true
			}

			for n := headEndAt; n < tailStartAt; n++ {
				if seenNumber[n] {
					t.Errorf("line %d is present, but shouldn't (head end=%d, tail start=%d)", n, headEndAt, tailStartAt)
					hadError = true

					break
				}
			}

			for n := tailStartAt; n < maxLine; n++ {
				if !seenNumber[n] {
					t.Errorf("line %d isn't present, but should (head end=%d, tail start=%d)", n, headEndAt, tailStartAt)
					hadError = true

					break
				}
			}

			if !hadElipsisMarker {
				t.Error("elipis marked \"[...]\" not found")

				hadError = true
			}

			// +6 is for the elipis marked and its newline
			if len(content) > headSize+tailSize+6 {
				t.Errorf("len(content) = %d, want < %d", len(content), headSize+tailSize+6)

				hadError = true
			}

			if len(content) < headSize+tailSize/2 {
				t.Errorf("len(content) = %d, want > %d", len(content), headSize+tailSize/2)

				hadError = true
			}

			if b.head.Len() > b.headMaxSize {
				t.Errorf("head size = %d, want < %d", b.head.Len(), b.headMaxSize)

				hadError = true
			}

			for i := range b.tails {
				if b.tails[i].Len() > b.tailMaxSize {
					t.Errorf("tails[%d] size = %d, want < %d", i, b.tails[i].Len(), b.tailMaxSize)

					hadError = true
				}
			}

			if hadError {
				if len(content) < 150 {
					t.Logf("content is %#v", string(content))
				} else {
					t.Logf("content[:150] is %#v", string(content[:150]))
					t.Logf("content[-150:] is %#v", string(content[len(content)-150:]))
				}
			}
		})
	}
}

func Benchmark_buffer(b *testing.B) {
	buff := &buffer{}

	const (
		maxLine  = 100000
		headSize = 5000
		tailSize = 5000
	)

	buff.SetCapacity(headSize, tailSize)

	for n := 0; n < b.N; n++ {
		line := fmt.Sprintf("this is line #%d. This random is to reduce compression %d%d%d\n", n, rand.Int(), rand.Int(), rand.Int()) //nolint: gosec

		n, err := buff.write(time.Now(), []byte(line))
		if err != nil {
			b.Fatal(err)
		}

		if n != len([]byte(line)) {
			b.Fatalf("Write() = %d, want %d", n, len([]byte(line)))
		}
	}
}
