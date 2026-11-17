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

package bleemeoapi

const (
	APITagsLength     = 100
	DiagnosticMaxSize = 5 << 20 // DiagnosticMaxSize = 5MB
)

const (
	// ParamFields is the URL query parameter name for specifying API response fields.
	ParamFields = "fields"

	// ParamAgent is the URL query parameter name for filtering by agent.
	ParamAgent = "agent"
)

// MetricFields is the fields used on the API for a metric, we always use all fields to
// make sure no Metric object is returned with some empty fields which could create bugs.
const MetricFields = "id,label,item,labels_text,unit,unit_text,service,container,deactivated_at," +
	"threshold_low_warning,threshold_low_critical,threshold_high_warning,threshold_high_critical,status_of,agent"
