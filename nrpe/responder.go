// Copyright 2015-2025 Bleemeo
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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/google/shlex"
)

var (
	errContainsEmptyCommand = errors.New("NRPE: config file contains an empty command")
	errUnreadable           = errors.New("NRPE: Unable to read output")
)

type checkRegistry interface {
	GetCheckNow(nameInstance discovery.NameInstance) (discovery.CheckNow, error)
}

// Responder is used to build the NRPE answer.
type Responder struct {
	runner         *gloutonexec.Runner
	discovery      checkRegistry
	customCheck    map[string]discovery.NameInstance
	nrpeCommands   map[string]string
	allowArguments bool
}

// NewResponse returns a Response.
func NewResponse(services []config.Service, checkRegistry checkRegistry, nrpeConfPath []string) Responder {
	customChecks := make(map[string]discovery.NameInstance)

	for _, service := range services {
		if service.NagiosNRPEName == "" {
			continue
		}

		customChecks[service.NagiosNRPEName] = discovery.NameInstance{
			Name:     service.Type,
			Instance: service.Instance,
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

// Response return the response of an NRPE request.
func (r Responder) Response(ctx context.Context, request string) (string, int16, error) {
	requestArgs := strings.Split(request, "!")

	logger.V(2).Printf("Received request for NRPE command %s", requestArgs[0])

	_, ok := r.customCheck[requestArgs[0]]
	if ok {
		return r.responseCustomCheck(ctx, requestArgs[0])
	}

	_, ok = r.nrpeCommands[requestArgs[0]]
	if ok {
		return r.responseNRPEConf(ctx, requestArgs)
	}

	// this error has been disabled as we want to closely match the nrpe server output
	return "", 0, fmt.Errorf("NRPE: Command '%s' not defined", requestArgs[0]) //nolint:err113
}

func (r Responder) responseCustomCheck(ctx context.Context, request string) (string, int16, error) {
	nameContainer := r.customCheck[request]

	checkNow, err := r.discovery.GetCheckNow(nameContainer)
	if err != nil {
		// this error has been disabled as we want to closely match the nrpe server output
		return "", 0, fmt.Errorf("NRPE: Command '%s' exists but does not have an associated check", request) //nolint:err113
	}

	statusDescription := checkNow(ctx)

	return statusDescription.StatusDescription, int16(statusDescription.CurrentStatus.NagiosCode()), nil //nolint:gosec
}

func (r Responder) responseNRPEConf(ctx context.Context, requestArgs []string) (string, int16, error) {
	nrpeCommand, err := r.returnCommand(requestArgs)
	if err != nil {
		logger.V(1).Printf("Impossible to create the NRPE command : %s", err)

		return "", 0, errUnreadable
	}

	if len(nrpeCommand) == 0 {
		return "", 0, errContainsEmptyCommand
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// nrpeCommand[0] is not remote controlled. It come from local configuration files.
	out, err := r.runner.Run(ctx, gloutonexec.Option{CombinedOutput: true}, nrpeCommand[0], nrpeCommand[1:]...)
	nagiosCode := 0

	if exitError, ok := err.(*exec.ExitError); ok {
		nagiosCode = exitError.ExitCode()
	} else if err != nil {
		logger.V(1).Printf("NRPE command %s failed : %s", nrpeCommand, err)

		return "", 0, errUnreadable
	}

	output := string(out)
	output = strings.TrimSuffix(output, "\n")

	return output, int16(nagiosCode), nil //nolint:gosec
}

func (r Responder) returnCommand(requestArgs []string) ([]string, error) {
	nrpeCommand := r.nrpeCommands[requestArgs[0]]

	argPattern := "\\$ARG([0-9])+\\$"
	regex := regexp.MustCompile(argPattern)

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
// and a boolean to allow or not the arguments in NRPE requests.
func readNRPEConf(nrpeConfPath []string) (map[string]string, bool) {
	nrpeConfMap := make(map[string]string)

	if nrpeConfPath == nil {
		return nrpeConfMap, false
	}

	allowArguments := false

	for _, nrpeConfFile := range nrpeConfPath {
		confBytes, err := os.ReadFile(nrpeConfFile)
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

// readNRPEConfFile read confBytes and returns an updated version of nrpeConfMap and allowArgument.
func readNRPEConfFile(confBytes []byte, nrpeConfMap map[string]string) (map[string]string, bool) {
	commandLinePattern := "^command\\[(.+)\\]( *)=.*$"
	commandLineRegex := regexp.MustCompile(commandLinePattern)

	allowArgumentPattern := "^dont_blame_nrpe=( *)[0-1]$"
	allowArgumentRegex := regexp.MustCompile(allowArgumentPattern)

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
