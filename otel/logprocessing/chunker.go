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
	"sync"

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

// split returns two plog.Logs object, each containing a half of the records of the given one.
func (lc logChunker) split(ld plog.Logs) (plog.Logs, plog.Logs) {
	recordsMid := ld.LogRecordCount() / 2
	recordIdx := 0

	firstHalf := plog.NewLogs()
	secondHalf := plog.NewLogs()

	// The goal is to produce two plog.Logs objects with the same number of records in each.
	// To achieve this, we base our iteration index solely on the total number of records we ranged through,
	// regardless of the resource or the scope. This is why we initialize these as late as possible,
	// only when we're about to append a record to the final objects.

	for _, origResourceLogs := range ld.ResourceLogs().All() {
		var (
			firstResourceLogs, secondResourceLogs plog.ResourceLogs
			copyResOnce1, copyResOnce2            sync.Once
		)

		for _, origScopeLogs := range origResourceLogs.ScopeLogs().All() {
			var (
				firstScopeLogs, secondScopeLogs plog.ScopeLogs
				copyScopeOnce1, copyScopeOnce2  sync.Once
			)

			for _, origRecord := range origScopeLogs.LogRecords().All() {
				if recordIdx < recordsMid {
					copyResOnce1.Do(func() {
						firstResourceLogs = firstHalf.ResourceLogs().AppendEmpty()
						origResourceLogs.Resource().CopyTo(firstResourceLogs.Resource())
					})
					copyScopeOnce1.Do(func() {
						firstScopeLogs = firstResourceLogs.ScopeLogs().AppendEmpty()
						origScopeLogs.Scope().CopyTo(firstScopeLogs.Scope())
					})
					origRecord.CopyTo(firstScopeLogs.LogRecords().AppendEmpty())
				} else {
					copyResOnce2.Do(func() {
						secondResourceLogs = secondHalf.ResourceLogs().AppendEmpty()
						origResourceLogs.Resource().CopyTo(secondResourceLogs.Resource())
					})
					copyScopeOnce2.Do(func() {
						secondScopeLogs = secondResourceLogs.ScopeLogs().AppendEmpty()
						origScopeLogs.Scope().CopyTo(secondScopeLogs.Scope())
					})
					origRecord.CopyTo(secondScopeLogs.LogRecords().AppendEmpty())
				}

				recordIdx++
			}
		}
	}

	return firstHalf, secondHalf
}
