package facts

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

func TestDecodeDocker(t *testing.T) {
	// Test case are generated using:
	// docker run --rm -ti --name test \
	//     -v /var/run/docker.sock:/var/run/docker.sock \
	//     bleemeo/bleemeo-agent \
	//     python3 -c 'import docker;
	//     print(docker.APIClient(version="1.21").top("test"))'
	cases := []container.ContainerTopOKBody{
		// Boot2Docker 1.12.3 first boot
		{
			Processes: [][]string{
				{
					"3216", "root",
					"python3 -c import docker;print(docker.Client(version=\"1.21\").top(\"test\"))",
				},
			},
			Titles: []string{"PID", "USER", "COMMAND"},
		},
		// Boot2Docker 1.12.3 second boot
		{
			Titles: []string{
				"UID", "PID", "PPID", "C", "STIME", "TTY", "TIME", "CMD",
			},
			Processes: [][]string{
				{
					"root", "1551", "1542", "0", "14:13", "pts/1", "00:00:00",
					"python3 -c import docker;print(docker.Client(version=\"1.21\").top(\"test\"))",
				},
			},
		},
		// Ubuntu 16.04
		{
			Processes: [][]string{
				{
					"root", "5017", "4988", "0", "15:15", "pts/29", "00:00:00",
					"python3 -c import docker;print(docker.Client(version=\"1.21\").top(\"test\"))"},
			},
			Titles: []string{
				"UID", "PID", "PPID", "C", "STIME", "TTY", "TIME", "CMD",
			},
		},
		// With ps_args="waux" added. On Ubuntu 18.04
		{
			Processes: [][]string{
				{"root", "28554", "39.0", "0.1", "85640", "28496", "pts/0", "Ss+", "11:43", "0:00", "python3 -c import docker;print(docker.APIClient(version=\"1.21\").top(\"test\", ps_args=\"waux\"))"},
			},
			Titles: []string{"USER", "PID", "%CPU", "%MEM", "VSZ", "RSS", "TTY", "STAT", "START", "TIME", "COMMAND"}},
	}
	for i, c := range cases {
		got := decodeDocker(c, "theDockerID")
		if len(got) != 1 {
			t.Errorf("Case #%v: len(got) == %v, want 1", i, len(got))
		}
		if got[0].CmdLine[0] != "python3" {
			t.Errorf("Case #%v: CmdLine[0] == %v, want %v", i, got[0].CmdLine[0], "python3")
		}
		if got[0].ContainerID != "theDockerID" {
			t.Errorf("Case #%v: ContainerID == %v, want %v", i, got[0].ContainerID, "theDockerID")
		}
	}
}

func TestPsTime2Second(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"00:16:42", 16*60 + 42},
		{"16:42", 16*60 + 42},
		{"1-02:27:14", 24*3600 + 2*3600 + 27*60 + 14},
		{"1587:14", 1587*60 + 14},

		// busybox time
		{"12h27", 12*3600 + 27*60},
		{"6d09", 6*24*3600 + 9*3600},
		{"18d12", 18*24*3600 + 12*3600},
	}
	for _, c := range cases {
		got, err := psTime2Second(c.in)
		if err != nil {
			t.Errorf("psTime2second(%#v) raise %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("psTime2second(%#v) == %v, want %v", c.in, got, c.want)
		}
	}
}

func TestPsStat2Status(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"D", "disk-sleep"},
		{"I", "?"},
		{"I<", "?"},
		{"R+", "running"},
		{"Rl", "running"},
		{"S", "sleeping"},
		{"S<", "sleeping"},
		{"S<l", "sleeping"},
		{"SLl+", "sleeping"},
		{"Ss", "sleeping"},
		{"Z+", "zombie"},
		{"T", "stopped"},
		{"t", "tracing-stop"},
	}
	for _, c := range cases {
		got := psStat2Status(c.in)
		if got != c.want {
			t.Errorf("psTime2second(%#v) == %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestContainerIDFromCGroupData(t *testing.T) {

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"minikube v0.28.2",
			`11:freezer:/kubepods/besteffort/pod8f469a2e-bcd6-11e8-abe9-080027ae1159/bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6
10:perf_event:/kubepods/besteffort/pod8f469a2e-bcd6-11e8-abe9-080027ae1159/bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6
[...]
1:name=systemd:/kubepods/besteffort/pod8f469a2e-bcd6-11e8-abe9-080027ae1159/bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6
`,
			"bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6",
		},
		{
			"Docker on Ubuntu",
			`12:pids:/docker/bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6
11:hugetlb:/docker/bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6
[...]
1:cpuset:/docker/bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6`,
			"bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6",
		},
		{
			"Docker on CentOS",
			`11:cpuset:/system.slice/docker-bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6.scope
10:devices:/system.slice/docker-bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6.scope
[...]
1:name=systemd:/system.slice/docker-bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6.scope`,
			"bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6",
		},
	}

	for _, c := range cases {
		got := containerIDFromCGroupData(c.in)
		if got != c.want {
			t.Errorf("containerIDFromCGroupData([%s]) == %#v, want %#v", c.name, got, c.want)
		}
	}
}

type mockDockerProcess struct {
	processesResult        []Process
	containerID2NameResult map[string]string
}

func (m mockDockerProcess) processes(ctx context.Context, maxAge time.Duration) (processes []Process, err error) {
	return m.processesResult, nil
}
func (m mockDockerProcess) containerID2Name(ctx context.Context, maxAge time.Duration) (containerID2Name map[string]string, err error) {
	return m.containerID2NameResult, nil
}

func TestUpdateProcesses(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	pp := ProcessProvider{
		dp: mockDockerProcess{
			processesResult: []Process{
				{
					PID:         12,
					Name:        "redis",
					ContainerID: "redis-container-id",
				},
				{
					PID:         42,
					Name:        "mysql",
					ContainerID: "mysql-container-id",
				},
			},
			containerID2NameResult: map[string]string{
				"mysql-container-id":  "mysql-name",
				"golang-container-id": "golang-name",
			},
		},
		psutil: mockDockerProcess{
			processesResult: []Process{
				{
					PID:         1,
					Name:        "init",
					CreateTime:  t0,
					ContainerID: "",
				},
				{
					PID:         12,
					Name:        "redis2",
					CreateTime:  t0,
					ContainerID: "",
				},
				{
					PID:         1337,
					Name:        "golang",
					CreateTime:  t0,
					ContainerID: "",
				},
			},
		},
		containerIDFromCGroup: func(pid int) string {
			switch pid {
			case 1:
				return ""
			case 1337:
				return "golang-container-id"
			default:
				t.Errorf("containerIDFromCGroup called with pid==%d", pid)
				return ""
			}
		},
	}
	pp.l.Lock()
	err := pp.updateProcesses(context.Background())
	pp.l.Unlock()
	if err != nil {
		t.Error(err)
	}
	cases := []Process{
		{
			PID:         12,
			Name:        "redis2",
			ContainerID: "redis-container-id",
			CreateTime:  t0,
		},
		{
			PID:         42,
			Name:        "mysql",
			ContainerID: "mysql-container-id",
		},
		{
			PID:         1,
			Name:        "init",
			ContainerID: "",
			CreateTime:  t0,
		},
		{
			PID:         1337,
			Name:        "golang",
			ContainerID: "golang-container-id",
			CreateTime:  t0,
		},
	}
	if len(pp.processes) != len(cases) {
		t.Errorf("len(pp.processe) == %v, want %v", len(pp.processes), len(cases))
	}
	for _, c := range cases {
		got := pp.processes[c.PID]
		if !reflect.DeepEqual(got, c) {
			t.Errorf("pp.processes[%v] == %v, want %v", c.PID, got, c)
		}
	}
}
