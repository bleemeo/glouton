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
