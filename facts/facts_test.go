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

func TestByteCountDecimalMaxEB(t *testing.T) {
	in := uint64(5540000000000000000)

	want := "4.81 EB"

	if got := ByteCountDecimal(in); !reflect.DeepEqual(got, want) {
		t.Errorf("TEstbyteCountDecimal(...) == %s, want %s", got, want)
	}
}

func TestByteCountDecimalB(t *testing.T) {
	in := uint64(0)

	want := "0 B"

	if got := ByteCountDecimal(in); !reflect.DeepEqual(got, want) {
		t.Errorf("TEstbyteCountDecimal(...) == %s, want %s", got, want)
	}
}

func TestByteCountDecimalKB(t *testing.T) {
	in := uint64(1024)

	want := "1.00 KB"

	if got := ByteCountDecimal(in); !reflect.DeepEqual(got, want) {
		t.Errorf("TEstbyteCountDecimal(...) == %s, want %s", got, want)
	}
}

func TestByteCountDecimalMB(t *testing.T) {
	in := uint64(543288000)

	want := "518.12 MB"

	if got := ByteCountDecimal(in); !reflect.DeepEqual(got, want) {
		t.Errorf("TEstbyteCountDecimal(...) == %s, want %s", got, want)
	}
}

func TestByteCountDecimalGB(t *testing.T) {
	in := uint64(4432880000)

	want := "4.13 GB"

	if got := ByteCountDecimal(in); !reflect.DeepEqual(got, want) {
		t.Errorf("TEstbyteCountDecimal(...) == %s, want %s", got, want)
	}
}

func Test_decodeFreeBSDRouteGet(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantMac string
		wantIP  string
	}{
		{
			name: "TrueNAS-13.0-U4",
			data: `
RTA_DST: inet 8.8.8.8; RTA_IFP: link ; RTM_GET: Report Metrics: len 224, pid: 0, seq 1, errno 0, flags:<UP,GATEWAY,HOST,STATIC>
locks:  inits:
sockaddrs: <DST,IFP>
 8.8.8.8 link#0
   route to: 8.8.8.8
destination: 0.0.0.0
       mask: 0.0.0.0
    gateway: 192.168.205.1
        fib: 0
  interface: em0
      flags: <UP,GATEWAY,DONE,STATIC>
 recvpipe  sendpipe  ssthresh  rtt,msec    mtu        weight    expire
       0         0         0         0      1500         1         0

locks:  inits:
sockaddrs: <DST,GATEWAY,NETMASK,IFP,IFA>
 0.0.0.0 192.168.205.1 0.0.0.0 em0:8a.79.fc.cd.e9.d0 192.168.205.12
`,
			wantMac: "8a:79:fc:cd:e9:d0",
			wantIP:  "192.168.205.12",
		},
		{
			name: "FreeBSD 13.1-RELEASE",
			data: `
RTA_DST: inet 8.8.8.8; RTA_IFP: link ; RTM_GET: Report Metrics: len 224, pid: 0, seq 1, errno 0, flags:<UP,GATEWAY,HOST,STATIC>
locks:  inits:
sockaddrs: <DST,IFP>
 8.8.8.8 link#0
   route to: 8.8.8.8
destination: 0.0.0.0
       mask: 0.0.0.0
    gateway: 192.168.205.1
        fib: 0
  interface: vtnet0
      flags: <UP,GATEWAY,DONE,STATIC>
 recvpipe  sendpipe  ssthresh  rtt,msec    mtu        weight    expire
       0         0         0         0      1500         1         0

locks:  inits:
sockaddrs: <DST,GATEWAY,NETMASK,IFP,IFA>
 0.0.0.0 192.168.205.1 0.0.0.0 vtnet0:52.55.5f.37.31.b5 192.168.205.13
`,
			wantMac: "52:55:5f:37:31:b5",
			wantIP:  "192.168.205.13",
		},
		{
			name: "TrueNAS-12.0-RELEASE",
			data: `
RTA_DST: inet 8.8.8.8; RTA_IFP: link ; RTM_GET: Report Metrics: len 224, pid: 0, seq 1, errno 0, flags:<UP,GATEWAY,HOST,STATIC>
locks:  inits: 
sockaddrs: <DST,IFP>
 8.8.8.8 link#0
   route to: 8.8.8.8
destination: 0.0.0.0
       mask: 0.0.0.0
    gateway: 192.168.205.1
        fib: 0
  interface: em0
      flags: <UP,GATEWAY,DONE,STATIC>
 recvpipe  sendpipe  ssthresh  rtt,msec    mtu        weight    expire
       0         0         0         0      1500         1         0 

locks:  inits: 
sockaddrs: <DST,GATEWAY,NETMASK,IFP,IFA>
 0.0.0.0 192.168.205.1 0.0.0.0 em0:52.55.f3.c4.ce.e0 192.168.205.19
 `,
			wantMac: "52:55:f3:c4:ce:e0",
			wantIP:  "192.168.205.19",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotIP, gotMac := decodeFreeBSDRouteGet(tt.data)
			if gotIP != tt.wantIP {
				t.Errorf("decodeFreeBSDRouteGet() IP = %v, want %v", gotIP, tt.wantIP)
			}

			if gotMac != tt.wantMac {
				t.Errorf("decodeFreeBSDRouteGet() MAC = %v, want %v", gotMac, tt.wantMac)
			}
		})
	}
}

func Test_decodeFreeBSDVersion(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "TrueNAS-13.0-U4",
			data: "TrueNAS-13.0-U4 (e5af99be6d)",
			want: map[string]string{
				"NAME":        "TrueNAS",
				"VERSION_ID":  "13.0-U4",
				"PRETTY_NAME": "TrueNAS 13.0-U4",
			},
		},
		{
			name: "TrueNAS-12.0-RELEASE",
			data: "TrueNAS-12.0-RELEASE (f862218137)",
			want: map[string]string{
				"NAME":        "TrueNAS",
				"VERSION_ID":  "12.0-RELEASE",
				"PRETTY_NAME": "TrueNAS 12.0-RELEASE",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeFreeBSDVersion(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("decodeFreeBSDVersion() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("decodeFreeBSDVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_bytesToString(t *testing.T) {
	tests := []struct {
		name   string
		buffer []byte
		want   string
	}{
		{
			name:   "Test to make linter happy, else bytesToString isn't used on Windows",
			buffer: []byte{'a', 'b', 0, 0},
			want:   "ab",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bytesToString(tt.buffer); got != tt.want {
				t.Errorf("bytesToString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_tzFromSymlink(t *testing.T) {
	tests := []struct {
		name          string
		symlinkTarget string
		want          string
	}{
		{
			name:          "almalinux8-1",
			symlinkTarget: "../usr/share/zoneinfo/UTC",
			want:          "UTC",
		},
		{
			name:          "almalinux8-2",
			symlinkTarget: "../usr/share/zoneinfo/Pacific/Guadalcanal",
			want:          "Pacific/Guadalcanal",
		},
		{
			name:          "ubuntu2204-1",
			symlinkTarget: "/usr/share/zoneinfo/Etc/UTC",
			want:          "Etc/UTC",
		},
		{
			name:          "ubuntu2204-1",
			symlinkTarget: "/usr/share/zoneinfo/America/Cancun",
			want:          "America/Cancun",
		},
		{
			name:          "avoid-crash",
			symlinkTarget: "/usr/share/zoneinfo",
			want:          "",
		},
		{
			name:          "unknown-link",
			symlinkTarget: "/usr/local/myfile/UTC",
			want:          "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tzFromSymlink(tt.symlinkTarget); got != tt.want {
				t.Errorf("tzFromSymlink() = %v, want %v", got, tt.want)
			}
		})
	}
}
