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

import "slices"

// ServiceVariant names which implementation of a service type is running, for the few
// service types that have more than one and where the difference changes what Glouton has
// to do: which port to reach it on, which input to build, which check to run.
// Most services will not have variants.
type ServiceVariant string

const (
	// VariantUnknown is the zero value: either the service type has no variants at all,
	// or it has them and nothing has said which one this is -- a user-declared service
	// that named no variant, since auto-discovery always fills one for the types that
	// have them. Callers fall back to the service type's plain default.
	VariantUnknown ServiceVariant = ""

	// VariantInfluxd covers InfluxDB 1.x and 2.x together, and VariantInfluxDB3 is 3.x.
	VariantInfluxd   ServiceVariant = "influxd"
	VariantInfluxDB3 ServiceVariant = "influxdb3"
)

// variantsByService lists the variants each service type accepts, for validating what a
// user wrote in a service override. A type absent from this map takes no variant.
//
//nolint:gochecknoglobals
var variantsByService = map[ServiceName][]ServiceVariant{
	InfluxDBService: {VariantInfluxd, VariantInfluxDB3},
}

// knownVariants maps a process name to the variant it identifies, keyed the same way as
// knownProcesses -- the base name of the command line's first word, which is what
// serviceByCommand matches on.
//
// Auto-discovery fills the variant from this alone, never from the resolved executable
// path: /proc/<pid>/exe briefly fails to resolve right after a container restart, and a
// port that depended on it would flip to the other variant's default for that window.
// The command line is already what classified the process as this service type.
//
//nolint:gochecknoglobals
var knownVariants = map[string]ServiceVariant{
	"influxd":   VariantInfluxd,
	"influxdb3": VariantInfluxDB3,
}

// IsValidFor reports whether this variant is one the service type accepts. The unknown
// variant is valid everywhere: it is what a service that named none carries.
func (v ServiceVariant) IsValidFor(serviceType ServiceName) bool {
	if v == VariantUnknown {
		return true
	}

	return slices.Contains(variantsByService[serviceType], v)
}
