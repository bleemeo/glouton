// Copyright 2015-2023 Bleemeo
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

package inputs

import (
	"glouton/logger"
	"os"
	"sync/atomic"
)

var SecretCount atomic.Int64 //nolint: gochecknoglobals, revive

func CheckLockedMemory() {
	secretsCount := uint64(SecretCount.Load())
	required := 2 * secretsCount * uint64(os.Getpagesize())
	available := getLockedMemoryLimit()

	if required > available {
		required /= 1024
		available /= 1024

		logger.V(0).Printf("Insufficient lockable memory %dkb when %dkb is required (%d secrets). Please increase this limit", available, required, secretsCount)
	}
}
