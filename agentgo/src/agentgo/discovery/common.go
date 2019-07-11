package discovery

import (
	"context"
	"net"
	"time"
)

// Discoverer allow to discover services. See DynamicDiscovery and Discovery
type Discoverer interface {
	Discovery(ctx context.Context, maxAge time.Duration) (services []Service, err error)
}

type nameContainer struct {
	name        string
	containerID string
}

// Service is the information found about a given service
type Service struct {
	Name            string
	ContainerID     string
	ContainerName   string
	IPAddress       string // IPAddress is the IPv4 address to reach service for metrics gathering. If empty, it means IP was not found
	ListenAddresses []net.Addr
	ExePath         string
	Active          bool

	hasNetstatInfo bool
}

// nolint:gochecknoglobals
var (
	servicesDiscoveryInfo = map[string]discoveryInfo{
		"apache": {
			ServicePort:     80,
			ServiceProtocol: "tcp",
		},
		"haproxy": {
			IgnoreHighPort: true, // HAProxy use a random high-port when Syslog over-UDP is enabled.
		},
		"memcached": {
			ServicePort:     11211,
			ServiceProtocol: "tcp",
		},
		"redis": {
			ServicePort:     6379,
			ServiceProtocol: "tcp",
		},
	}
)

type discoveryInfo struct {
	ServicePort     int
	ServiceProtocol string // "tcp", "udp" or "unix"
	IgnoreHighPort  bool
}
