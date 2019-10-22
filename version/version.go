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

package version

import "fmt"

//nolint:gochecknoglobals
var (
	// BuildHash is the git hash of the build. (local change ignored)
	BuildHash = "unset"

	// Version is the agent version
	Version = "0.1"
)

// UserAgent returns the User-Agent for request performed by the agent
func UserAgent() string {
	return fmt.Sprintf("Glouton %s", Version)
}
