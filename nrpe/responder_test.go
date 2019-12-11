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
	"reflect"
	"testing"
)

func TestReadNRPEConfFile(t *testing.T) {
	var nrpeConf = `
# NRPE Commands
command[check_users]=/usr/local/nagios/libexec/check_users -w 5 -c 10
command[check_load]=/usr/local/nagios/libexec/check_load -r -w .15,.10,.05 -c .30,.25,.20
command[check_hda1]=/usr/local/nagios/libexec/check_disk -w 20% -c 10% -p /dev/hda1
command[check_zombie_procs]=/usr/local/nagios/libexec/check_procs -w 5 -c 10 -s Z
command[check_total_procs]=/usr/local/nagios/libexec/check_procs -w 150 -c 200
# Other parameters
pid_file=/var/run/nagios/nrpe.pid
include=/etc/nagios/nrpe_local.cfg
ssl_logging=0x00
connection_timeout=300
`
	expectedResult := map[string]string{
		"check_users":        "/usr/local/nagios/libexec/check_users -w 5 -c 10",
		"check_load":         "/usr/local/nagios/libexec/check_load -r -w .15,.10,.05 -c .30,.25,.20",
		"check_hda1":         "/usr/local/nagios/libexec/check_disk -w 20% -c 10% -p /dev/hda1",
		"check_zombie_procs": "/usr/local/nagios/libexec/check_procs -w 5 -c 10 -s Z",
		"check_total_procs":  "/usr/local/nagios/libexec/check_procs -w 150 -c 200",
	}
	nrpeConfByte := []byte(nrpeConf)
	readNRPEConfFileResult := readNRPEConfFile(nrpeConfByte, make(map[string]string))
	if !reflect.DeepEqual(readNRPEConfFileResult, expectedResult) {
		t.Errorf("readNRPEConfFile(configurationFile) == %v, want %v", readNRPEConfFileResult, expectedResult)
	}
}
