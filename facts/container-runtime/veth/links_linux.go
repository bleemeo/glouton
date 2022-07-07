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
