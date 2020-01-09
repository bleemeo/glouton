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
	"strconv"
	"strings"
	"time"

	"github.com/google/shlex"
)

type checkRegistry interface {
	GetCheckNow(discovery.NameContainer) (discovery.CheckNow, error)
}

// Responder is used to build the NRPE answer
type Responder struct {
	discovery      checkRegistry
	customCheck    map[string]discovery.NameContainer
	nrpeCommands   map[string]string
	allowArguments bool
}

// NewResponse returns a Response
func NewResponse(servicesOverride []map[string]string, checkRegistry checkRegistry, nrpeConfPath []string) Responder {
	customChecks := make(map[string]discovery.NameContainer)
	for _, fragment := range servicesOverride {
		nagiosNRPEName, ok := fragment["nagios_nrpe_name"]
		if ok {
			customChecks[nagiosNRPEName] = discovery.NameContainer{
				Name:          fragment["id"],
				ContainerName: fragment["instance"],
			}
		}
	}
	nrpeCommands, allowArguments := readNRPEConf(nrpeConfPath)
	return Responder{
		discovery:      checkRegistry,
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
	return "", 0, fmt.Errorf("NRPE: Command '%s' not defined", requestArgs[0])
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
		logger.V(1).Printf("Impossible to create the NRPE command : %s", err)
		return "", 0, fmt.Errorf("NRPE: Unable to read output")
	}

	if len(nrpeCommand) == 0 {
		return "", 0, fmt.Errorf("NRPE: config file contains an empty command")
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
	regex := regexp.MustCompile(argPatern)

	argsToReplace := regex.FindAllString(nrpeCommand, -1)

	for _, arg := range argsToReplace {
		argNumber := strings.TrimRight(strings.TrimLeft(arg, "$ARG"), "$")
		argInt, _ := strconv.Atoi(argNumber)

		if len(requestArgs) > argInt && r.allowArguments {
			nrpeCommand = strings.ReplaceAll(nrpeCommand, arg, requestArgs[argInt])
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

	allowArguments := false
	for _, nrpeConfFile := range nrpeConfPath {
		confBytes, err := ioutil.ReadFile(nrpeConfFile)
		if err != nil {
			logger.V(1).Printf("Impossible to read '%s' : %s", nrpeConfFile, err)
			continue
		}
		nrpeConfMap, allowArguments = readNRPEConfFile(confBytes, nrpeConfMap)
	}

	if allowArguments {
		return nrpeConfMap, true
	}
	return nrpeConfMap, false
}

// readNRPEConfFile read confBytes and returns an updated version of nrpeConfMap and allowArgument
func readNRPEConfFile(confBytes []byte, nrpeConfMap map[string]string) (map[string]string, bool) {
	commandLinePatern := "^command\\[(.+)\\]( *)=.*$"
	commandLineRegex := regexp.MustCompile(commandLinePatern)

	allowArgumentPatern := "^dont_blame_nrpe=( *)[0-1]$"
	allowArgumentRegex := regexp.MustCompile(allowArgumentPatern)

	confCommandArguments := false
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
			if command == "" {
				logger.V(0).Printf("WARNING: NRPE configuration file contains an empty command for %s", commandName)
			}
			continue
		}
		matched = allowArgumentRegex.MatchString(line)
		if matched {
			splitLine := strings.TrimLeft(strings.Split(line, "=")[1], " ")
			switch splitLine {
			case "0":
				confCommandArguments = false
			case "1":
				confCommandArguments = true
			}
		}
	}
	return nrpeConfMap, confCommandArguments
}