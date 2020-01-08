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
	"time"

	"github.com/google/shlex"
)

// Responder is used to build the NRPE answer
type Responder struct {
	discovery      *discovery.Discovery
	customCheck    map[string]discovery.NameContainer
	nrpeCommands   map[string]string
	allowArguments bool
}

// CommandArguments is an enumeration of (undefined, allowed, notAllowed).
type CommandArguments uint8

// Possible values for the allowArguments enum.
const (
	undefined CommandArguments = iota
	allowed
	notAllowed
)

// NewResponse returns a Response
func NewResponse(servicesOverride []map[string]string, d *discovery.Discovery, nrpeConfPath []string) Responder {
	customChecks := make(map[string]discovery.NameContainer)
	for _, fragment := range servicesOverride {
		customChecks[fragment["nagios_nrpe_name"]] = discovery.NameContainer{
			Name:          fragment["id"],
			ContainerName: fragment["instance"],
		}
	}
	nrpeCommands, allowArguments := readNRPEConf(nrpeConfPath)
	return Responder{
		discovery:      d,
		customCheck:    customChecks,
		nrpeCommands:   nrpeCommands,
		allowArguments: allowArguments,
	}
}

// Response return the response of an NRPE request
func (r Responder) Response(ctx context.Context, request string) (string, int16, error) {
	requestArgs := strings.Split(request, "!")
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

func (r Responder) responseCustomCheck(ctx context.Context, request string) (string, int16, error) {
	nameContainer := r.customCheck[request]

	checkNow, err := r.discovery.GetCheckNow(nameContainer)
	if err != nil {
		return "", 0, fmt.Errorf("NRPE: Command '%s' exists but hasn't an associated check", request)
	}

	statusDescription := checkNow(ctx)
	return statusDescription.StatusDescription, int16(statusDescription.CurrentStatus.NagiosCode()), nil
}

func (r Responder) responseNRPEConf(ctx context.Context, requestArgs []string) (string, int16, error) {
	nrpeCommand, err := r.returnCommand(requestArgs)
	if err != nil {
		return "", 0, fmt.Errorf("impossible to create the NRPE command : %s", err)
	}

	if len(nrpeCommand) == 0 {
		return "", 0, fmt.Errorf("the nrpe command is empty")
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nrpeCommand[0], nrpeCommand[1:]...)
	out, err := cmd.CombinedOutput()
	nagiosCode := 0
	if exitError, ok := err.(*exec.ExitError); ok {
		nagiosCode = exitError.ExitCode()
	} else if err != nil {
		logger.V(1).Printf("NRPE command %s failed : %s", nrpeCommand, err)
		return "", 0, fmt.Errorf("NRPE: Unable to read output")
	}

	output := string(out)
	output = strings.TrimSuffix(output, "\n")
	return output, int16(nagiosCode), nil
}

func (r Responder) returnCommand(requestArgs []string) ([]string, error) {
	nrpeCommand := r.nrpeCommands[requestArgs[0]]

	argPatern := "\\$ARG([0-9])+\\$"
	regex, err := regexp.Compile(argPatern)
	if err != nil {
		logger.V(2).Printf("regex: impossible to compile as regex: %s", argPatern)
		return make([]string, 0), err
	}

	argsToReplace := regex.FindAllString(nrpeCommand, -1)

	for i, arg := range argsToReplace {
		if len(requestArgs) > i+1 && r.allowArguments {
			nrpeCommand = strings.Replace(nrpeCommand, arg, requestArgs[i+1], 1)
		} else {
			nrpeCommand = strings.Replace(nrpeCommand, arg, "", 1)
		}
	}
	return shlex.Split(nrpeCommand)
}

// readNRPEConf reads all the conf files of nrpeConfPath and returns a map which contains all the commands
// and a boolean to allow or not the arguments in NRPE requests
func readNRPEConf(nrpeConfPath []string) (map[string]string, bool) {
	nrpeConfMap := make(map[string]string)
	if nrpeConfPath == nil {
		return nrpeConfMap, false
	}

	allowArguments := undefined
	for _, nrpeConfFile := range nrpeConfPath {
		confBytes, err := ioutil.ReadFile(nrpeConfFile)
		if err != nil {
			logger.V(1).Printf("Impossible to read '%s' : %s", nrpeConfFile, err)
			continue
		}
		nrpeConfMap, allowArguments = readNRPEConfFile(confBytes, nrpeConfMap, allowArguments)
	}

	if allowArguments == allowed {
		return nrpeConfMap, true
	}
	return nrpeConfMap, false
}

// readNRPEConfFile read confBytes and returns an updated version of nrpeConfMap and allowArgument
func readNRPEConfFile(confBytes []byte, nrpeConfMap map[string]string, allowArguments CommandArguments) (map[string]string, CommandArguments) {
	commandLinePatern := "^command\\[(.+)\\]=.*$"
	commandLineRegex, err := regexp.Compile(commandLinePatern)
	if err != nil {
		logger.V(2).Printf("Regex: impossible to compile as regex: %s", commandLinePatern)
		return nrpeConfMap, allowArguments
	}

	allowArgumentPatern := "^dont_blame_nrpe=( *)[0-1]$"
	allowArgumentRegex, err := regexp.Compile(allowArgumentPatern)
	if err != nil {
		logger.V(2).Printf("Regex: impossible to compile as regex: %s", allowArgumentPatern)
		return nrpeConfMap, allowArguments
	}

	confCommandArguments := undefined
	confString := string(confBytes)
	confLines := strings.Split(confString, "\n")
	for _, line := range confLines {
		matched := commandLineRegex.MatchString(line)
		if matched {
			splitLine := strings.SplitN(line, "=", 2)
			command := splitLine[1]
			command = strings.TrimRight(command, " ")
			commandName := strings.Split(strings.Split(splitLine[0], "[")[1], "]")[0]
			nrpeConfMap[commandName] = command
			continue
		}
		matched = allowArgumentRegex.MatchString(line)
		if matched {
			splitLine := strings.TrimLeft(strings.Split(line, "=")[1], " ")
			switch splitLine {
			case "0":
				confCommandArguments = notAllowed
			case "1":
				confCommandArguments = allowed
			}
		}
	}
	return nrpeConfMap, confCommandArguments
}
