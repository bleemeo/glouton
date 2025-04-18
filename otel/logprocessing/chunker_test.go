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
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

func makeLogs(recCount int) plog.Logs {
	logs := plog.NewLogs()

	resourceLogs := logs.ResourceLogs().AppendEmpty()

	resource := resourceLogs.Resource()
	resource.Attributes().PutStr("service.name", "srv")

	scopeLogs := resourceLogs.ScopeLogs().AppendEmpty()

	scope := scopeLogs.Scope()
	scope.SetName("logger")
	scope.SetVersion("v1.0.0")

	for i := range recCount {
		logRec := scopeLogs.LogRecords().AppendEmpty()

		logRec.Body().SetStr(fmt.Sprint("Log message n°", i+1))

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
		expectedChunksCount int
		expectedTotalSize   int
	}{
		{
			maxSize:             256,
			recordCount:         1,
			expectedChunksCount: 1,
			expectedTotalSize:   106,
		},
		{
			maxSize:             256,
			recordCount:         3,
			expectedChunksCount: 1,
			expectedTotalSize:   226,
		},
		{
			maxSize:             256,
			recordCount:         15,
			expectedChunksCount: 7,
			expectedTotalSize:   1234,
		},
		{
			maxSize:             128,
			recordCount:         50,
			expectedChunksCount: 50,
			expectedTotalSize:   5341,
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("maxSize=%d recordCount=%d", tc.maxSize, tc.recordCount), func(t *testing.T) {
			t.Parallel()

			var pushCount, totalPushed int

			chunker := logChunker{
				maxChunkSize: tc.maxSize,
				pushLogsFn: func(_ context.Context, b []byte) error {
					t.Logf("Pushing %d bytes", len(b)) // TODO: remove

					pushCount++
					totalPushed += len(b)

					return nil
				},
			}

			ld := makeLogs(tc.recordCount)

			err := chunker.push(t.Context(), ld)
			if err != nil {
				t.Fatal(err)
			}

			if pushCount != tc.expectedChunksCount {
				t.Errorf("expected %d chunks, got %d", tc.expectedChunksCount, pushCount)
			}

			if totalPushed != tc.expectedTotalSize {
				t.Errorf("Expected %d bytes to be pushed, got %d", tc.expectedTotalSize, totalPushed)
			}
		})
	}
}
