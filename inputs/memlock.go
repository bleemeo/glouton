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

package inputs

import (
	"math"
	"os"

	"github.com/bleemeo/glouton/logger"
)

const (
	baseMemoryPages      = 3 // The number of pages that are always used by memguard
	memoryPagesPerSecret = 2 // This value is from looking at telegraf/memguard code and experimentation.
)

func CheckLockedMemory() {
	const arbitraryMinSecretsCount uint64 = 16

	required := (baseMemoryPages + memoryPagesPerSecret*arbitraryMinSecretsCount) * uint64(os.Getpagesize())
	available := getLockedMemoryLimit()

	if required > available {
		required /= 1024
		available /= 1024

		msg := `The amount of lockable memory (%dkB) may be insufficient, and should be at least %dkB.
Learn more about this limitation at https://go.bleemeo.com/l/doc-memory-locking.`
		logger.V(0).Printf(msg, available, required)
	}
}

func MaxParallelSecrets() int {
	available := float64(getLockedMemoryLimit())
	pageSize := float64(os.Getpagesize())

	return int(math.Floor(available / ((baseMemoryPages + memoryPagesPerSecret) * pageSize)))
}

func LockedMemoryLimit() uint64 {
	return getLockedMemoryLimit()
}

// SecretfulInput represents an input that potentially contains secrets.
type SecretfulInput interface {
	SecretCount() int
}
