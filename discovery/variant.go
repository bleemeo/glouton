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
//
// It exists so that the service keeps the name the panel should show. An NTP service
// answered by chronyd is still "ntp" to a user, and an InfluxDB 3 server is still
// "influxdb" -- naming them "chrony" and "influxdb3" instead would have to be translated
// back at every place the name is displayed, and would collide with a user's own service
// override, which is keyed by that name. The variant carries the implementation alongside
// the name rather than inside it.
//
// Most service types have exactly one implementation and leave this empty: there is no
// variant of Apache to distinguish.
type ServiceVariant string

const (
	// VariantUnknown is the zero value: either the service type has no variants at all,
	// or it has them and nothing has said which one this is -- a user-declared service
	// that named no variant, since auto-discovery always fills one for the types that
	// have them. Callers fall back to the service type's plain default.
	VariantUnknown ServiceVariant = ""

	// VariantInfluxd covers InfluxDB 1.x and 2.x together, and VariantInfluxDB3 is 3.x.
	//
	// The split is by binary rather than by major version on purpose, because the binary
	// is all a process can be told apart by: 1.x and 2.x are both "influxd" and their
	// command lines are identical, where 3.x is "influxdb3". That is exactly the
	// granularity discovery needs, since the two lines that share a binary also share
	// port 8086 while 3.x serves 8181.
	//
	// Telling 1.x from 2.x needs the server itself to answer, which only matters for
	// where the metrics are read from, and inputs/influxdb already asks it at gather
	// time -- see detectLine there. Nothing at discovery level needs that answer.
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
