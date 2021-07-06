// Copyright 2015-2021 Bleemeo
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

package snmp

import (
	"fmt"
)

func MigratedTargets(vMap []interface{}) (cfg interface{}) {
	var migratedTargets []interface{}

	for dict := range vMap {
		tmp := vMap[dict].(map[string]interface{})
		u := fmt.Sprintf("http://localhost:9116/snmp?module=%s&target=%s", tmp["module"], tmp["target"])

		migratedTargets = append(migratedTargets, map[string]interface{}{
			"url":  u,
			"name": tmp["name"],
		})
	}

	return migratedTargets
}

// execute snmp exporter
