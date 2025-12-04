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
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func Test_bufferSize(t *testing.T) {
	re := regexp.MustCompile(`this is line #(\d+)`)
	b := &buffer{}

	const (
		maxLine  = 100000
		headSize = 10000
		tailSize = 20000
	)

	b.SetCapacity(headSize, tailSize)

	var tailAlreadyPresent bool

	for lineNumber := range maxLine {
		line := fmt.Sprintf("this is line #%d. This random is to reduce compression %d%d%d\n", lineNumber, rand.Int(), rand.Int(), rand.Int()) //nolint: gosec

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

			for n := range maxLine {
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

func Test_bufferOrder(t *testing.T) { //nolint: maintidx
	t.Parallel()

	re := regexp.MustCompile(`this is line #(\d+)`)

	const (
		headSize             = 10000
		tailSize             = 20000
		elispseExpectedAfter = 250
	)

	type change struct {
		changeAt    int
		newHeadSize int
		newTailSize int
	}

	cases := []struct {
		name    string
		changes []change // must be ordred by changeAt
	}{
		{
			name: "no change",
			changes: []change{
				{
					changeAt:    0,
					newHeadSize: headSize,
					newTailSize: tailSize,
				},
			},
		},
		{
			name: "increase early",
			changes: []change{
				{
					changeAt:    0,
					newHeadSize: headSize,
					newTailSize: tailSize,
				},
				{
					changeAt:    5,
					newHeadSize: headSize + 5,
					newTailSize: tailSize + 10,
				},
			},
		},
		{
			name: "no change early",
			changes: []change{
				{
					changeAt:    0,
					newHeadSize: headSize,
					newTailSize: tailSize,
				},
				{
					changeAt:    5,
					newHeadSize: headSize,
					newTailSize: tailSize,
				},
			},
		},
		{
			name: "increase late",
			changes: []change{
				{
					changeAt:    0,
					newHeadSize: headSize,
					newTailSize: tailSize,
				},
				{
					changeAt:    2500,
					newHeadSize: headSize + 5,
					newTailSize: tailSize + 10,
				},
			},
		},
		{
			name: "multiple changes",
			changes: []change{
				{
					changeAt:    0,
					newHeadSize: headSize,
					newTailSize: tailSize,
				},
				{
					changeAt:    100,
					newHeadSize: headSize + 5,
					newTailSize: tailSize + 10,
				},
				{
					changeAt:    1000,
					newHeadSize: headSize - 5,
					newTailSize: tailSize - 10,
				},
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b := &buffer{}

			// We should have biggest writeSize big enough, such as we cycled tail enough time.
			// 10*elispseExpectedAfter should be enough.
			writeSize := []int{10, 100, 150, 200, 250, 1000, 10000, 100000}

			var (
				lineNumber             int
				changeIndex            int
				elispseAfterCountLines int
			)

			rnd := rand.New(rand.NewSource(42)) //nolint: gosec

			for _, maxLine := range writeSize {
				t.Run(fmt.Sprintf("maxLine=%d", maxLine), func(t *testing.T) {
					if testing.Short() && maxLine >= 10000 {
						t.SkipNow()
					}

					for ; lineNumber < maxLine; lineNumber++ {
						if len(tt.changes) > changeIndex && lineNumber == tt.changes[changeIndex].changeAt {
							b.SetCapacity(tt.changes[changeIndex].newHeadSize, tt.changes[changeIndex].newTailSize)

							changeIndex++
						}

						line := fmt.Sprintf("this is line #%d. This random is to reduce compression %d%d%d\n", lineNumber, rnd.Int(), rnd.Int(), rnd.Int())

						n, err := b.write(time.Now(), []byte(line))
						if err != nil {
							t.Fatal(err)
						}

						if n != len([]byte(line)) {
							t.Fatalf("Write() = %d, want %d", n, len([]byte(line)))
						}
					}

					seenNumber := make(map[int]int)
					content := b.Content()

					var (
						hadError         bool
						hadElipsisMarker bool
						elipsisMarkerAt  int
						lastNumber       int64
					)

					for i, line := range strings.Split(string(content), "\n") {
						if line == "" {
							continue
						}

						if line == "[...]" {
							hadElipsisMarker = true
							elipsisMarkerAt = i

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

						if n < lastNumber {
							t.Errorf("line #%d has number %d but previous number was bigger (%d)", i, n, lastNumber)
						}

						if n != lastNumber+1 && !hadElipsisMarker && i != 0 {
							t.Errorf("line #%d has number %d but expected %d", i, n, lastNumber+1)
						}

						if n != lastNumber+1 && hadElipsisMarker && elipsisMarkerAt != i-1 {
							t.Errorf("line #%d has number %d but expected %d", i, n, lastNumber+1)
						}

						lastNumber = n

						if _, ok := seenNumber[int(n)]; ok {
							t.Errorf("line #%d has number %d which was already seen at line %d", i, n, seenNumber[int(n)])
						}

						seenNumber[int(n)] = i
					}

					if _, ok := seenNumber[0]; !ok {
						t.Error("Line #0 isn't seen")
					}

					if elispseAfterCountLines == 0 && hadElipsisMarker {
						elispseAfterCountLines = elipsisMarkerAt
					} else if hadElipsisMarker && elispseAfterCountLines != elipsisMarkerAt {
						t.Errorf("elipsisMarkerAt=%d want %d", elipsisMarkerAt, elispseAfterCountLines)
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

			if elispseAfterCountLines > elispseExpectedAfter {
				t.Errorf("elispseAfterCountLines = %d, want less than %d", elispseAfterCountLines, elispseExpectedAfter)
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

	for n := 0; b.Loop(); n++ {
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
