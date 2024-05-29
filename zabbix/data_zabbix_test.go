// Copyright 2015-2024 Bleemeo
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

//nolint:gochecknoglobals
package zabbix

import "errors"

var allPackets = []packetCapture{
	versionPacket,
	pingPacket,
	netIfDiscovery,
	netIfLo,
	cpuUtil,
	doesNotExist,
}

var versionPacket = packetCapture{
	Description: "agent.version",
	QueryKey:    "agent.version",
	QueryRaw: []byte{
		0x5a, 0x42, 0x58, 0x44, 0x01, 0x0d, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x61, 0x67, 0x65,
		0x6e, 0x74, 0x2e, 0x76, 0x65, 0x72, 0x73, 0x69,
		0x6f, 0x6e,
	},
	ReplyRaw: []byte{
		0x5a, 0x42, 0x58, 0x44, 0x01, 0x05, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x34, 0x2e, 0x32,
		0x2e, 0x34,
	},
	ReplyString: "4.2.4",
}

var pingPacket = packetCapture{
	Description: "agent.ping",
	QueryKey:    "agent.ping",
	QueryRaw: []byte{
		0x5a, 0x42, 0x58, 0x44, 0x01, 0x0a, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x61, 0x67, 0x65,
		0x6e, 0x74, 0x2e, 0x70, 0x69, 0x6e, 0x67,
	},
	ReplyRaw: []byte{
		0x5a, 0x42, 0x58, 0x44, 0x01, 0x01, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x31,
	},
	ReplyString: "1",
}

var netIfDiscovery = packetCapture{
	Description: "net.if.discovery",
	QueryKey:    "net.if.discovery",
	QueryRaw: []byte{
		0x5a, 0x42, 0x58, 0x44, 0x01, 0x10, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x6e, 0x65, 0x74,
		0x2e, 0x69, 0x66, 0x2e, 0x64, 0x69, 0x73, 0x63,
		0x6f, 0x76, 0x65, 0x72, 0x79,
	},
	ReplyRaw: []byte{
		0x5a, 0x42, 0x58, 0x44, 0x01, 0xb3, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x5b, 0x7b, 0x22,
		0x7b, 0x23, 0x49, 0x46, 0x4e, 0x41, 0x4d, 0x45,
		0x7d, 0x22, 0x3a, 0x22, 0x77, 0x6c, 0x70, 0x32,
		0x73, 0x30, 0x22, 0x7d, 0x2c, 0x7b, 0x22, 0x7b,
		0x23, 0x49, 0x46, 0x4e, 0x41, 0x4d, 0x45, 0x7d,
		0x22, 0x3a, 0x22, 0x76, 0x65, 0x74, 0x68, 0x30,
		0x61, 0x38, 0x30, 0x33, 0x61, 0x64, 0x22, 0x7d,
		0x2c, 0x7b, 0x22, 0x7b, 0x23, 0x49, 0x46, 0x4e,
		0x41, 0x4d, 0x45, 0x7d, 0x22, 0x3a, 0x22, 0x64,
		0x6f, 0x63, 0x6b, 0x65, 0x72, 0x30, 0x22, 0x7d,
		0x2c, 0x7b, 0x22, 0x7b, 0x23, 0x49, 0x46, 0x4e,
		0x41, 0x4d, 0x45, 0x7d, 0x22, 0x3a, 0x22, 0x76,
		0x65, 0x74, 0x68, 0x33, 0x63, 0x64, 0x33, 0x61,
		0x34, 0x32, 0x22, 0x7d, 0x2c, 0x7b, 0x22, 0x7b,
		0x23, 0x49, 0x46, 0x4e, 0x41, 0x4d, 0x45, 0x7d,
		0x22, 0x3a, 0x22, 0x6c, 0x6f, 0x22, 0x7d, 0x2c,
		0x7b, 0x22, 0x7b, 0x23, 0x49, 0x46, 0x4e, 0x41,
		0x4d, 0x45, 0x7d, 0x22, 0x3a, 0x22, 0x76, 0x65,
		0x74, 0x68, 0x32, 0x30, 0x61, 0x64, 0x34, 0x65,
		0x61, 0x22, 0x7d, 0x2c, 0x7b, 0x22, 0x7b, 0x23,
		0x49, 0x46, 0x4e, 0x41, 0x4d, 0x45, 0x7d, 0x22,
		0x3a, 0x22, 0x76, 0x65, 0x74, 0x68, 0x62, 0x63,
		0x30, 0x39, 0x38, 0x31, 0x31, 0x22, 0x7d, 0x5d,
	},
	ReplyString: `[{"{#IFNAME}":"wlp2s0"},{"{#IFNAME}":"veth0a803ad"},{"{#IFNAME}":"docker0"},{"{#IFNAME}":"veth3cd3a42"},{"{#IFNAME}":"lo"},{"{#IFNAME}":"veth20ad4ea"},{"{#IFNAME}":"vethbc09811"}]`,
}

var netIfLo = packetCapture{
	Description: "net.if.in[lo]",
	QueryKey:    "net.if.in",
	QueryArgs:   []string{"lo"},
	QueryRaw: []byte{
		0x5a, 0x42, 0x58, 0x44, 0x01, 0x0d, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x6e, 0x65, 0x74,
		0x2e, 0x69, 0x66, 0x2e, 0x69, 0x6e, 0x5b, 0x6c,
		0x6f, 0x5d,
	},
	ReplyRaw: []byte{
		0x5a, 0x42, 0x58, 0x44, 0x01, 0x06, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x37, 0x39, 0x37,
		0x38, 0x32, 0x36,
	},
	ReplyString: "797826",
}

var cpuUtil = packetCapture{
	Description: "system.cpu.util",
	QueryKey:    "system.cpu.util",
	QueryArgs:   []string{"all", "user", "avg1"},
	QueryRaw: []byte{
		0x5a, 0x42, 0x58, 0x44, 0x01, 0x1e, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x73, 0x79, 0x73,
		0x74, 0x65, 0x6d, 0x2e, 0x63, 0x70, 0x75, 0x2e,
		0x75, 0x74, 0x69, 0x6c, 0x5b, 0x61, 0x6c, 0x6c,
		0x2c, 0x75, 0x73, 0x65, 0x72, 0x2c, 0x61, 0x76,
		0x67, 0x31, 0x5d,
	},
	ReplyRaw: []byte{
		0x5a, 0x42, 0x58, 0x44, 0x01, 0x08, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x33, 0x2e, 0x30,
		0x30, 0x38, 0x31, 0x32, 0x33,
	},
	ReplyString: "3.008123",
}

var doesNotExist = packetCapture{
	Description: "Does not exists",
	QueryKey:    "agent.does.not.exists",
	QueryRaw: []byte{
		0x5a, 0x42, 0x58, 0x44, 0x01, 0x15, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x61, 0x67, 0x65,
		0x6e, 0x74, 0x2e, 0x64, 0x6f, 0x65, 0x73, 0x2e,
		0x6e, 0x6f, 0x74, 0x2e, 0x65, 0x78, 0x69, 0x73,
		0x74, 0x73,
	},
	ReplyRaw: []byte{
		0x5a, 0x42, 0x58, 0x44, 0x01, 0x26, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x5a, 0x42, 0x58,
		0x5f, 0x4e, 0x4f, 0x54, 0x53, 0x55, 0x50, 0x50,
		0x4f, 0x52, 0x54, 0x45, 0x44, 0x00, 0x55, 0x6e,
		0x73, 0x75, 0x70, 0x70, 0x6f, 0x72, 0x74, 0x65,
		0x64, 0x20, 0x69, 0x74, 0x65, 0x6d, 0x20, 0x6b,
		0x65, 0x79, 0x2e,
	},
	// This errors represents a raw zabbix response, hence we don't respect go's errors guideline
	ReplyError: errors.New("Unsupported item key"), //nolint:stylecheck,goerr113
}
