// Copyright 2015-2026 Bleemeo
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

package logprocessing

import (
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/otel/logsource"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// Test that every discovered service with a log config shows up in the diagnostic, including the ones
// processLogSources skips and the ones whose setup failed.
func TestManagerDiagnosticSources(t *testing.T) {
	t.Parallel()

	pipeline := &pipelineContext{}
	man := &Manager{
		config:            config.OpenTelemetry{AutoDiscovery: config.AutoDiscovery{ContainerAndServiceEnable: true}},
		pipeline:          pipeline,
		containerRecv:     newContainerReceiver(pipeline),
		watchedServices:   make(map[discovery.NameInstance]sourceDiagnostic),
		watchedContainers: make(map[string]sourceDiagnostic),
		serviceReceivers:  make(map[discovery.NameInstance][]*logReceiver),
	}

	rejected := svc("rejected", "", "", true, time.Now())
	rejected.LogProcessingError = `unknown log format "nope"`

	services := []discovery.Service{
		rejected,
		svc("stopped", "", "", false, time.Now(), discovery.ServiceLogReceiver{FilePath: "/var/log/stopped.log"}),
		svc("myapp", "", "", true, time.Now(),
			discovery.ServiceLogReceiver{FilePath: "/var/log/myapp/ok.log"},
			discovery.ServiceLogReceiver{FilePath: "/var/log/myapp/broken.log", Format: "fmt"},
		),
	}

	logSources := man.processLogSources(services, nil, nil)
	if len(logSources) != 2 {
		t.Fatalf("Expected 2 log sources for myapp, got %d", len(logSources))
	}

	// Simulates HandleLogsFromDynamicSources failing to set up one of the two files.
	myappKey := discovery.NameInstance{Name: "myapp"}
	diag := man.watchedServices[myappKey]
	diag.SetupErrors = map[string]string{"/var/log/myapp/broken.log": "setup receiver: boom"}
	man.watchedServices[myappKey] = diag

	sources, _ := man.DiagnosticSources()

	want := []logsource.SourceDiagnostic{
		{
			Name:   "auditd",
			Origin: "auditd",
			Status: logsource.SourceExcluded,
			Reason: "log.opentelemetry.auto_discovery.auditd_enable is false",
		},
		{
			Name:   "journald",
			Origin: "journald",
			Status: logsource.SourceExcluded,
			Reason: "log.opentelemetry.auto_discovery.journald_enable is false",
		},
		{
			Name:      "service:myapp",
			Origin:    "service",
			Status:    logsource.SourceFailed,
			Errors:    []string{"setup receiver: boom"},
			Include:   []string{"/var/log/myapp/broken.log"},
			LogFormat: "fmt",
		},
		{
			Name:    "service:myapp",
			Origin:  "service",
			Status:  logsource.SourceWaiting,
			Reason:  "log receiver not set up yet",
			Include: []string{"/var/log/myapp/ok.log"},
		},
		{
			Name:   "service:rejected",
			Origin: "service",
			Status: logsource.SourceFailed,
			Reason: `unknown log format "nope"`,
		},
		{
			Name:   "service:stopped",
			Origin: "service",
			Status: logsource.SourceWaiting,
			Reason: "service is not active",
		},
		{
			Name:   "syslog",
			Origin: "syslog",
			Status: logsource.SourceExcluded,
			Reason: "log.opentelemetry.auto_discovery.syslog_enable is false",
		},
	}

	opts := []cmp.Option{
		cmpopts.IgnoreUnexported(logsource.SourceDiagnostic{}),
		cmpopts.EquateEmpty(),
		cmpopts.SortSlices(func(a, b logsource.SourceDiagnostic) bool {
			if a.Name != b.Name {
				return a.Name < b.Name
			}

			return a.Status < b.Status
		}),
	}

	if diff := cmp.Diff(want, sources, opts...); diff != "" {
		t.Errorf("DiagnosticSources mismatch (-want +got):\n%s", diff)
	}
}

// Test that services with an explicit log config are listed as excluded, and the others aren't listed.
func TestExcludedServiceSources(t *testing.T) {
	t.Parallel()

	withLogFiles := svc("myapp", "", "", true, time.Now())
	withLogFiles.Config.LogFiles = []config.ServiceLogFile{{FilePath: "/var/log/myapp/myapp.log"}}

	withoutLogConfig := svc("apache", "", "", true, time.Now(), discovery.ServiceLogReceiver{FilePath: "/var/log/apache2/access.log"})

	got := ExcludedServiceSources([]discovery.Service{withLogFiles, withoutLogConfig}, "some reason")

	want := []logsource.SourceDiagnostic{
		{
			Name:    "service:myapp",
			Origin:  "service",
			Status:  logsource.SourceExcluded,
			Reason:  "some reason",
			Include: []string{"/var/log/myapp/myapp.log"},
		},
	}

	if diff := cmp.Diff(want, got, cmpopts.IgnoreUnexported(logsource.SourceDiagnostic{})); diff != "" {
		t.Errorf("ExcludedServiceSources mismatch (-want +got):\n%s", diff)
	}
}
