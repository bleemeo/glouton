// Copyright 2015-2026 Bleemeo
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

package logsource

import (
	"maps"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
)

// FileSizer reports the current size of every log file a source is watching,
// keyed by file path.
type FileSizer interface {
	SizesByFile() (map[string]int64, error)
}

// GetLastFileSizesFromCache loads the cross-restart file-size cache saved by SaveLastFileSizesToCache under cacheKey. It only tells
// whether a file is new, never the read offset.
func GetLastFileSizesFromCache(state bleemeoTypes.State, cacheKey string) (lastFileSizes map[string]int64) {
	err := state.Get(cacheKey, &lastFileSizes)
	if err != nil {
		logger.V(1).Printf("Can't find log file sizes in cache (key %q): %v", cacheKey, err)
	}

	return lastFileSizes
}

// SaveLastFileSizesToCache persists the current size of every file watched by every sizer under cacheKey.
func SaveLastFileSizesToCache(state bleemeoTypes.State, cacheKey string, sizers []FileSizer) {
	lastFileSizes := make(map[string]int64)

	for _, sizer := range sizers {
		if sizer == nil {
			continue
		}

		sizesByFile, err := sizer.SizesByFile()
		if err != nil {
			logger.V(1).Printf("Can't get log file sizes: %v", err)

			continue
		}

		maps.Copy(lastFileSizes, sizesByFile)
	}

	err := state.Set(cacheKey, lastFileSizes)
	if err != nil {
		logger.V(1).Printf("Failed to save last log file sizes to cache (key %q): %v", cacheKey, err)
	}
}
