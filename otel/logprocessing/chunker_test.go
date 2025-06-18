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

package logprocessing

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

func makeLogs(t *testing.T, res1RecordCount, res2RecordCount int, bodySizeFactor int) plog.Logs {
	t.Helper()

	if res1RecordCount > 999 || res2RecordCount > 999 {
		t.Fatal("Too many log records, you'll need to update the padding in the log body below to increase this limit.")
	}

	if bodySizeFactor < 1 {
		t.Fatalf("Invalid bodySizeFactor %d: it must be at least 1.", bodySizeFactor)
	}

	logs := plog.NewLogs()

	for resIdx, recordCount := range []int{res1RecordCount, res2RecordCount} {
		if recordCount == 0 {
			continue
		}

		resourceLogs := logs.ResourceLogs().AppendEmpty()

		resource := resourceLogs.Resource()
		resource.Attributes().PutStr("service.name", fmt.Sprintf("srv-%d", resIdx+1))

		scopeLogs := resourceLogs.ScopeLogs().AppendEmpty()

		scope := scopeLogs.Scope()
		scope.SetName("logger")
		scope.SetVersion("v1.0.0")

		for i := range recordCount {
			logRec := scopeLogs.LogRecords().AppendEmpty()

			// Padding the log number with leading zeroes enables us to get
			// log records of the same size, regardless of their index.
			bodySample := fmt.Sprintf("Log message n°%03d.", i+1)
			logRec.Body().SetStr(strings.Repeat(bodySample, bodySizeFactor))

			logRec.SetSeverityText("INFO")
			logRec.SetSeverityNumber(plog.SeverityNumberInfo)

			logRec.SetTimestamp(pcommon.NewTimestampFromTime(time.Now()))

			attrs := logRec.Attributes()
			attrs.PutStr("key", "value")
		}
	}

	return logs
}

func TestChunker(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		maxSize             int
		recordCount         int
		bodySizeFactor      int
		expectedChunksCount int
		expectedTotalSize   int
		allowExceed         bool
	}{
		{
			maxSize:             256,
			recordCount:         1,
			bodySizeFactor:      1,
			expectedChunksCount: 1,
			expectedTotalSize:   111,
		},
		{
			maxSize:             256,
			recordCount:         3,
			bodySizeFactor:      1,
			expectedChunksCount: 1,
			expectedTotalSize:   237,
		},
		{
			maxSize:             512,
			recordCount:         15,
			bodySizeFactor:      5,
			expectedChunksCount: 7,
			expectedTotalSize:   2442,
		},
		{
			maxSize:             128,
			recordCount:         50,
			bodySizeFactor:      1,
			expectedChunksCount: 50,
			expectedTotalSize:   5550,
		},
		{
			maxSize:             64,
			recordCount:         1,
			bodySizeFactor:      10,
			expectedChunksCount: 1,
			expectedTotalSize:   287,
			allowExceed:         true, // we can't split a single record
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("maxSize=%d recordCount=%d", tc.maxSize, tc.recordCount), func(t *testing.T) {
			t.Parallel()

			var pushCount, totalPushed, totalRecords int

			unmarshaler := new(plog.ProtoUnmarshaler)
			chunker := logChunker{
				maxChunkSize: tc.maxSize,
				pushLogsFn: func(_ context.Context, b []byte) error {
					if len(b) > tc.maxSize && !tc.allowExceed {
						t.Errorf("Max chunk size exceeded: %d > %d", len(b), tc.maxSize)
					}

					pushCount++
					totalPushed += len(b)

					logs, err := unmarshaler.UnmarshalLogs(b)
					if err != nil {
						t.Fatal("Failed to unmarshal logs:", err)
					}

					totalRecords += logs.LogRecordCount()

					return nil
				},
			}

			err := chunker.push(t.Context(), makeLogs(t, tc.recordCount, 0, tc.bodySizeFactor))
			if err != nil {
				t.Fatal(err)
			}

			if pushCount != tc.expectedChunksCount {
				t.Errorf("Expected %d chunks, got %d", tc.expectedChunksCount, pushCount)
			}

			if totalPushed != tc.expectedTotalSize {
				t.Errorf("Expected %d bytes to be pushed, got %d", tc.expectedTotalSize, totalPushed)
			}

			if totalRecords != tc.recordCount {
				t.Errorf("Expected %d records, got %d", tc.recordCount, totalRecords)
			}
		})
	}
}

func TestChunkerSpecialCases(t *testing.T) { //nolint: maintidx
	t.Parallel()

	type resourceSummary struct {
		Records    int
		Attributes map[string]any
	}

	type chunkSummary struct {
		Size              int
		Records           int
		ResourceSummaries []resourceSummary
	}

	runChunker := func(t *testing.T, maxSize int, ld plog.Logs) (recordCount int, chunkSummaries []chunkSummary) {
		t.Helper()

		unmarshaler := new(plog.ProtoUnmarshaler)
		chunker := logChunker{
			maxChunkSize: maxSize,
			pushLogsFn: func(_ context.Context, b []byte) error {
				logs, err := unmarshaler.UnmarshalLogs(b)
				if err != nil {
					t.Fatal("Failed to unmarshal logs:", err)
				}

				chunkRecCount := logs.LogRecordCount()
				recordCount += chunkRecCount
				summary := chunkSummary{
					Size:              len(b),
					Records:           chunkRecCount,
					ResourceSummaries: make([]resourceSummary, logs.ResourceLogs().Len()),
				}

				for i, res := range logs.ResourceLogs().All() {
					recordsCount := 0

					for _, scope := range res.ScopeLogs().All() {
						recordsCount += scope.LogRecords().Len()
					}

					summary.ResourceSummaries[i] = resourceSummary{
						Records:    recordsCount,
						Attributes: res.Resource().Attributes().AsRaw(),
					}
				}

				chunkSummaries = append(chunkSummaries, summary)

				return nil
			},
		}

		err := chunker.push(t.Context(), ld)
		if err != nil {
			t.Fatal("Failed to push logs:", err)
		}

		return recordCount, chunkSummaries
	}

	t.Run("uniform distribution normal", func(t *testing.T) {
		t.Parallel()

		const totalRecordCount = 100

		recordCount, chunkSummaries := runChunker(t, 1024, makeLogs(t, totalRecordCount/2, totalRecordCount/2, 1))

		if recordCount != totalRecordCount {
			t.Errorf("Expected %d records, got %d", totalRecordCount, recordCount)
		}

		expectedChunks := []chunkSummary{
			{
				Size:    795,
				Records: 12,
				ResourceSummaries: []resourceSummary{
					{
						Records:    12,
						Attributes: map[string]any{"service.name": "srv-1"},
					},
				},
			},
			{
				Size:    857,
				Records: 13,
				ResourceSummaries: []resourceSummary{
					{
						Records:    13,
						Attributes: map[string]any{"service.name": "srv-1"},
					},
				},
			},
			{
				Size:    795,
				Records: 12,
				ResourceSummaries: []resourceSummary{
					{
						Records:    12,
						Attributes: map[string]any{"service.name": "srv-1"},
					},
				},
			},
			{
				Size:    857,
				Records: 13,
				ResourceSummaries: []resourceSummary{
					{
						Records:    13,
						Attributes: map[string]any{"service.name": "srv-1"},
					},
				},
			},
			{
				Size:    795,
				Records: 12,
				ResourceSummaries: []resourceSummary{
					{
						Records:    12,
						Attributes: map[string]any{"service.name": "srv-2"},
					},
				},
			},
			{
				Size:    857,
				Records: 13,
				ResourceSummaries: []resourceSummary{
					{
						Records:    13,
						Attributes: map[string]any{"service.name": "srv-2"},
					},
				},
			},
			{
				Size:    795,
				Records: 12,
				ResourceSummaries: []resourceSummary{
					{
						Records:    12,
						Attributes: map[string]any{"service.name": "srv-2"},
					},
				},
			},
			{
				Size:    857,
				Records: 13,
				ResourceSummaries: []resourceSummary{
					{
						Records:    13,
						Attributes: map[string]any{"service.name": "srv-2"},
					},
				},
			},
		}

		if diff := cmp.Diff(expectedChunks, chunkSummaries); diff != "" {
			t.Errorf("Unexpected chunks (-want +got):\n%s", diff)
		}
	})

	t.Run("uniform distribution big", func(t *testing.T) {
		t.Parallel()

		recordCount, chunkSummaries := runChunker(t, 512, makeLogs(t, 1, 1, 10))

		if recordCount != 2 {
			t.Errorf("Expected 2 records, got %d", recordCount)
		}

		expectedChunks := []chunkSummary{
			{
				Size:    287,
				Records: 1,
				ResourceSummaries: []resourceSummary{
					{
						Records:    1,
						Attributes: map[string]any{"service.name": "srv-1"},
					},
				},
			},
			{
				Size:    287,
				Records: 1,
				ResourceSummaries: []resourceSummary{
					{
						Records:    1,
						Attributes: map[string]any{"service.name": "srv-2"},
					},
				},
			},
		}
		if diff := cmp.Diff(expectedChunks, chunkSummaries); diff != "" {
			t.Errorf("Unexpected chunks (-want +got):\n%s", diff)
		}
	})

	t.Run("uniform distribution too big", func(t *testing.T) {
		t.Parallel()

		recordCount, chunkSummaries := runChunker(t, 512, makeLogs(t, 1, 1, 50))

		if recordCount != 2 {
			t.Errorf("Expected 2 records, got %d", recordCount)
		}

		expectedChunks := []chunkSummary{
			{
				Size:    1047,
				Records: 1,
				ResourceSummaries: []resourceSummary{
					{
						Records:    1,
						Attributes: map[string]any{"service.name": "srv-1"},
					},
				},
			},
			{
				Size:    1047,
				Records: 1,
				ResourceSummaries: []resourceSummary{
					{
						Records:    1,
						Attributes: map[string]any{"service.name": "srv-2"},
					},
				},
			},
		}
		if diff := cmp.Diff(expectedChunks, chunkSummaries); diff != "" {
			t.Errorf("Unexpected chunks (-want +got):\n%s", diff)
		}
	})

	t.Run("diverse distribution 1", func(t *testing.T) {
		t.Parallel()

		recordCount, chunkSummaries := runChunker(t, 1024, makeLogs(t, 20, 80, 1))

		if recordCount != 100 {
			t.Errorf("Expected 100 records, got %d", recordCount)
		}

		expectedChunks := []chunkSummary{
			{
				Size:    795,
				Records: 12,
				ResourceSummaries: []resourceSummary{
					{
						Records:    12,
						Attributes: map[string]any{"service.name": "srv-1"},
					},
				},
			},
			{
				Size:    908,
				Records: 13,
				ResourceSummaries: []resourceSummary{
					{
						Records:    8,
						Attributes: map[string]any{"service.name": "srv-1"},
					},
					{
						Records:    5,
						Attributes: map[string]any{"service.name": "srv-2"},
					},
				},
			},
			{
				Size:    795,
				Records: 12,
				ResourceSummaries: []resourceSummary{
					{
						Records:    12,
						Attributes: map[string]any{"service.name": "srv-2"},
					},
				},
			},
			{
				Size:    857,
				Records: 13,
				ResourceSummaries: []resourceSummary{
					{
						Records:    13,
						Attributes: map[string]any{"service.name": "srv-2"},
					},
				},
			},
			{
				Size:    795,
				Records: 12,
				ResourceSummaries: []resourceSummary{
					{
						Records:    12,
						Attributes: map[string]any{"service.name": "srv-2"},
					},
				},
			},
			{
				Size:    857,
				Records: 13,
				ResourceSummaries: []resourceSummary{
					{
						Records:    13,
						Attributes: map[string]any{"service.name": "srv-2"},
					},
				},
			},
			{
				Size:    795,
				Records: 12,
				ResourceSummaries: []resourceSummary{
					{
						Records:    12,
						Attributes: map[string]any{"service.name": "srv-2"},
					},
				},
			},
			{
				Size:    857,
				Records: 13,
				ResourceSummaries: []resourceSummary{
					{
						Records:    13,
						Attributes: map[string]any{"service.name": "srv-2"},
					},
				},
			},
		}
		if diff := cmp.Diff(expectedChunks, chunkSummaries); diff != "" {
			t.Errorf("Unexpected chunks (-want +got):\n%s", diff)
		}
	})

	t.Run("diverse distribution 2", func(t *testing.T) {
		t.Parallel()

		recordCount, chunkSummaries := runChunker(t, 1024, makeLogs(t, 80, 20, 1))

		if recordCount != 100 {
			t.Errorf("Expected 100 records, got %d", recordCount)
		}

		expectedChunks := []chunkSummary{
			{
				Size:    795,
				Records: 12,
				ResourceSummaries: []resourceSummary{
					{
						Records:    12,
						Attributes: map[string]any{"service.name": "srv-1"},
					},
				},
			},
			{
				Size:    857,
				Records: 13,
				ResourceSummaries: []resourceSummary{
					{
						Records:    13,
						Attributes: map[string]any{"service.name": "srv-1"},
					},
				},
			},
			{
				Size:    795,
				Records: 12,
				ResourceSummaries: []resourceSummary{
					{
						Records:    12,
						Attributes: map[string]any{"service.name": "srv-1"},
					},
				},
			},
			{
				Size:    857,
				Records: 13,
				ResourceSummaries: []resourceSummary{
					{
						Records:    13,
						Attributes: map[string]any{"service.name": "srv-1"},
					},
				},
			},
			{
				Size:    795,
				Records: 12,
				ResourceSummaries: []resourceSummary{
					{
						Records:    12,
						Attributes: map[string]any{"service.name": "srv-1"},
					},
				},
			},
			{
				Size:    857,
				Records: 13,
				ResourceSummaries: []resourceSummary{
					{
						Records:    13,
						Attributes: map[string]any{"service.name": "srv-1"},
					},
				},
			},
			{
				Size:    846,
				Records: 12,
				ResourceSummaries: []resourceSummary{
					{
						Records:    5,
						Attributes: map[string]any{"service.name": "srv-1"},
					},
					{
						Records:    7,
						Attributes: map[string]any{"service.name": "srv-2"},
					},
				},
			},
			{
				Size:    857,
				Records: 13,
				ResourceSummaries: []resourceSummary{
					{
						Records:    13,
						Attributes: map[string]any{"service.name": "srv-2"},
					},
				},
			},
		}
		if diff := cmp.Diff(expectedChunks, chunkSummaries); diff != "" {
			t.Errorf("Unexpected chunks (-want +got):\n%s", diff)
		}
	})
}
