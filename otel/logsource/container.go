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

package logsource

import (
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/parser/container"
)

// BuildContainerEnvelopeOperator unwraps a Docker-JSON/CRI container log
// envelope so `body` becomes the actual log message. Must run first, before
// any other operator.
func BuildContainerEnvelopeOperator() operator.Config {
	containerCfg := container.NewConfig()
	containerCfg.AddMetadataFromFilePath = false

	return operator.Config{Builder: containerCfg}
}
