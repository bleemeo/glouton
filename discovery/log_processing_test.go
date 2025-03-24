// Copyright 2015-2025 Bleemeo
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
	"path/filepath"
	"testing"

	"github.com/bleemeo/glouton/config"
)

func TestDefaultLogProcessingConfigs(t *testing.T) {
	t.Parallel()

	defaultKnownLogFormats := config.DefaultKnownLogFormats()

	for service, info := range servicesLogInfo {
		for _, fileFormat := range info.FileFormats {
			if _, err := filepath.Match(fileFormat.FilePath, "value"); err != nil {
				t.Errorf("File path %q for service %s is invalid: %v", fileFormat.FilePath, service, err)
			}

			if _, ok := defaultKnownLogFormats[fileFormat.Format]; !ok {
				t.Errorf("Log format %q for service %s on file %s in not referenced in the default known log formats", fileFormat.Format, service, fileFormat.FilePath)
			}
		}

		if _, ok := defaultKnownLogFormats[info.DockerFormat]; !ok {
			t.Errorf("Log format %q for service %s with docker in not referenced in the default known log formats", info.DockerFormat, service)
		}
	}
}
