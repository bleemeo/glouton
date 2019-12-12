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
	"fmt"
	"reflect"
	"testing"
)

var nrpeConf1 = `
# NRPE Commands
command[check_users]=/usr/local/nagios/libexec/check_users -w 5 -c 10
# Other parameters
pid_file=/var/run/nagios/nrpe.pid
include=/etc/nagios/nrpe_local.cfg
`
var nrpeConf2 = `
# NRPE Commands
command[check_load]=/usr/local/nagios/libexec/check_load -r -w .15,.10,.05 -c .30,.25,.20
command[check_zombie_procs]=/usr/local/nagios/libexec/check_procs -w 5 -c 10 -s Z
# Other parameters
connection_timeout=300
dont_blame_nrpe=0
`
var nrpeConf3 = `
# NRPE Commands
command[check_users]=new command
command[check_hda1]=/usr/local/nagios/libexec/check_disk -w 20% -c 10% -p /dev/hda1
command[check_zombie_procs]=new command again
# Other parameters
pid_file=/var/run/nagios/nrpe.pid
dont_blame_nrpe=1
`
var nrpeConf4 = `
dont_blame_nrpe=1
`

var nrpeConf5 = `
# Empty configuration file
`

var nrpeConf6 = `
dont_blame_nrpe=0
# NRPE Command
command[list_partitions]=lsblk
`

func TestReadNRPEConfFile(t *testing.T) {
	type Entries struct {
		Bytes            []byte
		Map              map[string]string
		CommandArguments CommandArguments
	}
	type Want struct {
		Map              map[string]string
		CommandArguments CommandArguments
	}
	cases := []struct {
		Entries Entries
		Want    Want
	}{
		{
			Entries: Entries{
				Bytes:            []byte(nrpeConf1),
				Map:              make(map[string]string),
				CommandArguments: undefined,
			},
			Want: Want{
				Map: map[string]string{
					"check_users": "/usr/local/nagios/libexec/check_users -w 5 -c 10",
				},
				CommandArguments: undefined,
			},
		},
		{
			Entries: Entries{
				Bytes: []byte(nrpeConf2),
				Map: map[string]string{
					"check_users": "/usr/local/nagios/libexec/check_users -w 5 -c 10",
				},
				CommandArguments: undefined,
			},
			Want: Want{
				Map: map[string]string{
					"check_users":        "/usr/local/nagios/libexec/check_users -w 5 -c 10",
					"check_load":         "/usr/local/nagios/libexec/check_load -r -w .15,.10,.05 -c .30,.25,.20",
					"check_zombie_procs": "/usr/local/nagios/libexec/check_procs -w 5 -c 10 -s Z",
				},
				CommandArguments: notAllowed,
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
				CommandArguments: notAllowed,
			},
			Want: Want{
				Map: map[string]string{
					"check_users":        "new command",
					"check_load":         "/usr/local/nagios/libexec/check_load -r -w .15,.10,.05 -c .30,.25,.20",
					"check_zombie_procs": "new command again",
					"check_hda1":         "/usr/local/nagios/libexec/check_disk -w 20% -c 10% -p /dev/hda1",
				},
				CommandArguments: allowed,
			},
		},
		{
			Entries: Entries{
				Bytes:            []byte(nrpeConf4),
				Map:              make(map[string]string),
				CommandArguments: undefined,
			},
			Want: Want{
				Map:              make(map[string]string),
				CommandArguments: allowed,
			},
		},
		{
			Entries: Entries{
				Bytes:            []byte(nrpeConf5),
				Map:              make(map[string]string),
				CommandArguments: allowed,
			},
			Want: Want{
				Map:              make(map[string]string),
				CommandArguments: undefined,
			},
		},
		{
			Entries: Entries{
				Bytes:            []byte(nrpeConf6),
				Map:              make(map[string]string),
				CommandArguments: allowed,
			},
			Want: Want{
				Map: map[string]string{
					"list_partitions": "lsblk",
				},
				CommandArguments: notAllowed,
			},
		},
	}

	for _, c := range cases {
		mapResult, commandArgumentsResult := readNRPEConfFile(c.Entries.Bytes, c.Entries.Map, c.Entries.CommandArguments)
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
						"check_users": "command --option -a",
					},
					allowArguments: false,
				},
				Args: []string{"check_users"},
			},
			Want: Want{
				Command: []string{"command", "--option", "-a"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option -a",
					},
					allowArguments: true,
				},
				Args: []string{"check_users"},
			},
			Want: Want{
				Command: []string{"command", "--option", "-a"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option $ARG0$ -a",
					},
					allowArguments: true,
				},
				Args: []string{"check_users", "argument"},
			},
			Want: Want{
				Command: []string{"command", "--option", "argument", "-a"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option $ARG0$ -a $ARG1$",
					},
					allowArguments: true,
				},
				Args: []string{"check_users", "argument0", "argument1"},
			},
			Want: Want{
				Command: []string{"command", "--option", "argument0", "-a", "argument1"},
				Err:     nil,
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option $ARG0$ -a",
					},
					allowArguments: false,
				},
				Args: []string{"check_users", "argument"},
			},
			Want: Want{
				Command: []string{},
				Err:     fmt.Errorf("impossible to create the command custom arguments are not allowed"),
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option $ARG0$ -a",
					},
					allowArguments: true,
				},
				Args: []string{"check_users", "argument0", "argument1"},
			},
			Want: Want{
				Command: []string{},
				Err:     fmt.Errorf("wrong number of arguments for check_users command : 2 given, 1 needed"),
			},
		},
		{
			Entries: Entries{
				Responder: Responder{
					discovery:   nil,
					customCheck: nil,
					nrpeCommands: map[string]string{
						"check_users": "command --option $ARG0$ -a $ARG1$",
					},
					allowArguments: true,
				},
				Args: []string{"check_users", "argument0"},
			},
			Want: Want{
				Command: []string{},
				Err:     fmt.Errorf("wrong number of arguments for check_users command : 1 given, 2 needed"),
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
				Command: []string{},
				Err:     fmt.Errorf("wrong number of arguments for check_users command : 1 given, 0 needed"),
			},
		},
	}

	for i, c := range cases {
		commandResult, errResult := c.Entries.Responder.returnCommand(c.Entries.Args)
		if !reflect.DeepEqual(commandResult, c.Want.Command) {
			t.Errorf("%v r.retunrCommand(%v) == '%v', want '%v'", i, c.Entries.Args, commandResult, c.Want.Command)
		}
		if !reflect.DeepEqual(errResult, c.Want.Err) {
			t.Errorf("%v r.returnCommand(%v) == '%v', want '%v'", i, c.Entries.Args, errResult, c.Want.Err)
		}
	}
}
