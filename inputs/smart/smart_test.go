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

package smart

import "testing"

func TestOverrideDeviceName(t *testing.T) {
	testCases := []struct {
		deviceName   string
		deviceType   string
		expectedName string
	}{
		{
			deviceName:   "nvme0",
			deviceType:   "nvme",
			expectedName: "nvme0",
		},
		{
			deviceName:   "0",
			deviceType:   "megaraid,0",
			expectedName: "RAID Disk 0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.deviceName+"/"+tc.deviceType, func(t *testing.T) {
			result := overrideDeviceName(tc.deviceName, tc.deviceType)
			if result != tc.expectedName {
				t.Errorf("Unexpected result of overrideDeviceName(%q, %q): want %q, got %q.", tc.deviceName, tc.deviceType, tc.expectedName, result)
			}
		})
	}
}
