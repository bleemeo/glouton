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
	"context"
	"fmt"
	"glouton/discovery"
	"glouton/logger"
	"io/ioutil"
	"os/exec"
	"regexp"
	"strings"
)

// Response is used to build the NRPE answer
type Response struct {
	discovery    *discovery.Discovery
	customCheck  map[string]discovery.NameContainer
	nrpeCommands map[string]string
}

// NewResponse returns a Response
func NewResponse(servicesOverride []map[string]string, d *discovery.Discovery) Response {
	customChecks := make(map[string]discovery.NameContainer)
	for _, fragment := range servicesOverride {
		customChecks[fragment["nagios_nrpe_name"]] = discovery.NameContainer{
			Name:          fragment["id"],
			ContainerName: fragment["instance"],
		}
	}
	nrpeCommands := readNRPEConf()
	return Response{
		discovery:    d,
		customCheck:  customChecks,
		nrpeCommands: nrpeCommands,
	}
}

// Response return the response of an NRPE request
func (r Response) Response(ctx context.Context, request string) (string, int16, error) {
	requestArgs := strings.Split(request, " ")
	_, ok := r.customCheck[requestArgs[0]]
	if ok {
		return r.responseCustomCheck(ctx, requestArgs[0])
	}
	_, ok = r.nrpeCommands[requestArgs[0]]
	if ok {
		return r.responseNRPEConf(ctx, requestArgs)
	}
	return "", 0, fmt.Errorf("NRPE: Command '%s' not defined", request)
}

func (r Response) responseCustomCheck(ctx context.Context, request string) (string, int16, error) {
	nameContainer, _ := r.customCheck[request]

	checkNow, err := r.discovery.GetCheckNow(nameContainer)
	if err != nil {
		return "", 0, fmt.Errorf("NRPE: Command '%s' exists but hasn't an associated check", request)
	}

	statusDescription := checkNow(ctx)
	return statusDescription.StatusDescription, int16(statusDescription.CurrentStatus.NagiosCode()), nil
}

func (r Response) responseNRPEConf(ctx context.Context, requestArgs []string) (string, int16, error) {
	nrpeCommand, _ := r.nrpeCommands[requestArgs[0]]
	nrpeCommandArgs := strings.Split(nrpeCommand, " ")
	argPatern := "\\$ARG([0-9])+\\$"
	nbArgs := 0
	for i, arg := range nrpeCommandArgs {
		match, _ := regexp.MatchString(argPatern, arg)
		if match {
			nbArgs++
			if len(requestArgs) > nbArgs {
				nrpeCommandArgs[i] = requestArgs[nbArgs]
			}
		}
	}
	if len(requestArgs) != nbArgs {
		return "", 0, fmt.Errorf("Wrong number of arguments for %s command : %v given, %v needed", requestArgs[0], len(requestArgs), nbArgs)
	}

	nrpeCommand = unSplit(nrpeCommandArgs)

	out, err := exec.Command(nrpeCommand).Output()
	if err != nil {
		return "", 2, fmt.Errorf("NRPE command %s failed : %s", nrpeCommand, err)
	}

	output := string(out[:])
	return output, 0, nil
}

func unSplit(sliceString []string) string {
	finalString := ""
	for _, s := range sliceString {
		if finalString == "" {
			finalString = finalString + s
		} else {
			finalString = finalString + " " + s
		}
	}
	return finalString
}

func readNRPEConf() map[string]string {
	confBytes, err := ioutil.ReadFile("/etc/nagios/nrpe.cfg")
	if err != nil {
		logger.V(1).Printf("Impossible to read '/etc/nagios/nrpe.cfg' : %s", err)
	}
	confString := string(confBytes)
	confLines := strings.Split(confString, "\n")
	patern := "^command\\[(([a-z]|[A-Z]|[0-9]|[_])+)\\]="
	nrpeConfMap := make(map[string]string)
	for _, line := range confLines {
		matched, err := regexp.MatchString(patern, line)
		if err != nil {
			logger.V(2).Printf("NRPE conf, MatchString failed for: %s", line)
			continue
		}
		if matched {
			splitLine := strings.Split(line, "=")
			command := splitLine[1]
			commandName := strings.Split(strings.Split(splitLine[0], "[")[1], "]")[0]
			nrpeConfMap[commandName] = command
		}
	}
	return nrpeConfMap
}
