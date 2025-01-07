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

package facts

import (
	"context"

	"github.com/shirou/gopsutil/v3/load"
)

const dmiDir = "/sys/devices/virtual/dmi/id/"

func (f *FactProvider) platformFacts(context.Context) map[string]string {
	return nil
}

// primaryAddresses returns the primary IPv4
//
// This should be the IP address that this server use to communicate
// on internet. It may be the private IP if the box is NATed.
func (f *FactProvider) primaryAddress(context.Context) (ipAddress string, macAddress string) {
	return "", ""
}

func getCPULoads() ([]float64, error) {
	loads, err := load.Avg()
	if err != nil {
		return nil, err
	}

	return []float64{loads.Load1, loads.Load5, loads.Load15}, nil
}
