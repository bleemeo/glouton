package facts

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	psutilNet "github.com/shirou/gopsutil/net"
)

// NetstatProvider provide netstat information from both a file (output of netstat command) and using gopsutil
//
// The file is useful since gopsutil will be run with current privilege which are unlikely to be root.
// The file should be the output of netstat run as root.
type NetstatProvider struct {
	filePath string
}

// Netstat return a mapping from PID to listening addresses
//
// Supported addresses network is currently "tcp", "udp" or "unix".
func (np NetstatProvider) Netstat(ctx context.Context) (netstat map[int][]net.Addr, err error) {
	netstatData, err := ioutil.ReadFile(np.filePath)
	if err != nil && !os.IsNotExist(err) {
		return
	}
	netstat = decodeNetstatFile(string(netstatData))
	dynamicNetstat, err := psutilNet.Connections("inet")
	if err == nil {
		for _, c := range dynamicNetstat {
			if c.Pid == 0 {
				continue
			}
			if c.Status != "LISTEN" {
				continue
			}
			address := c.Laddr.IP
			// We currently only with IPv4. Convert IPv6 address to IPv4 one.
			if address == "::" {
				address = "0.0.0.0"
			}
			if address == "::1" {
				address = "127.0.0.1"
			}
			if strings.Contains(address, ":") {
				continue
			}
			protocol := ""
			switch {
			case c.Type == syscall.SOCK_STREAM:
				protocol = "tcp"
			case c.Type == syscall.SOCK_DGRAM:
				protocol = "udp"
			default:
				continue
			}

			netstat[int(c.Pid)] = addAddress(netstat[int(c.Pid)], listenAddress{
				network: protocol,
				address: fmt.Sprintf("%s:%d", address, c.Laddr.Port),
			})
		}
	}
	return netstat, nil
}

//nolint:gochecknoglobals
var (
	netstatRE = regexp.MustCompile(
		`(?P<protocol>udp6?|tcp6?)\s+\d+\s+\d+\s+(?P<address>[0-9a-f.:]+):(?P<port>\d+)\s+[0-9a-f.:*]+\s+(LISTEN)?\s+(?P<pid>\d+)/(?P<program>.*)$`,
	)
	netstatUnixRE = regexp.MustCompile(
		`^(?P<protocol>unix)\s+\d+\s+\[\s+(ACC |W |N )+\s*\]\s+(DGRAM|STREAM)\s+LISTENING\s+(\d+\s+)?(?P<pid>\d+)/(?P<program>.*)\s+(?P<address>.+)$`,
	)
)

type listenAddress struct {
	network string
	address string
}

func (l listenAddress) Network() string {
	return l.network
}
func (l listenAddress) String() string {
	return l.address
}

func decodeNetstatFile(data string) map[int][]net.Addr {
	result := make(map[int][]net.Addr)
	lines := strings.Split(data, "\n")
	for _, line := range lines {
		var protocol, address string
		var pid, port int64
		var err error
		r := netstatRE.FindStringSubmatch(line)
		if r != nil {
			protocol = r[1]
			address = r[2]
			port, err = strconv.ParseInt(r[3], 10, 0)
			if err != nil {
				continue
			}
			pid, err = strconv.ParseInt(r[5], 10, 0)
			if err != nil {
				continue
			}
		} else {
			r = netstatUnixRE.FindStringSubmatch(line)
			if r == nil {
				continue
			}
			protocol = r[1]
			address = r[7]
			pid, err = strconv.ParseInt(r[5], 10, 0)
			if err != nil {
				continue
			}
			port = 0
		}

		// socket with "tcp6" or "udp6" are usually IPv6 + IPv4. We currently only with IPv4.
		// Convert IPv6 address to IPv4 one and "tcp6/udp6" to "tcp/udp"
		if protocol == "tcp6" || protocol == "udp6" {
			if address == "::" {
				address = "0.0.0.0"
			}
			if address == "::1" {
				address = "127.0.0.1"
			}
			if strings.Contains(address, ":") {
				// It's still an IPv6 address, we don't know how to convert it to IPv4
				continue
			}
			protocol = protocol[:3]
		}
		if protocol == "tcp" || protocol == "udp" {
			address = fmt.Sprintf("%s:%d", address, port)
		}
		addresses := result[int(pid)]
		if addresses == nil {
			addresses = make([]net.Addr, 0)
		}
		result[int(pid)] = addAddress(addresses, listenAddress{
			network: protocol,
			address: address,
		})
	}
	return result
}

func addAddress(addresses []net.Addr, newAddr net.Addr) []net.Addr {
	duplicate := false
	if newAddr.Network() != "unix" {
		address, portStr, err := net.SplitHostPort(newAddr.String())
		if err != nil {
			log.Printf("DBG: unable to split host/port for %#v: %v", newAddr.String(), err)
			return addresses
		}
		port, err := strconv.ParseInt(portStr, 10, 0)
		if err != nil {
			log.Printf("DBG: unable to parse port %#v: %v", portStr, err)
			return addresses
		}
		for i, v := range addresses {
			if v.Network() != newAddr.Network() {
				continue
			}
			_, otherPortStr, err := net.SplitHostPort(v.String())
			if err != nil {
				log.Printf("DBG: unable to split host/port for %#v: %v", v.String(), err)
				return addresses
			}
			otherPort, err := strconv.ParseInt(otherPortStr, 10, 0)
			if err != nil {
				log.Printf("DBG: unable to parse port %#v: %v", portStr, err)
				return addresses
			}
			if otherPort == port {
				duplicate = true
				// We prefere 127.* address
				if strings.HasPrefix(address, "127.") {
					addresses[i] = newAddr
				}
				break
			}
		}
	}
	if !duplicate {
		addresses = append(addresses, newAddr)
	}
	return addresses
}
