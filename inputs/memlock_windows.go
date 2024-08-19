// Copyright 2015-2024 Bleemeo
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

//go:build windows

package inputs

import "golang.org/x/sys/windows"

func getLockedMemoryLimit() uint64 {
	handle := windows.CurrentProcess()

	var (
		memMin, memMax uintptr
		flag           uint32
	)

	windows.GetProcessWorkingSetSizeEx(handle, &memMin, &memMax, &flag)

	return uint64(memMax)
}
