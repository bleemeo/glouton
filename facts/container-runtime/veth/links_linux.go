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

//go:build linux

package veth

import "github.com/vishvananda/netlink"

// linkList returns the links using netlink.
func linkList() ([]link, error) {
	interfaces, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}

	links := make([]link, len(interfaces))

	for i, iface := range interfaces {
		// NetNsID identifies the namespace holding the link, it is only set when the
		// interface is associated with another network namespace (-1 is the default value).
		hasNSPeer := iface.Attrs().NetNsID >= 0

		// Don't ignore minikube interface eth0.
		if hasNSPeer && iface.Attrs().Name == "eth0" {
			hasNSPeer = false
		}

		links[i] = link{
			name:      iface.Attrs().Name,
			index:     iface.Attrs().Index,
			hasNSPeer: hasNSPeer,
		}
	}

	return links, nil
}
