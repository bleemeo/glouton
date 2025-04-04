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
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bleemeo/glouton/logger"

	psutilNet "github.com/shirou/gopsutil/v4/net"
)

// NetstatProvider provide netstat information from both a file (output of netstat command) and using gopsutil
//
// The file is useful since gopsutil will be run with current privilege which are unlikely to be root.
// The file should be the output of netstat run as root.
type NetstatProvider struct {
	FilePath string
}

// Netstat return a mapping from PID to listening addresses
//
// Supported addresses network is currently "tcp", "udp" or "unix".
func (np NetstatProvider) Netstat(_ context.Context, processes map[int]Process) (netstat map[int][]ListenAddress, err error) {
	netstat = make(map[int][]ListenAddress)

	netstatInfo, errFile := os.Stat(np.FilePath)
	if errFile == nil {
		var netstatData []byte

		netstatData, errFile = os.ReadFile(np.FilePath)
		if errFile == nil {
			netstat = decodeNetstatFile(string(netstatData))

			np.cleanRecycledPIDs(netstat, processes, netstatInfo.ModTime())
		}
	}

	dynamicNetstat, err := psutilNet.Connections("inet")
	if err != nil {
		return netstat, err
	}

	np.mergeNetstats(netstat, dynamicNetstat)

	return netstat, errFile
}

func (np NetstatProvider) mergeNetstats(netstat map[int][]ListenAddress, dynamicNetstat []psutilNet.ConnectionStat) {
	for _, c := range dynamicNetstat {
		if c.Pid == 0 {
			continue
		}

		if c.Status != "LISTEN" {
			continue
		}

		address := c.Laddr.IP

		var protocol string

		switch c.Type {
		case syscall.SOCK_STREAM:
			protocol = "tcp"
		case syscall.SOCK_DGRAM:
			protocol = "udp"
		default:
			continue
		}

		if c.Family == syscall.AF_INET6 {
			protocol += "6"
		}

		netstat[int(c.Pid)] = addAddress(netstat[int(c.Pid)], ListenAddress{
			NetworkFamily: protocol,
			Address:       address,
			Port:          int(c.Laddr.Port),
		})
	}
}

func (np NetstatProvider) cleanRecycledPIDs(netstat map[int][]ListenAddress, processes map[int]Process, modTime time.Time) {
	for _, c := range processes {
		if c.PID == 0 {
			continue
		}

		if modTime.Before(c.CreateTime) {
			// The process running with p.PID recycled a previously used pid referenced in the netstat file.
			// We need to flush the last address as it is related to the previous process.
			delete(netstat, c.PID)
		}
	}
}

var (
	netstatRE = regexp.MustCompile(
		`(?P<protocol>udp6?|tcp6?)\s+\d+\s+\d+\s+(?P<address>[0-9a-f.:]+):(?P<port>\d+)\s+[0-9a-f.:*]+\s+(LISTEN)?\s+(?P<pid>\d+)/(?P<program>.*)$`,
	)
	netstatUnixRE = regexp.MustCompile(
		`^(?P<protocol>unix)\s+\d+\s+\[\s+(ACC |W |N )+\s*\]\s+(DGRAM|STREAM)\s+LISTENING\s+(\d+\s+)?(?P<pid>\d+)/(?P<program>.*)\s+(?P<address>.+)$`,
	)
)

// ListenAddress is net.Addr implmentation.
type ListenAddress struct {
	NetworkFamily string
	Address       string
	Port          int
}

// Network is the method from net.Addr.
func (l ListenAddress) Network() string {
	return l.NetworkFamily
}

func (l ListenAddress) String() string {
	if l.NetworkFamily == "unix" {
		return l.Address
	}

	return fmt.Sprintf("%s:%d", l.Address, l.Port)
}

func decodeNetstatFile(data string) map[int][]ListenAddress {
	result := make(map[int][]ListenAddress)
	lines := strings.Split(data, "\n")

	for _, line := range lines {
		var (
			protocol, address string
			pid, port         int64
			err               error
		)

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

		addresses := result[int(pid)]
		if addresses == nil {
			addresses = make([]ListenAddress, 0)
		}

		result[int(pid)] = addAddress(addresses, ListenAddress{
			NetworkFamily: protocol,
			Address:       address,
			Port:          int(port),
		})
	}

	return result
}

func addAddress(addresses []ListenAddress, newAddr ListenAddress) []ListenAddress {
	duplicate := false

	if newAddr.NetworkFamily != "unix" {
		if newAddr.NetworkFamily == "tcp6" || newAddr.NetworkFamily == "udp6" {
			if newAddr.Address == "::" {
				newAddr.Address = "0.0.0.0"
			}

			if newAddr.Address == "::1" {
				newAddr.Address = "127.0.0.1"
			}

			if strings.Contains(newAddr.Address, ":") {
				// It's still an IPv6 address, we don't know how to convert it to IPv4
				return addresses
			}

			newAddr.NetworkFamily = newAddr.NetworkFamily[:3]
		}

		for i, v := range addresses {
			if v.Network() != newAddr.Network() {
				continue
			}

			_, otherPortStr, err := net.SplitHostPort(v.String())
			if err != nil {
				logger.V(1).Printf("unable to split host/port for %#v: %v", v.String(), err)

				return addresses
			}

			otherPort, err := strconv.ParseInt(otherPortStr, 10, 0)
			if err != nil {
				logger.V(1).Printf("unable to parse port %#v: %v", otherPortStr, err)

				return addresses
			}

			if int(otherPort) == newAddr.Port {
				duplicate = true
				// We prefere 127.* address
				if strings.HasPrefix(newAddr.Address, "127.") {
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
