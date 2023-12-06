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
	"math"
	"os"
)

const memoryPagesPerSecret = 3

func CheckLockedMemory() {
	const arbitraryMinSecretsCount uint64 = 16

	required := memoryPagesPerSecret * arbitraryMinSecretsCount * uint64(os.Getpagesize())
	available := getLockedMemoryLimit()

	if required > available {
		required /= 1024
		available /= 1024

		if MaxParallelSecrets() < 2 { //nolint: revive, staticcheck
			// We won't be able to run vSphere inputs at all ...
		}

		logger.V(0).Printf("The amount of lockable memory (%dKB) may be insufficient, and should be at least %dKB.", available, required)
	}
}

func MaxParallelSecrets() int {
	available := float64(getLockedMemoryLimit())
	pageSize := float64(os.Getpagesize())

	return int(math.Floor(available / (memoryPagesPerSecret * pageSize)))
}

// SecretfulInput represents an input that potentially contains secrets.
type SecretfulInput interface {
	SecretCount() int
}
