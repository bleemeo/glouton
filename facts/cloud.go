// Copyright 2015-2019 Bleemeo
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
	"encoding/json"
	"fmt"
	"glouton/logger"
	"net"
	"strconv"
	"strings"
)

type azureTag struct {
	Key   string `json:"name"`
	Value string `json:"value"`
}

type azureIPAddress struct {
	Private string `json:"privateIpAddress"`
	Public  string `json:"publicIpAddress,omitempty"`
}

type azureIPSubnet struct {
	IP     string `json:"address"`
	Prefix string `json:"prefix"`
}

type azureInterfaceIP4 struct {
	Addresses []azureIPAddress `json:"ipAddress"`
	Subnets   []azureIPSubnet  `json:"subnet"`
}

/*
// We do not return ipv6 adresses for now
type azureInterfaceIP6 struct {
	Addresses []azureIpAddress `json:"ipAddress"`
	// there is no subnet information for ipv6 adresses, for whatever reason
}
*/

type azureNetworkInterface struct {
	IPv4 azureInterfaceIP4 `json:"ipv4"`
	//IPv6       azureInterfaceIP6 `json:"ipv6"`
	MacAddress string `json:"macAddress"`
}

type azureNetworks struct {
	Interfaces []azureNetworkInterface `json:"interface"`
}

type azureCompute struct {
	ID               string     `json:"vmId"`
	VMSize           string     `json:"vmSize"`
	Location         string     `json:"location"`
	Name             string     `json:"name"`
	PlacementGroupID string     `json:"placementGroupId"`
	Tags             []azureTag `json:"tagsList"`
}

type azureInstance struct {
	Instance azureCompute  `json:"compute"`
	Network  azureNetworks `json:"network"`
}

func azureFacts(ctx context.Context) map[string]string {
	facts := make(map[string]string)

	instanceData := httpQuery(ctx, "http://169.254.169.254/metadata/instance?api-version=2019-11-01", []string{"Metadata:true"})
	if instanceData == "" {
		return facts
	}

	var inst azureInstance

	err := json.Unmarshal([]byte(instanceData), &inst)
	if err != nil {
		logger.V(2).Printf("facts: couldn't parse azure instance informations, some facts may be missing on your dashboard: %v", err)
		return facts
	}

	facts["azure_instance_id"] = inst.Instance.ID
	facts["azure_instance_type"] = inst.Instance.VMSize
	facts["azure_local_hostname"] = inst.Instance.Name
	facts["azure_location"] = inst.Instance.Location

	if inst.Instance.PlacementGroupID != "" {
		facts["azure_placement_group_id"] = inst.Instance.PlacementGroupID
	}

	publicIPs := make([]string, 0, 1)
	privateIPs := make([]string, 0, 1)
	subnets := make([]string, 0, 1)

	// collect network informations (the list of public ips, private ips and the subnets the privates IPs belong to)
	for _, network := range inst.Network.Interfaces {
		interfaceSubnets := make([]net.IPNet, 0, len(network.IPv4.Subnets))

		for _, sub := range network.IPv4.Subnets {
			prefix, err := strconv.Atoi(sub.Prefix)
			if err != nil {
				logger.V(2).Printf("facts: couldn't parse azure network subnets: %v", err)
				return facts
			}

			interfaceSubnets = append(interfaceSubnets, net.IPNet{IP: net.ParseIP(sub.IP), Mask: net.CIDRMask(prefix, 32)})
		}

		// At the time of this writing, an instance cannot have a public Ipv6 address on Azure (public IPv6 are only available on Azure LBs),
		// so I decided to not consider IPv6 at all
		for _, ip := range network.IPv4.Addresses {
			if ip.Public != "" {
				ip := net.ParseIP(ip.Public)
				publicIPs = append(publicIPs, ip.String())
			}

			ip := net.ParseIP(ip.Private)
			foundSubnet := false

			for _, sub := range interfaceSubnets {
				if sub.Contains(ip) {
					foundSubnet = true

					privateIPs = append(privateIPs, ip.String())
					subnets = append(subnets, sub.String())

					break
				}
			}

			if !foundSubnet {
				logger.V(2).Printf("facts: couldn't find the network subnet for ip %s", ip)
			}
		}
	}

	if len(publicIPs) > 0 {
		facts["azure_network_public_ips"] = strings.Join(publicIPs, ",")
		facts["azure_network_private_ips"] = strings.Join(privateIPs, ",")
		facts["azure_network_private_subnets"] = strings.Join(subnets, ",")
	}

	tags := make([]string, 0, len(inst.Instance.Tags))

	for _, v := range inst.Instance.Tags {
		formattedValue := strings.ReplaceAll(strings.ReplaceAll(v.Value, ":", "\\:"), ",", "\\,")
		tags = append(tags, fmt.Sprintf("%s:%s", v.Key, formattedValue))
	}

	if len(tags) > 0 {
		facts["azure_tags"] = strings.Join(tags, ",")
	}

	return facts
}

type gceNetworkExternalIP struct {
	IP string `json:"externalIp"`
}

type gceNetworkInterface struct {
	ExternalIPs []gceNetworkExternalIP `json:"accessConfigs"`
	IP          string                 `json:"IP"`
	Mask        string                 `json:"subnetmask"`
}

type gceInstance struct {
	ID          int                   `json:"id"`
	Hostname    string                `json:"hostname"`
	MachineType string                `json:"machineType"`
	Name        string                `json:"name"`
	Networks    []gceNetworkInterface `json:"networkInterfaces"`
	Attributes  map[string]string     `json:"attributes"`
	Zone        string                `json:"zone"`
}

func gceFacts(ctx context.Context) map[string]string {
	facts := make(map[string]string)

	// retrieve the ID of the (GCE) project for which this VM instance was spawned. We will use it later to "sanitize" machine types, zone names, and so on.
	projectID, err := strconv.Atoi(httpQuery(ctx, "http://metadata.google.internal/computeMetadata/v1/project/numeric-project-id", []string{"Metadata-Flavor:Google"}))
	if err != nil {
		logger.V(2).Printf("facts: couldn't retrieve your google cloud project ID, some facts may be missing on your dashboard: %v", err)
		return facts
	}

	// retrieve the metadata itself
	instanceData := httpQuery(ctx, "http://metadata.google.internal/computeMetadata/v1/instance/?recursive=true", []string{"Metadata-Flavor:Google"})
	if instanceData == "" {
		return facts
	}

	var inst gceInstance

	err = json.Unmarshal([]byte(instanceData), &inst)
	if err != nil {
		logger.V(2).Printf("facts: couldn't parse google cloud instance informations, some facts may be missing on your dashboard: %v", err)
		return facts
	}

	machineTypePrefix := fmt.Sprintf("projects/%d/machineTypes/", projectID)
	if strings.HasPrefix(inst.MachineType, machineTypePrefix) {
		facts["gce_instance_type"] = inst.MachineType[len(machineTypePrefix):]
	}

	zonePrefix := fmt.Sprintf("projects/%d/zones/", projectID)
	if strings.HasPrefix(inst.Zone, zonePrefix) {
		facts["gce_location"] = inst.Zone[len(zonePrefix):]
	}

	facts["gce_local_hostname"] = inst.Hostname
	facts["gce_local_shortname"] = inst.Name
	facts["gce_instance_id"] = strconv.Itoa(inst.ID)

	publicIPs := make([]string, 0, 1)
	privateIPs := make([]string, 0, 1)
	subnets := make([]string, 0, 1)

	// collect network informations (the list of public ips, private ips and the subnets the privates IPs belong to)
	for _, network := range inst.Networks {
		for _, publicIP := range network.ExternalIPs {
			if publicIP.IP != "" {
				publicIPs = append(publicIPs, publicIP.IP)
			}
		}

		// At the time of this writing, not only an instance cannot have a public Ipv6 address (only available on LBs),
		// but Google VPCs don't support IPv6 at all (unlike Azure and AWS) !
		ip := net.ParseIP(network.IP)

		maskSplitted := strings.Split(network.Mask, ".")
		if len(maskSplitted) != 4 {
			logger.V(2).Printf("Couldn't parse the network mask %s", network.Mask)
			continue
		}

		a, err1 := strconv.Atoi(maskSplitted[0])
		b, err2 := strconv.Atoi(maskSplitted[1])
		c, err3 := strconv.Atoi(maskSplitted[2])
		d, err4 := strconv.Atoi(maskSplitted[3])

		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			logger.V(2).Printf("Couldn't parse the network mask %s", network.Mask)
			continue
		}

		subnetMask := net.IPv4Mask(byte(a), byte(b), byte(c), byte(d))
		subnetIP := ip.Mask(subnetMask)
		subnet := net.IPNet{IP: subnetIP, Mask: subnetMask}

		privateIPs = append(privateIPs, ip.String())
		subnets = append(subnets, subnet.String())
	}

	if len(publicIPs) > 0 {
		facts["gce_network_public_ips"] = strings.Join(publicIPs, ",")
		facts["gce_network_private_ips"] = strings.Join(privateIPs, ",")
		facts["gce_network_private_subnets"] = strings.Join(subnets, ",")
	}

	tags := make([]string, 0, len(inst.Attributes))

	for k, v := range inst.Attributes {
		if k == "ssh-keys" {
			continue
		}

		v = strings.ReplaceAll(strings.ReplaceAll(v, ":", "\\:"), ",", "\\,")
		tags = append(tags, fmt.Sprintf("%s:%s", k, v))
	}

	if len(tags) > 0 {
		// "tags" are in fact called attributes in GCE parlance, we do not report the GCE tags (presented as "network tags" in the cloud console)
		facts["gce_tags"] = strings.Join(tags, ",")
	}

	return facts
}

func awsFacts(ctx context.Context) map[string]string {
	facts := make(map[string]string)
	facts["aws_ami_id"] = urlContent(ctx, "http://169.254.169.254/latest/meta-data/ami-id")

	if facts["aws_ami_id"] == "" {
		// If first request fail, don't try other one, it's probably not an
		// AWS EC2.
		return facts
	}

	facts["aws_instance_id"] = urlContent(ctx, "http://169.254.169.254/latest/meta-data/instance-id")
	facts["aws_instance_type"] = urlContent(ctx, "http://169.254.169.254/latest/meta-data/instance-type")
	facts["aws_local_hostname"] = urlContent(ctx, "http://169.254.169.254/latest/meta-data/local-hostname")
	facts["aws_security_groups"] = urlContent(ctx, "http://169.254.169.254/latest/meta-data/security-groups")
	facts["aws_public_ipv4"] = urlContent(ctx, "http://169.254.169.254/latest/meta-data/public-ipv4")
	facts["aws_placement"] = urlContent(ctx, "http://169.254.169.254/latest/meta-data/placement/availability-zone")

	baseURL := "http://169.254.169.254/latest/meta-data/network/interfaces/macs/"

	macs := urlContent(ctx, baseURL)
	if macs == "" {
		return facts
	}

	resultVPC := make([]string, 0)
	resultIPv4 := make([]string, 0)

	for _, line := range strings.Split(macs, "\n") {
		t := urlContent(ctx, baseURL+line+"vpc-id")
		if t != "" {
			resultVPC = append(resultVPC, t)
		}

		t = urlContent(ctx, baseURL+line+"vpc-ipv4-cidr-block")
		if t != "" {
			resultIPv4 = append(resultIPv4, t)
		}
	}

	if len(resultVPC) > 0 {
		facts["aws_vpc_id"] = strings.Join(resultVPC, ",")
	}

	if len(resultIPv4) > 0 {
		facts["aws_vpc_ipv4_cidr_block"] = strings.Join(resultIPv4, ",")
	}

	return facts
}
