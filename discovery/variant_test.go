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

package discovery

import (
	"strings"
	"testing"

	"github.com/bleemeo/glouton/config"
)

func TestServiceVariantIsValidFor(t *testing.T) {
	cases := []struct {
		name        string
		variant     ServiceVariant
		serviceType ServiceName
		want        bool
	}{
		{"chrony on ntp", VariantChrony, NTPService, true},
		{"ntpd on ntp", VariantNTPd, NTPService, true},
		{"influxd on influxdb", VariantInfluxd, InfluxDBService, true},
		{"influxdb3 on influxdb", VariantInfluxDB3, InfluxDBService, true},
		// The empty variant is what a service that named none carries, so it has to be
		// valid everywhere -- including on the types that take no variant at all.
		{"unknown on ntp", VariantUnknown, NTPService, true},
		{"unknown on apache", VariantUnknown, ApacheService, true},
		// A variant belonging to another service type is as wrong as a typo.
		{"chrony on influxdb", VariantChrony, InfluxDBService, false},
		{"influxd on ntp", VariantInfluxd, NTPService, false},
		// Apache has one implementation, so naming any variant for it is meaningless.
		{"chrony on apache", VariantChrony, ApacheService, false},
		{"typo", "chronyd", NTPService, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.variant.IsValidFor(tc.serviceType); got != tc.want {
				t.Errorf("%q.IsValidFor(%q) = %v, want %v", tc.variant, tc.serviceType, got, tc.want)
			}
		})
	}
}

// TestValidateServicesVariant covers what a user wrote in a service override. A variant
// that isn't one of the type's own must warn rather than pass through: taken as-is it
// would be indistinguishable from naming none, and silently fall back to the type's
// default port -- 8181 for an "influxdb" the user meant to be a 1.x on 8086.
func TestValidateServicesVariant(t *testing.T) {
	cases := []struct {
		name        string
		service     config.Service
		wantWarning bool
		wantVariant string
	}{
		{
			name:        "valid variant is kept",
			service:     config.Service{Type: "influxdb", Variant: "influxd"}, //nolint:exhaustruct
			wantWarning: false,
			wantVariant: "influxd",
		},
		{
			name:        "no variant is fine",
			service:     config.Service{Type: "influxdb"}, //nolint:exhaustruct
			wantWarning: false,
			wantVariant: "",
		},
		{
			name:        "unknown variant warns and is dropped",
			service:     config.Service{Type: "influxdb", Variant: "influxdb2"}, //nolint:exhaustruct
			wantWarning: true,
			wantVariant: "",
		},
		{
			// A variant on a type that has none at all gets its own message: there is
			// nothing to suggest instead.
			name:        "variant on a type without variants warns",
			service:     config.Service{Type: "apache", Variant: "influxd"}, //nolint:exhaustruct
			wantWarning: true,
			wantVariant: "",
		},
		{
			name:        "chrony on ntp is kept",
			service:     config.Service{Type: "ntp", Variant: "chrony"}, //nolint:exhaustruct
			wantWarning: false,
			wantVariant: "chrony",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings := validateServices([]config.Service{tc.service}, config.OpenTelemetry{}) //nolint:exhaustruct

			if tc.wantWarning && len(warnings) == 0 {
				t.Error("validateServices() gave no warning, want one")
			}

			if !tc.wantWarning && len(warnings) != 0 {
				t.Errorf("validateServices() warned unexpectedly: %s", warnings)
			}

			key := NameInstance{Name: tc.service.Type, Instance: tc.service.Instance}

			srv, ok := got[key]
			if !ok {
				t.Fatalf("validateServices() dropped the service %v", key)
			}

			if srv.Variant != tc.wantVariant {
				t.Errorf("validateServices() variant = %q, want %q", srv.Variant, tc.wantVariant)
			}
		})
	}
}

// TestValidateServicesVariantWarningNamesAlternatives checks the message is actionable:
// a user who mistyped a variant needs to be told which ones exist.
func TestValidateServicesVariantWarningNamesAlternatives(t *testing.T) {
	_, warnings := validateServices(
		[]config.Service{{Type: "influxdb", Variant: "influxdb2"}}, //nolint:exhaustruct
		config.OpenTelemetry{}, //nolint:exhaustruct
	)

	if len(warnings) == 0 {
		t.Fatal("validateServices() gave no warning for an unknown variant")
	}

	message := warnings.MaybeUnwrap().Error()
	for _, want := range []string{"influxd", "influxdb3"} {
		if !strings.Contains(message, want) {
			t.Errorf("warning %q does not name the valid variant %q", message, want)
		}
	}
}

// TestApplyOverrideVariant covers the two ways a service ends up with a variant: the one
// auto-discovery found on the process, and the one the user named in an override. An
// override that names none must leave the discovered one alone, so that tuning an
// unrelated field doesn't silently reset the service to its type's default port.
func TestApplyOverrideVariant(t *testing.T) {
	cases := []struct {
		name           string
		discovered     ServiceVariant
		override       config.Service
		wantVariant    ServiceVariant
		wantDefaultPrt int
	}{
		{
			name:           "discovery only",
			discovered:     VariantInfluxd,
			override:       config.Service{Type: "influxdb"}, //nolint:exhaustruct
			wantVariant:    VariantInfluxd,
			wantDefaultPrt: 8086,
		},
		{
			name:           "override wins over discovery",
			discovered:     VariantInfluxd,
			override:       config.Service{Type: "influxdb", Variant: "influxdb3"}, //nolint:exhaustruct
			wantVariant:    VariantInfluxDB3,
			wantDefaultPrt: 8181,
		},
		{
			// Nothing discovered and nothing declared: the type's own default.
			name:           "neither",
			discovered:     VariantUnknown,
			override:       config.Service{Type: "influxdb"}, //nolint:exhaustruct
			wantVariant:    VariantUnknown,
			wantDefaultPrt: 8181,
		},
		{
			name:           "override on a service discovery never saw",
			discovered:     VariantUnknown,
			override:       config.Service{Type: "influxdb", Variant: "influxd"}, //nolint:exhaustruct
			wantVariant:    VariantInfluxd,
			wantDefaultPrt: 8086,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := NameInstance{Name: "influxdb", Instance: ""}
			services := map[NameInstance]Service{
				key: { //nolint:exhaustruct
					Name:           "influxdb",
					ServiceType:    InfluxDBService,
					ServiceVariant: tc.discovered,
					Active:         true,
				},
			}

			overrides, warnings := validateServices([]config.Service{tc.override}, config.OpenTelemetry{}) //nolint:exhaustruct
			if len(warnings) != 0 {
				t.Fatalf("validateServices() warned: %s", warnings)
			}

			got := copyAndMergeServiceWithOverride(services, overrides)
			applyOverrideInPlace(got)

			if got[key].ServiceVariant != tc.wantVariant {
				t.Errorf("ServiceVariant = %q, want %q", got[key].ServiceVariant, tc.wantVariant)
			}

			di := servicesDiscoveryInfo[InfluxDBService]
			if port := got[key].defaultPort(di); port != tc.wantDefaultPrt {
				t.Errorf("defaultPort() = %d, want %d", port, tc.wantDefaultPrt)
			}
		})
	}
}
