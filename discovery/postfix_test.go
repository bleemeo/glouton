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

package discovery

import (
	"testing"

	"github.com/bleemeo/glouton/facts"
)

// TestPostfixSpoolPath covers which spool directory a Postfix service's queues are read
// from.
//
// Unlike Varnish there is no binary to run, so a containerised Postfix needs nothing but
// the path of its own spool: the input opens files, and /proc names them from wherever
// Glouton can read it. Getting this wrong is quiet rather than loud -- the machine's own
// spool is a real directory on a mail server, so the queues of one Postfix would be
// published under the name of another.
func TestPostfixSpoolPath(t *testing.T) {
	cases := []struct {
		testName string
		service  Service
		want     string
		wantOK   bool
	}{
		{
			// Not a container: the machine's own spool, which is all this could mean
			// before containers were handled.
			testName: "postfix on the machine",
			service: Service{ //nolint:exhaustruct
				Name:        string(PostfixService),
				ServiceType: PostfixService,
			},
			want:   "/var/spool/postfix",
			wantOK: true,
		},
		{
			testName: "containerised postfix reads its own spool",
			service: Service{ //nolint:exhaustruct
				Name:        string(PostfixService),
				Instance:    "test-postfix",
				ServiceType: PostfixService,
				ContainerID: "postfix1",
				container:   facts.FakeContainer{FakePID: 4242}, //nolint:exhaustruct
			},
			want:   "/proc/4242/root/var/spool/postfix",
			wantOK: true,
		},
		{
			// A container between states has no PID, so there is no /proc entry to walk.
			// Answering with the machine's spool instead would be the quiet failure above.
			testName: "container with no pid names nothing",
			service: Service{ //nolint:exhaustruct
				Name:        string(PostfixService),
				Instance:    "test-postfix",
				ServiceType: PostfixService,
				ContainerID: "postfix1",
				container:   facts.FakeContainer{FakePID: 0}, //nolint:exhaustruct
			},
			want:   "",
			wantOK: false,
		},
	}

	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			got, gotOK := postfixSpoolPath(c.service)

			if got != c.want {
				t.Errorf("postfixSpoolPath() = %q, want %q", got, c.want)
			}

			if gotOK != c.wantOK {
				t.Errorf("postfixSpoolPath() ok = %v, want %v", gotOK, c.wantOK)
			}
		})
	}
}
