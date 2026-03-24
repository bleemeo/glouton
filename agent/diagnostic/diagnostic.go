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

package diagnostic

import (
	"context"
	"fmt"
	"io"
	"runtime"

	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
)

func DiagnosticGlobalInfo(ctx context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("goroutines.txt")
	if err != nil {
		return err
	}

	// We don't know how big the buffer needs to be to collect
	// all the goroutines. Use 2MB buffer which hopefully is enough
	buffer := make([]byte, 1<<21)

	n := runtime.Stack(buffer, true)
	buffer = buffer[:n]

	_, err = file.Write(buffer)
	if err != nil {
		return err
	}

	err = crashreport.AddStderrLogToArchive(archive)
	if err != nil {
		logger.V(1).Printf("Can't add stderr log to archive: %v", err)
	}

	file, err = archive.Create("log.txt")
	if err != nil {
		return err
	}

	tmp := logger.Buffer()

	_, err = file.Write(tmp)
	if err != nil {
		return err
	}

	compressedSize := logger.CompressedSize()

	fmt.Fprintf(file, "-- Log size = %d, compressed = %d (ratio: %.2f)\n", len(tmp), compressedSize, float64(compressedSize)/float64(len(tmp)))

	file, err = archive.Create("memstats.txt")
	if err != nil {
		return err
	}

	if err := writeMemstat(file); err != nil {
		return err
	}

	return nil
}

func writeMemstat(writer io.Writer) error {
	var stat runtime.MemStats

	runtime.ReadMemStats(&stat)

	_, err := fmt.Fprintf(writer, "Heap in-use %s, object in-use %d\n", facts.ByteCountDecimal(stat.HeapAlloc), stat.HeapObjects)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(writer, "Total allocated %s, object %d\n", facts.ByteCountDecimal(stat.TotalAlloc), stat.Mallocs)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(
		writer,
		"Heap details Sys (OS allocated) %s, InUse %s, Idle %s (released %s)\n",
		facts.ByteCountDecimal(stat.HeapSys),
		facts.ByteCountDecimal(stat.HeapInuse),
		facts.ByteCountDecimal(stat.HeapIdle),
		facts.ByteCountDecimal(stat.HeapReleased),
	)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(
		writer,
		"Other memory Sys (OS allocated) / InUse: Stack %s / %s; MSpans %s / %s; MCache %s / %s\n",
		facts.ByteCountDecimal(stat.StackSys),
		facts.ByteCountDecimal(stat.StackInuse),
		facts.ByteCountDecimal(stat.MSpanSys),
		facts.ByteCountDecimal(stat.MSpanInuse),
		facts.ByteCountDecimal(stat.MCacheSys),
		facts.ByteCountDecimal(stat.MCacheInuse),
	)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(
		writer,
		"Other memory BuckHashSys %s, GCSys %s, OtherSys %s\n",
		facts.ByteCountDecimal(stat.BuckHashSys),
		facts.ByteCountDecimal(stat.GCSys),
		facts.ByteCountDecimal(stat.OtherSys),
	)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(
		writer,
		"Total: Sys %s; Sum of all *Sys %s, RSS %s\n",
		facts.ByteCountDecimal(stat.Sys),
		facts.ByteCountDecimal(stat.HeapSys+stat.StackSys+stat.MSpanSys+stat.MCacheSys+stat.BuckHashSys+stat.GCSys+stat.OtherSys),
		facts.ByteCountDecimal(getResidentMemoryOfSelf()),
	)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(
		writer,
		"Locked memory limit: %s; Max parallel input secrets: %d\n",
		facts.ByteCountDecimal(inputs.LockedMemoryLimit()),
		inputs.MaxParallelSecrets(),
	)
	if err != nil {
		return err
	}

	return nil
}
