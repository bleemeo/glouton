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
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/utils/gloutonexec"
)

func testHostFS() fstest.MapFS {
	return fstest.MapFS{
		"usr/sbin/apache2":                      {Mode: 0o755},
		"usr/sbin/nginx":                        {Mode: 0o755},
		"usr/bin/redis-server":                  {Mode: 0o755},
		"usr/lib/postgresql/16/bin/postgres":    {Mode: 0o755},
		"usr/local/bin/redis-server":            {Mode: 0o755},
		"usr/bin/java":                          {Mode: 0o755},
		"usr/bin/python3":                       {Mode: 0o755},
		"usr/lib/erlang/erts-13.2/bin/beam.smp": {Mode: 0o755},
		"etc/apache2/apache2.conf":              {Mode: 0o644},
		"usr/sbin":                              {Mode: os.ModeDir | 0o755},
	}
}

func TestGuessExecutable(t *testing.T) {
	t.Parallel()

	hostFS := testHostFS()

	if got := guessExecutable(nil, []string{"/usr/sbin/apache2", "-k", "start"}); got != "" {
		t.Errorf("guessExecutable() with no host filesystem = %q, want \"\"", got)
	}

	tests := []struct {
		name    string
		cmdLine []string
		want    string
	}{
		{
			name:    "absolute executable",
			cmdLine: []string{"/usr/sbin/apache2", "-k", "start"},
			want:    "/usr/sbin/apache2",
		},
		{
			name:    "rewritten into a single string, path first",
			cmdLine: []string{"/usr/bin/redis-server 127.0.0.1:6379"},
			want:    "/usr/bin/redis-server",
		},
		{
			name:    "rewritten into a single string, path after the name",
			cmdLine: []string{"nginx: master process /usr/sbin/nginx -g daemon on; master_process on;"},
			want:    "/usr/sbin/nginx",
		},
		{
			name:    "java",
			cmdLine: []string{"/usr/bin/java", "-cp", "/usr/share/cassandra/*", "org.apache.cassandra.service.CassandraDaemon"},
			want:    "",
		},
		{
			name:    "python",
			cmdLine: []string{"/usr/bin/python3", "/usr/bin/fail2ban-server", "-xf", "start"},
			want:    "",
		},
		{
			name:    "erlang",
			cmdLine: []string{"/usr/lib/erlang/erts-13.2/bin/beam.smp", "--", "-s", "rabbit", "boot"},
			want:    "",
		},
		{
			name:    "relative path",
			cmdLine: []string{"apache2", "-k", "start"},
			want:    "",
		},
		{
			name:    "rewritten without a path",
			cmdLine: []string{"postgres: 16/main: checkpointer"},
			want:    "",
		},
		{
			name:    "missing file",
			cmdLine: []string{"/usr/sbin/mysqld"},
			want:    "",
		},
		{
			name:    "not executable",
			cmdLine: []string{"/etc/apache2/apache2.conf"},
			want:    "",
		},
		{
			name:    "directory",
			cmdLine: []string{"/usr/sbin"},
			want:    "",
		},
		{
			name:    "not a clean path",
			cmdLine: []string{"/usr/lib/../sbin/apache2"},
			want:    "",
		},
		{
			name:    "root",
			cmdLine: []string{"/"},
			want:    "",
		},
		{
			name:    "empty command line",
			cmdLine: nil,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := guessExecutable(hostFS, tt.cmdLine); got != tt.want {
				t.Errorf("guessExecutable(%q) = %q, want %q", tt.cmdLine, got, tt.want)
			}
		})
	}
}

// TestDynamicDiscoveryGuessesExePath checks that discovery falls back on the command line
// for the executable of a host process only, and only when the process lister couldn't
// read it.
func TestDynamicDiscoveryGuessesExePath(t *testing.T) {
	t.Parallel()

	const containerID = "c1"

	dd := NewDynamic(Option{
		PS: mockProcess{
			[]facts.Process{
				{PID: 10, CmdLineList: []string{"/usr/sbin/apache2", "-k", "start"}, Status: "S"},
				{PID: 20, CmdLineList: []string{"nginx: master process /usr/sbin/nginx -g daemon on; master_process on;"}, Status: "S"},
				// The process listening is the one kept first, its command line tells nothing.
				{PID: 30, CmdLineList: []string{"postgres: 16/main: postmaster"}, Status: "S"},
				{PID: 31, CmdLineList: []string{"/usr/lib/postgresql/16/bin/postgres", "-D", "/var/lib/postgresql/16/main"}, Status: "S"},
				// Its command line names a path of the container, which the host happens to have too.
				{
					PID:           40,
					CmdLineList:   []string{"/usr/local/bin/redis-server *:6379"},
					Status:        "S",
					ContainerID:   containerID,
					ContainerName: testRedis,
				},
				// What the process lister read always wins.
				{PID: 50, CmdLineList: []string{"/usr/bin/memcached", "-m", "64"}, Status: "S", Executable: "/opt/memcached/bin/memcached"},
			},
		},
		Netstat: mockNetstat{result: map[int][]facts.ListenAddress{
			30: {{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 5432}},
		}},
		ContainerInfo: mockContainerInfo{
			containers: map[string]facts.FakeContainer{
				containerID: {FakeID: containerID, FakeContainerName: testRedis},
			},
		},
		IsContainerIgnored: facts.ContainerFilter{}.ContainerIgnored,
		HostFS:             testHostFS(),
	})

	services, _, err := dd.Discovery(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	want := map[ServiceName]string{
		ApacheService:     "/usr/sbin/apache2",
		NginxService:      "/usr/sbin/nginx",
		PostgreSQLService: "/usr/lib/postgresql/16/bin/postgres",
		RedisService:      "",
		MemcachedService:  "/opt/memcached/bin/memcached",
	}

	got := make(map[ServiceName]string, len(services))

	for _, srv := range services {
		got[srv.ServiceType] = srv.ExePath
	}

	for serviceType, wantPath := range want {
		gotPath, ok := got[serviceType]
		if !ok {
			t.Errorf("service %s wasn't discovered", serviceType)

			continue
		}

		if gotPath != wantPath {
			t.Errorf("ExePath of %s = %q, want %q", serviceType, gotPath, wantPath)
		}
	}
}

// TestSetServiceActiveReadsExePathOnHost checks that the executable of an absent service
// is looked for on the host's filesystem, which isn't Glouton's own when it runs in a container.
func TestSetServiceActiveReadsExePathOnHost(t *testing.T) {
	t.Parallel()

	hostRoot := t.TempDir()
	exePath := "/usr/sbin/apache2"

	if err := os.MkdirAll(filepath.Join(hostRoot, "usr", "sbin"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(hostRoot, exePath), nil, 0o755); err != nil { //nolint:gosec // An executable is what's tested.
		t.Fatal(err)
	}

	d := &Discovery{
		commandRunner:                  gloutonexec.New(hostRoot),
		absentServiceDeactivationDelay: time.Hour,
	}

	service := Service{
		Name:         string(ApacheService),
		ServiceType:  ApacheService,
		ExePath:      exePath,
		Active:       true,
		LastTimeSeen: time.Now(),
	}

	if got := d.setServiceActiveAndContainer(service); !got.Active {
		t.Errorf("service was deactivated while %s exists on the host", exePath)
	}

	if err := os.Remove(filepath.Join(hostRoot, exePath)); err != nil {
		t.Fatal(err)
	}

	if got := d.setServiceActiveAndContainer(service); got.Active {
		t.Errorf("service is still active while %s was removed from the host", exePath)
	}
}

// TestSetServiceActiveFallsBackOnDelay checks that a service whose executable can't be
// looked for is deactivated once it hasn't been seen for absentServiceDeactivationDelay:
// when its ExePath is unknown, or when the host's filesystem isn't reachable (Glouton
// runs in a container without df.host_mount_point).
func TestSetServiceActiveFallsBackOnDelay(t *testing.T) {
	t.Parallel()

	const delay = time.Hour

	tests := []struct {
		name         string
		hostRootPath string
		exePath      string
	}{
		{name: "unknown executable", hostRootPath: "/", exePath: ""},
		{name: "host root path unset", hostRootPath: "", exePath: "/usr/sbin/apache2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := &Discovery{
				commandRunner:                  gloutonexec.New(tt.hostRootPath),
				absentServiceDeactivationDelay: delay,
			}

			service := Service{
				Name:         string(ApacheService),
				ServiceType:  ApacheService,
				ExePath:      tt.exePath,
				Active:       true,
				LastTimeSeen: time.Now().Add(-delay / 2),
			}

			if got := d.setServiceActiveAndContainer(service); !got.Active {
				t.Errorf("service was deactivated after %s, before the delay", delay/2)
			}

			service.LastTimeSeen = time.Now().Add(-2 * delay)

			if got := d.setServiceActiveAndContainer(service); got.Active {
				t.Errorf("service is still active after %s, past the delay", 2*delay)
			}
		})
	}
}
