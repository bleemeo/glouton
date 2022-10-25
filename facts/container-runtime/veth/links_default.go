// Copyright 2015-2022 Bleemeo
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

//go:build windows || darwin

package veth

import (
	"net"
	"strings"
)

// linkList returns the links using the telegraf input "net".
func linkList() ([]link, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	links := make([]link, len(interfaces))
	for i, iface := range interfaces {
		links[i] = link{
			name:      iface.Name,
			index:     iface.Index,
			hasNSPeer: strings.HasPrefix(iface.Name, "veth"),
		}
	}

	return links, nil
}
