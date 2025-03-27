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

//go:build windows

package agent

import (
	"os"

	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/facts/container-runtime/veth"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/exporter/windows"

	"github.com/yusufpapurcu/wmi"
	"golang.org/x/sys/windows/svc"
)

const serviceName string = "glouton"

type winService struct {
	stop chan<- os.Signal
}

// Execute implements svc.Handler.
func (ws winService) Execute(
	args []string,
	req <-chan svc.ChangeRequest,
	status chan<- svc.Status,
) (svcSpecificEC bool, exitCode uint32) {
	_ = args

	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	status <- svc.Status{State: svc.StartPending}
	status <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
loop:
	for c := range req {
		switch c.Cmd { //nolint:exhaustive
		case svc.Interrogate:
			status <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			ws.stop <- os.Interrupt

			break loop
		default:
			logger.V(1).Printf("unexpected control request #%d", c)
		}
	}
	status <- svc.Status{State: svc.StopPending}

	return
}

func initOSSpecificParts(stop chan<- os.Signal) {
	// IsAnInteractiveSession is deprecated but its replacement (IsWindowsService)
	// does not works and fail with an access denied error.
	isInteractive, err := svc.IsAnInteractiveSession() //nolint:staticcheck
	if err != nil {
		logger.V(0).Println(err)
		os.Exit(1)
	}

	if !isInteractive {
		go func() {
			defer crashreport.ProcessPanic()

			err = svc.Run(serviceName, winService{stop})
			if err != nil {
				logger.V(0).Printf("Failed to start the windows service: %v", err)
			}
		}()
	}

	// mirror code in https://github.com/prometheus-community/windows_exporter/blob/f1384759cb04f8a66cd0954e2eff4bca81b2caa4/exporter.go#L256
	// (cf. https://github.com/prometheus-community/windows_exporter/issues/77)
	s, err := wmi.InitializeSWbemServices(wmi.DefaultClient)
	if err != nil {
		logger.V(1).Printf("WMI error, windows specific services may not start: %v", err)

		return
	}

	wmi.DefaultClient.AllowMissingFields = true
	wmi.DefaultClient.SWbemServicesClient = s
}

func (a *agent) registerOSSpecificComponents(*veth.Provider) {
	if a.config.Agent.WindowsExporter.Enable {
		conf, err := a.buildCollectorsConfig()
		if err != nil {
			logger.V(0).Printf("Couldn't build configuration for windows_exporter: %v", err)

			return
		}

		collectors := a.config.Agent.WindowsExporter.Collectors
		if err := windows.AddWindowsExporter(a.gathererRegistry, collectors, conf); err != nil {
			logger.Printf("Unable to start windows_exporter, system metrics will be missing: %v", err)
		}
	}
}

func getResidentMemoryOfSelf() uint64 {
	return 0
}
