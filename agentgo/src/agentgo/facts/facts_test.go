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
