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

package smart

import (
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/influxdata/telegraf/plugins/inputs/smart"
)

func TestStorageDevicesPattern(t *testing.T) {
	if _, err := filepath.Match(storageDevicesPattern, "foo"); err != nil {
		t.Fatalf("Storage devices pattern is invalid: %v", err)
	}
}

func TestDeviceTypeFor(t *testing.T) {
	input2Expected := map[string]string{
		"nvme":          "",
		"-d nvme":       " -d nvme",
		"-d scsi":       "",
		"-d megaraid,7": " -d megaraid,7",
		"# unexpected":  "",
	}

	for input, expected := range input2Expected {
		result := deviceTypeFor(input)
		if result != expected {
			t.Errorf("Invalid result from deviceTypeFor(%q): want %q, got %q", input, expected, result)
		}
	}
}

func TestShouldHandleDevice(t *testing.T) {
	wrapper := inputWrapper{
		Smart: &smart.Smart{
			Excludes: []string{"/dev/nvme1"},
		},
	}

	expectedOutput := map[string]bool{
		"/dev/nvme0": true,
		"/dev/nvme1": false,
		"/dev/nvme2": true,
	}
	output := make(map[string]bool)

	for device := range expectedOutput {
		result := wrapper.shouldHandleDevice(device)
		output[device] = result
	}

	if diff := cmp.Diff(expectedOutput, output); diff != "" {
		t.Fatalf("Unexpected device filtering (-want +got):\n%s", diff)
	}
}

func TestParseScanOutput(t *testing.T) {
	testCases := []struct {
		name      string
		input     string
		sgDevices []string

		expectedDevices []string
	}{
		{
			name:            "firewall",
			input:           "/dev/sda -d scsi # /dev/sda, SCSI device",
			sgDevices:       []string{"/dev/sg0", "/dev/sg1", "/dev/sg2", "/dev/sg3", "/dev/sg4"},
			expectedDevices: []string{"/dev/sda", "/dev/sg0", "/dev/sg2", "/dev/sg3", "/dev/sg4"},
		},
		{
			name:            "home1",
			input:           "/dev/sda -d scsi # /dev/sda, SCSI device\n/dev/sdb -d scsi # /dev/sdb, SCSI device\n/dev/nvme0 -d nvme # /dev/nvme0, NVMe device",
			expectedDevices: []string{"/dev/sda", "/dev/sdb", "/dev/nvme0 -d nvme"},
		},
		{
			name:            "home2",
			input:           "",
			sgDevices:       []string{"/dev/sg0"},
			expectedDevices: []string{"/dev/sg0"},
		},
		{
			name:            "macOS",
			input:           "IOService:/AppleARMPE/arm-io/AppleT600xIO/ans@8F400000/AppleASCWrapV4/iop-ans-nub/RTBuddy(ANS2)/RTBuddyService/AppleANS3NVMeController/NS_01@1 -d nvme # IOService:/AppleARMPE/arm-io/AppleT600xIO/ans@8F400000/AppleASCWrapV4/iop-ans-nub/RTBuddy(ANS2)/RTBuddyService/AppleANS3NVMeController/NS_01@1, NVMe device",
			expectedDevices: []string{"IOService:/AppleARMPE/arm-io/AppleT600xIO/ans@8F400000/AppleASCWrapV4/iop-ans-nub/RTBuddy(ANS2)/RTBuddyService/AppleANS3NVMeController/NS_01@1 -d nvme"},
		},
		{
			name:            "proxmox1",
			input:           "/dev/sda -d scsi # /dev/sda, SCSI device\n/dev/bus/0 -d megaraid,0 # /dev/bus/0 [megaraid_disk_00], SCSI device\n/dev/bus/0 -d megaraid,1 # /dev/bus/0 [megaraid_disk_01], SCSI device\n/dev/bus/0 -d megaraid,2 # /dev/bus/0 [megaraid_disk_02], SCSI device\n/dev/bus/0 -d megaraid,3 # /dev/bus/0 [megaraid_disk_03], SCSI device\n/dev/bus/0 -d megaraid,4 # /dev/bus/0 [megaraid_disk_04], SCSI device\n/dev/bus/0 -d megaraid,5 # /dev/bus/0 [megaraid_disk_05], SCSI device\n/dev/bus/0 -d megaraid,6 # /dev/bus/0 [megaraid_disk_06], SCSI device\n/dev/bus/0 -d megaraid,7 # /dev/bus/0 [megaraid_disk_07], SCSI device\n/dev/bus/0 -d megaraid,8 # /dev/bus/0 [megaraid_disk_08], SCSI device\n/dev/bus/0 -d megaraid,9 # /dev/bus/0 [megaraid_disk_09], SCSI device\n/dev/bus/0 -d megaraid,10 # /dev/bus/0 [megaraid_disk_10], SCSI device\n/dev/bus/0 -d megaraid,11 # /dev/bus/0 [megaraid_disk_11], SCSI device\n/dev/bus/0 -d megaraid,12 # /dev/bus/0 [megaraid_disk_12], SCSI device\n/dev/bus/0 -d megaraid,13 # /dev/bus/0 [megaraid_disk_13], SCSI device",
			sgDevices:       []string{"/dev/sg0"},
			expectedDevices: []string{"/dev/sda", "/dev/bus/0 -d megaraid,0", "/dev/bus/0 -d megaraid,1", "/dev/bus/0 -d megaraid,2", "/dev/bus/0 -d megaraid,3", "/dev/bus/0 -d megaraid,4", "/dev/bus/0 -d megaraid,5", "/dev/bus/0 -d megaraid,6", "/dev/bus/0 -d megaraid,7", "/dev/bus/0 -d megaraid,8", "/dev/bus/0 -d megaraid,9", "/dev/bus/0 -d megaraid,10", "/dev/bus/0 -d megaraid,11", "/dev/bus/0 -d megaraid,12", "/dev/bus/0 -d megaraid,13"},
		},
		{
			name:            "proxmox2",
			input:           "/dev/sda -d scsi # /dev/sda, SCSI device\n/dev/bus/0 -d megaraid,0 # /dev/bus/0 [megaraid_disk_00], SCSI device\n/dev/bus/0 -d megaraid,1 # /dev/bus/0 [megaraid_disk_01], SCSI device",
			sgDevices:       []string{"/dev/sg0", "/dev/sg1"},
			expectedDevices: []string{"/dev/sda", "/dev/bus/0 -d megaraid,0", "/dev/bus/0 -d megaraid,1", "/dev/sg1"},
		},
	}

	for _, testCase := range testCases {
		tc := testCase

		t.Run(tc.name, func(t *testing.T) {
			iw := inputWrapper{
				Smart:     &smart.Smart{},
				sgDevices: tc.sgDevices,
			}

			devices := iw.parseScanOutput([]byte(tc.input))
			if diff := cmp.Diff(tc.expectedDevices, devices); diff != "" {
				t.Errorf("Unexpected devices (-want +got):\n%s", diff)
			}
		})
	}
}
