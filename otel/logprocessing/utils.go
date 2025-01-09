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
	"encoding/json"
	"fmt"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
)

const logFileSizesCacheKey = "LogFileSizes"

func getLastFileSizesFromCache(state bleemeoTypes.State) (lastFileSizes map[string]int64) {
	err := state.Get(logFileSizesCacheKey, &lastFileSizes)
	if err != nil {
		logger.V(1).Printf("Can't find log file sizes in cache: %v", err)
	}

	logger.Printf("Last file sizes: %v", lastFileSizes) // TODO: remove

	return lastFileSizes
}

func saveLastFileSizesToCache(state bleemeoTypes.State, receivers []*logReceiver) {
	lastFileSizes := make(map[string]int64)

	for _, recv := range receivers {
		sizesByFile, err := recv.sizesByFile()
		if err != nil {
			logger.V(1).Printf("Can't get log file sizes: %v", err)

			continue
		}

		for logFile, size := range sizesByFile {
			lastFileSizes[logFile] = size
		}
	}

	err := state.Set(logFileSizesCacheKey, lastFileSizes)
	if err != nil {
		logger.V(1).Printf("Failed to save last log file sizes to cache: %v", err)
	}
}

// formatTypes acts like a mix of %T and %v, on a slice.
func formatTypes[E any](a []E) string {
	result := "["

	for i, e := range a {
		result += fmt.Sprintf("%T", e)

		if i < len(a)-1 {
			result += " "
		}
	}

	return result + "]"
}

type receiverDiagnosticInformation struct {
	LogProcessedCount      int64
	LogThroughputPerMinute int
	FileLogReceiverPaths   []string
	ExecLogReceiverPaths   []string
	IgnoredFilePaths       []string
}

type diagnosticInformation struct {
	LogProcessedCount      int64
	LogThroughputPerMinute int
	ProcessingStatus       string
	Receivers              []receiverDiagnosticInformation
}

func (diagInfo diagnosticInformation) writeToArchive(writer types.ArchiveWriter) error {
	file, err := writer.Create("log-processing.json")
	if err != nil {
		return err
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(diagInfo)
}
