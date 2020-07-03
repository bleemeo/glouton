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

package common

import (
	"glouton/types"
	"strings"
)

// AllowMetric return True if current configuration allow this metrics.
func AllowMetric(labels map[string]string, annotations types.MetricAnnotations, whitelist map[string]bool) bool {
	if len(whitelist) == 0 {
		return true
	}

	if annotations.ServiceName != "" && strings.HasSuffix(labels[types.LabelName], "_status") {
		return true
	}

	return whitelist[labels[types.LabelName]]
}
