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
