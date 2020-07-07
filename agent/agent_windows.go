// +build windows

package agent

import (
	"glouton/logger"

	"github.com/StackExchange/wmi"
)

func (a *agent) registerOSSpecificComponents() {
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
