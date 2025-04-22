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
	"errors"

	"go.opentelemetry.io/collector/pdata/plog"
)

type logChunker struct {
	maxChunkSize int
	marshaller   plog.ProtoMarshaler
	pushLogsFn   func(context.Context, []byte) error
}

func (lc logChunker) push(ctx context.Context, ld plog.Logs) error {
	if ld.LogRecordCount() > 1 && lc.marshaller.LogsSize(ld) > lc.maxChunkSize {
		half1, half2 := lc.split(ld)

		return errors.Join(lc.push(ctx, half1), lc.push(ctx, half2))
	}

	b, err := lc.marshaller.MarshalLogs(ld)
	if err != nil {
		return err
	}

	return lc.pushLogsFn(ctx, b)
}

func (lc logChunker) split(ld plog.Logs) (plog.Logs, plog.Logs) {
	firstHalf := plog.NewLogs()
	secondHalf := plog.NewLogs()

	for i := range ld.ResourceLogs().Len() {
		origResourceLogs := ld.ResourceLogs().At(i)

		// Attributes
		firstResourceLogs := firstHalf.ResourceLogs().AppendEmpty()
		secondResourceLogs := secondHalf.ResourceLogs().AppendEmpty()

		origResourceLogs.Resource().CopyTo(firstResourceLogs.Resource())
		origResourceLogs.Resource().CopyTo(secondResourceLogs.Resource())

		for j := range origResourceLogs.ScopeLogs().Len() {
			origScopeLogs := origResourceLogs.ScopeLogs().At(j)

			// Scope
			firstScopeLogs := firstResourceLogs.ScopeLogs().AppendEmpty()
			secondScopeLogs := secondResourceLogs.ScopeLogs().AppendEmpty()

			origScopeLogs.Scope().CopyTo(firstScopeLogs.Scope())
			origScopeLogs.Scope().CopyTo(secondScopeLogs.Scope())

			// Records
			origRecords := origScopeLogs.LogRecords()
			total := origRecords.Len()
			mid := total / 2

			for k := range mid {
				origRecords.At(k).CopyTo(firstScopeLogs.LogRecords().AppendEmpty())
			}

			for k := mid; k < total; k++ {
				origRecords.At(k).CopyTo(secondScopeLogs.LogRecords().AppendEmpty())
			}
		}
	}

	return firstHalf, secondHalf
}
