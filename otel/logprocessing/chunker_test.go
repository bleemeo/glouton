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

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

func makeLogs(t *testing.T, recordCount int, bodySizeFactor int) plog.Logs {
	t.Helper()

	if recordCount > 999 {
		t.Fatal("Too many log records, you'll need to update the padding in the log body below to increase this limit.")
	}

	if bodySizeFactor < 1 {
		t.Fatalf("Invalid bodySizeFactor %d: it must be at least 1.", bodySizeFactor)
	}

	logs := plog.NewLogs()
	resourceLogs := logs.ResourceLogs().AppendEmpty()

	resource := resourceLogs.Resource()
	resource.Attributes().PutStr("service.name", "srv")

	scopeLogs := resourceLogs.ScopeLogs().AppendEmpty()

	scope := scopeLogs.Scope()
	scope.SetName("logger")
	scope.SetVersion("v1.0.0")

	for i := range recordCount {
		logRec := scopeLogs.LogRecords().AppendEmpty()

		// Padding the log number with leading zeroes enables us to get log records of the same size, regardless of their index.
		bodySample := fmt.Sprintf("Log message n°%03d.", i+1)
		logRec.Body().SetStr(strings.Repeat(bodySample, bodySizeFactor))

		logRec.SetSeverityText("INFO")
		logRec.SetSeverityNumber(plog.SeverityNumberInfo)

		logRec.SetTimestamp(pcommon.NewTimestampFromTime(time.Now()))

		attrs := logRec.Attributes()
		attrs.PutStr("key", "value")
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
			expectedTotalSize:   109,
		},
		{
			maxSize:             256,
			recordCount:         3,
			bodySizeFactor:      1,
			expectedChunksCount: 1,
			expectedTotalSize:   235,
		},
		{
			maxSize:             512,
			recordCount:         15,
			bodySizeFactor:      5,
			expectedChunksCount: 7,
			expectedTotalSize:   2428,
		},
		{
			maxSize:             128,
			recordCount:         50,
			bodySizeFactor:      1,
			expectedChunksCount: 50,
			expectedTotalSize:   5450,
		},
		{
			maxSize:             64,
			recordCount:         1,
			bodySizeFactor:      10,
			expectedChunksCount: 1,
			expectedTotalSize:   285,
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

			err := chunker.push(t.Context(), makeLogs(t, tc.recordCount, tc.bodySizeFactor))
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
