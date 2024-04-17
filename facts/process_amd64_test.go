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

//go:build amd64

package facts

import (
	"testing"
	"unsafe"
)

func TestWindowsStructureSizes(t *testing.T) {
	if unsafe.Sizeof(UnicodeString{}) != 16 {
		t.Errorf("unsafe.Sizeof(UnicodeString{}) = %d, want 16", unsafe.Sizeof(UnicodeString{}))
	}

	if unsafe.Sizeof(SystemProcessInformationStruct{}) != 0x100 {
		t.Errorf("unsafe.Sizeof(SystemProcessInformationStruct{}) = %d, want 0x100", unsafe.Sizeof(SystemProcessInformationStruct{}))
	}
}
