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

package nrpe

import (
	"errors"
	"reflect"
	"testing"
)

const nrpeConf1 = `
# NRPE Commands
command[check_users]=/usr/local/nagios/libexec/check_users -w 5 -c 10
# Other parameters
pid_file=/var/run/nagios/nrpe.pid
include=/etc/nagios/nrpe_local.cfg
`
const nrpeConf2 = `
# NRPE Commands
command[check_load]=/usr/local/nagios/libexec/check_load -r -w .15,.10,.05 -c .30,.25,.20
command[check_zombie_procs]=/usr/local/nagios/libexec/check_procs -w 5 -c 10 -s Z
# Other parameters
connection_timeout=300
dont_blame_nrpe=0
`
const nrpeConf3 = `
# NRPE Commands
command[check_users]=new command
command[check_hda1]=/usr/local/nagios/libexec/check_disk -w 20% -c 10% -p /dev/hda1
command[check_zombie_procs]=new command again
# Other parameters
pid_file=/var/run/nagios/nrpe.pid
dont_blame_nrpe=1
`
const nrpeConf4 = `
dont_blame_nrpe=1
`

const nrpeConf5 = `
# Empty configuration file
`

const nrpeConf6 = `
dont_blame_nrpe=0
# NRPE Command
command[list_partitions]=lsblk
`

const nrpeConf7 = `
command[check_with_unexpected_char]=command with [] and =
command[check_event_worse]=command[check_event_worse]=ls
command[ check with space]= command --followed-by-tailing-space
command[strange command?/$µ]=strange command characters §#@

# Note: nagios-nrpe-server don't support "dont_blame_nrpe =1", but support the
# following:
dont_blame_nrpe= 1
`

func TestReadNRPEConfFile(t *testing.T) {
	type Entries struct {
		Bytes []byte
		Map   map[string]string
	}

	type Want struct {
		Map              map[string]string
		CommandArguments bool
	}

	cases := []struct {
		Entries Entries
		Want    Want
	}{
		{
			Entries: Entries{
				Bytes: []byte(nrpeConf1),
				Map:   make(map[string]string),
			},
			Want: Want{
				Map: map[string]string{
					"check_users": "/usr/local/nagios/libexec/check_users -w 5 -c 10",
				},
				CommandArguments: false,
			},
		},
		{
			Entries: Entries{
				Bytes: []byte(nrpeConf2),
				Map: map[string]string{
					"check_users": "/usr/local/nagios/libexec/check_users -w 5 -c 10",
				},
			},
			Want: Want{
				Map: map[string]string{
					"check_users":        "/usr/local/nagios/libexec/check_users -w 5 -c 10",
					"check_load":         "/usr/local/nagios/libexec/check_load -r -w .15,.10,.05 -c .30,.25,.20",
					"check_zombie_procs": "/usr/local/nagios/libexec/check_procs -w 5 -c 10 -s Z",
				},
				CommandArguments: false,
			},
		},
		{
			Entries: Entries{
				Bytes: []byte(nrpeConf3),
				Map: map[string]string{
					"check_users":        "/usr/local/nagios/libexec/check_users -w 5 -c 10",
					"check_load":         "/usr/local/nagios/libexec/check_load -r -w .15,.10,.05 -c .30,.25,.20",
					"check_zombie_procs": "/usr/local/nagios/libexec/check_procs -w 5 -c 10 -s Z",
				},
			},
			Want: Want{
				Map: map[string]string{
					"check_users":        "new command",
					"check_load":         "/usr/local/nagios/libexec/check_load -r -w .15,.10,.05 -c .30,.25,.20",
					"check_zombie_procs": "new command again",
					"check_hda1":         "/usr/local/nagios/libexec/check_disk -w 20% -c 10% -p /dev/hda1",
				},
				CommandArguments: true,
			},
		},
		{
			Entries: Entries{
				Bytes: []byte(nrpeConf4),
				Map:   make(map[string]string),
			},
			Want: Want{
				Map:              make(map[string]string),
				CommandArguments: true,
			},
		},
		{
			Entries: Entries{
				Bytes: []byte(nrpeConf5),
				Map:   make(map[string]string),
			},
			Want: Want{
				Map:              make(map[string]string),
				CommandArguments: false,
			},
		},
		{
			Entries: Entries{
				Bytes: []byte(nrpeConf6),
				Map:   make(map[string]string),
			},
			Want: Want{
				Map: map[string]string{
					"list_partitions": "lsblk",
				},
				CommandArguments: false,
			},
		},
		{
			Entries: Entries{
				Bytes: []byte(nrpeConf7),
				Map:   make(map[string]string),
			},
			Want: Want{
				Map: map[string]string{
					"check_with_unexpected_char": "command with [] and =",
					"check_event_worse":          "command[check_event_worse]=ls",
					" check with space":          " command --followed-by-tailing-space",
					"strange command?/$µ":        "strange command characters §#@",
				},
				CommandArguments: true,
			},
		},
	}

	for _, c := range cases {
		mapResult, commandArgumentsResult := readNRPEConfFile(c.Entries.Bytes, c.Entries.Map)
		if !reflect.DeepEqual(mapResult, c.Want.Map) {
			t.Errorf("readNRPEConfFile(args) == %v, want %v", mapResult, c.Want.Map)
		}

		if commandArgumentsResult != c.Want.CommandArguments {
			t.Errorf("readNRPEConfFile(args) == %v, want %v", commandArgumentsResult, c.Want.CommandArguments)
		}
	}
}

func TestReturnCommand(t *testing.T) {
	type Entries struct {
		Responder Responder
		Args      []string
	}

	type Want struct {
		Command []string
		Err     error
	}

	cases := []struct {
		Entries Entries
		Want    Want
	}{
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option -h",
					},
					allowArguments: false,
				},
				Args: []string{"check_users"},
			},
			Want: Want{
				Command: []string{"command", "--option", "-h"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option -s",
					},
					allowArguments: true,
				},
				Args: []string{"check_users"},
			},
			Want: Want{
				Command: []string{"command", "--option", "-s"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option $ARG1$ -s",
					},
					allowArguments: true,
				},
				Args: []string{"check_users", "argument1"},
			},
			Want: Want{
				Command: []string{"command", "--option", "argument1", "-s"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option $ARG1$ -s",
					},
					allowArguments: true,
				},
				Args: []string{"check_users", "space in args"},
			},
			Want: Want{
				Command: []string{"command", "--option", "space", "in", "args", "-s"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option $ARG1$ -s '$ARG2$'",
					},
					allowArguments: true,
				},
				Args: []string{"check_users", "the argument one", "the argument two"},
			},
			Want: Want{
				Command: []string{"command", "--option", "the", "argument", "one", "-s", "the argument two"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option $ARG1$ -s \"$ARG2$\"",
					},
					allowArguments: true,
				},
				Args: []string{"check_users", "the argument one", "the argument two"},
			},
			Want: Want{
				Command: []string{"command", "--option", "the", "argument", "one", "-s", "the argument two"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option $ARG1$ -p $ARG1$",
					},
					allowArguments: true,
				},
				Args: []string{"check_users", "argument1", "1234"},
			},
			Want: Want{
				Command: []string{"command", "--option", "argument1", "-p", "argument1"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option $ARG1$ -h",
					},
					allowArguments: false,
				},
				Args: []string{"check_users", "argument1"},
			},
			Want: Want{
				Command: []string{"command", "--option", "-h"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option $ARG1$ -a",
					},
					allowArguments: true,
				},
				Args: []string{"check_users", "argument1", "argument2"},
			},
			Want: Want{
				Command: []string{"command", "--option", "argument1", "-a"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option $ARG1$ -a $ARG2$",
					},
					allowArguments: true,
				},
				Args: []string{"check_users", "argument1"},
			},
			Want: Want{
				Command: []string{"command", "--option", "argument1", "-a"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option",
					},
					allowArguments: true,
				},
				Args: []string{"check_users", "argument0"},
			},
			Want: Want{
				Command: []string{"command", "--option"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --args '$ARG1$@$ARG2$.com'",
					},
					allowArguments: true,
				},
				Args: []string{"check_users", "glouton", "bleemeo"},
			},
			Want: Want{
				Command: []string{"command", "--args", "glouton@bleemeo.com"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --args '$ARG1$ by $ARG2$'",
					},
					allowArguments: true,
				},
				Args: []string{"check_users", "glouton", "bleemeo"},
			},
			Want: Want{
				Command: []string{"command", "--args", "glouton by bleemeo"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --args '$ARG1$ by $ARG5$'",
					},
					allowArguments: true,
				},
				Args: []string{"check_users", "glouton", "bleemeo"},
			},
			Want: Want{
				Command: []string{"command", "--args", "glouton by "},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --args '$ARG1$ by $ARG5$'",
					},
					allowArguments: true,
				},
				Args: []string{"check_users", "glouton", "bleemeo company", "three as number", "four (4)", "the number five"},
			},
			Want: Want{
				Command: []string{"command", "--args", "glouton by the number five"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --args '$ARG1$ by $ARG1$'",
					},
					allowArguments: false,
				},
				Args: []string{"check_users", "glouton", "bleemeo"},
			},
			Want: Want{
				Command: []string{"command", "--args", " by "},
				Err:     nil,
			},
		},
	}

	for i, c := range cases {
		commandResult, errResult := c.Entries.Responder.returnCommand(c.Entries.Args)
		if !reflect.DeepEqual(commandResult, c.Want.Command) {
			t.Errorf("%v r.retunrCommand(%v) == '%v', want '%v'", i, c.Entries.Args, commandResult, c.Want.Command)
		}

		if !errors.Is(errResult, c.Want.Err) {
			t.Errorf("%v r.returnCommand(%v) == '%v', want '%v'", i, c.Entries.Args, errResult, c.Want.Err)
		}
	}
}
