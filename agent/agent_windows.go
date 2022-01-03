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

//go:build windows
// +build windows

package agent

import (
	"context"
	"glouton/logger"
	"os"

	"github.com/StackExchange/wmi"
	"golang.org/x/sys/windows/svc"
)

const serviceName string = "glouton"

type winService struct {
	cancelFunc *context.CancelFunc
}

// Execute implements svc.Handler.
func (ws *winService) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (svcSpecificEC bool, exitCode uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	status <- svc.Status{State: svc.StartPending}
	status <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
loop:
	for c := range req {
		switch c.Cmd { //nolint:exhaustive
		case svc.Interrogate:
			status <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			(*ws.cancelFunc)()

			break loop
		default:
			logger.V(1).Printf("unexpected control request #%d", c)
		}
	}
	status <- svc.Status{State: svc.StopPending}

	return
}

func (a *agent) initOSSpecificParts() {
	// IsAnInteractiveSession is deprecated but its remplacement (IsWindowsService)
	// does not works and fail with an access denied error.
	isInteractive, err := svc.IsAnInteractiveSession() //nolint:staticcheck
	if err != nil {
		logger.V(0).Println(err)
		os.Exit(1)
	}

	if !isInteractive {
		go func() {
			err = svc.Run(serviceName, &winService{cancelFunc: &a.cancel})
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

func (a *agent) registerOSSpecificComponents() {
	if a.oldConfig.Bool("agent.windows_exporter.enable") {
		conf, err := a.buildCollectorsConfig()
		if err != nil {
			logger.V(0).Printf("Couldn't build configuration for windows_exporter: %v", err)

			return
		}

		collectors := a.oldConfig.StringList("agent.windows_exporter.collectors")
		if err := a.gathererRegistry.AddWindowsExporter(collectors, conf); err != nil {
			logger.Printf("Unable to start windows_exporter, system metrics will be missing: %v", err)
		}
	}
}
