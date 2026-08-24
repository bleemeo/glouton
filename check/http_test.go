// Copyright 2015-2026 Bleemeo
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

package check

import (
	"net"
	"testing"

	"github.com/bleemeo/glouton/types"
)

func TestNewHTTP_mainTCPAddress(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{name: "ipv4", url: "http://172.17.0.5:8080/", want: "172.17.0.5:8080"},
		{name: "ipv6", url: "http://[2001:db8::1]:8080/", want: "[2001:db8::1]:8080"},
		{name: "ipv6-default-port", url: "http://[2001:db8::1]/", want: "[2001:db8::1]:80"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hc := NewHTTP(c.url, "", nil, false, 0, nil, types.MetricAnnotations{}, nil)

			if hc.mainTCPAddress != c.want {
				t.Errorf("NewHTTP(%q).mainTCPAddress = %q, want %q", c.url, hc.mainTCPAddress, c.want)
			}

			if _, _, err := net.SplitHostPort(hc.mainTCPAddress); err != nil {
				t.Errorf("net.SplitHostPort(%q) failed: %v", hc.mainTCPAddress, err)
			}
		})
	}
}
