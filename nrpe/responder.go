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

// Responder is used to build the NRPE answer
type Responder struct {
	discovery    *discovery.Discovery
	customCheck  map[string]discovery.NameContainer
	nrpeCommands map[string]string
}

// NewResponse returns a Response
func NewResponse(servicesOverride []map[string]string, d *discovery.Discovery, nrpeConfPath []string) Responder {
	customChecks := make(map[string]discovery.NameContainer)
	for _, fragment := range servicesOverride {
		customChecks[fragment["nagios_nrpe_name"]] = discovery.NameContainer{
			Name:          fragment["id"],
			ContainerName: fragment["instance"],
		}
	}
	nrpeCommands := readNRPEConf(nrpeConfPath)
	return Responder{
		discovery:    d,
		customCheck:  customChecks,
		nrpeCommands: nrpeCommands,
	}
}

// Response return the response of an NRPE request
func (r Responder) Response(ctx context.Context, request string) (string, int16, error) {
	requestArgs := strings.Split(request, " ")
	_, ok := r.customCheck[requestArgs[0]]
	if ok {
		return r.responseCustomCheck(ctx, requestArgs[0])
	}
	_, ok = r.nrpeCommands[requestArgs[0]]
	if ok {
		return r.responseNRPEConf(requestArgs)
	}
	return "", 0, fmt.Errorf("NRPE: Command '%s' not defined", request)
}

func (r Responder) responseCustomCheck(ctx context.Context, request string) (string, int16, error) {
	nameContainer := r.customCheck[request]

	checkNow, err := r.discovery.GetCheckNow(nameContainer)
	if err != nil {
		return "", 0, fmt.Errorf("NRPE: Command '%s' exists but hasn't an associated check", request)
	}

	statusDescription := checkNow(ctx)
	return statusDescription.StatusDescription, int16(statusDescription.CurrentStatus.NagiosCode()), nil
}

func (r Responder) responseNRPEConf(requestArgs []string) (string, int16, error) {
	nrpeCommand := r.nrpeCommands[requestArgs[0]]
	nrpeCommandArgs := strings.Split(nrpeCommand, " ")
	argPatern := "\\$ARG([0-9])+\\$"
	regex, _ := regexp.Compile(argPatern)
	nbArgs := 0
	for i, arg := range nrpeCommandArgs {
		match := regex.MatchString(arg)
		if match {
			nbArgs++
			if len(requestArgs) > nbArgs {
				nrpeCommandArgs[i] = requestArgs[nbArgs]
			}
		}
	}
	if len(requestArgs) != nbArgs {
		return "", 0, fmt.Errorf("wrong number of arguments for %s command : %v given, %v needed", requestArgs[0], len(requestArgs), nbArgs)
	}

	out, err := exec.Command(nrpeCommand).Output()
	if err != nil {
		return "", 2, fmt.Errorf("NRPE command %s failed : %s", nrpeCommand, err)
	}

	output := string(out)
	return output, 0, nil
}

func readNRPEConf(nrpeConfPath []string) map[string]string {
	nrpeConfMap := make(map[string]string)
	if nrpeConfPath == nil {
		return nrpeConfMap
	}
	commandLinePatern := "^command\\[(([a-z]|[A-Z]|[0-9]|[_])+)\\]=.*$"
	commandLineRegex, err := regexp.Compile(commandLinePatern)
	if err != nil {
		logger.V(2).Printf("Regex: impossible to compile as regex: %s", commandLinePatern)
		return nrpeConfMap
	}
	for _, nrpeConfFile := range nrpeConfPath {
		confBytes, err := ioutil.ReadFile(nrpeConfFile)
		if err != nil {
			logger.V(1).Printf("Impossible to read '%s' : %s", nrpeConfFile, err)
			continue
		}
		confString := string(confBytes)
		confLines := strings.Split(confString, "\n")
		for _, line := range confLines {
			matched := commandLineRegex.MatchString(line)
			if matched {
				splitLine := strings.Split(line, "=")
				command := splitLine[1]
				commandName := strings.Split(strings.Split(splitLine[0], "[")[1], "]")[0]
				nrpeConfMap[commandName] = command
			}
		}
	}
	return nrpeConfMap
}
