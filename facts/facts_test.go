// Copyright 2015-2019 Bleemeo
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

package facts

import (
	"reflect"
	"testing"
)

func TestDecodeOsRelease(t *testing.T) {
	in := `NAME="Ubuntu"
VERSION="18.04.2 LTS (Bionic Beaver)"
ID=ubuntu
ID_LIKE=debian
PRETTY_NAME="Ubuntu 18.04.2 LTS"
VERSION_ID="18.04"
HOME_URL="https://www.ubuntu.com/"
SUPPORT_URL="https://help.ubuntu.com/"
BUG_REPORT_URL="https://bugs.launchpad.net/ubuntu/"
PRIVACY_POLICY_URL="https://www.ubuntu.com/legal/terms-and-policies/privacy-policy"
VERSION_CODENAME=bionic
UBUNTU_CODENAME=bionic
`
	want := map[string]string{
		"NAME":               "Ubuntu",
		"VERSION":            "18.04.2 LTS (Bionic Beaver)",
		"ID":                 "ubuntu",
		"ID_LIKE":            "debian",
		"PRETTY_NAME":        "Ubuntu 18.04.2 LTS",
		"VERSION_ID":         "18.04",
		"HOME_URL":           "https://www.ubuntu.com/",
		"SUPPORT_URL":        "https://help.ubuntu.com/",
		"BUG_REPORT_URL":     "https://bugs.launchpad.net/ubuntu/",
		"PRIVACY_POLICY_URL": "https://www.ubuntu.com/legal/terms-and-policies/privacy-policy",
		"VERSION_CODENAME":   "bionic",
		"UBUNTU_CODENAME":    "bionic",
	}

	got, err := decodeOsRelease(in)
	if err != nil {
		t.Error(err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("decodeOsRelease(...) == %v, want %v", got, want)
	}
}

func TestByteCountDecimal(t *testing.T) {
	in := uint64(5540000000000000000)

	want := "4.81 EB"

	got := byteCountDecimal(in)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("TEstbyteCountDecimal(...) == %s, want %s", got, want)
	}
}
