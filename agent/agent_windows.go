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
		switch c.Cmd {
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
	// mirror code in https://github.com/prometheus-community/windows_exporter/blob/f1384759cb04f8a66cd0954e2eff4bca81b2caa4/exporter.go#L256
	// (cf. https://github.com/prometheus-community/windows_exporter/issues/77)
	s, err := wmi.InitializeSWbemServices(wmi.DefaultClient)
	if err != nil {
		logger.V(1).Printf("WMI error, windows specific services may not start: %v", err)
		return
	}

	wmi.DefaultClient.AllowMissingFields = true
	wmi.DefaultClient.SWbemServicesClient = s

	isInteractive, err := svc.IsAnInteractiveSession()
	if err != nil {
		logger.V(0).Println(err)
		os.Exit(1)
	}

	// Not interactive ? let's start a windows service
	if !isInteractive {
		go func() {
			err = svc.Run(serviceName, &winService{cancelFunc: &a.cancel})
			if err != nil {
				logger.V(0).Printf("Failed to start the windows service: %v", err)
			}
		}()
	}
}

func (a *agent) registerOSSpecificComponents() {
	if a.config.Bool("agent.windows_exporter.enabled") {
		if err := a.gathererRegistry.AddWindowsExporter(a.config.StringList("agent.windows_exporter.collectors")); err != nil {
			logger.Printf("Unable to start windows_exporter, system metrics will be missing: %v", err)
		}
	}
}
