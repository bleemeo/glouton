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
	"encoding/json"
	"reflect"
	"testing"
)

const (
	azureTestInstance string = `{
		"compute": {
			"azEnvironment": "AzurePublicCloud",
			"customData": "",
			"location": "FranceCentral",
			"name": "test-ub",
			"offer": "UbuntuServer",
			"osType": "Linux",
			"placementGroupId": "",
			"plan": {
				"name": "",
				"product": "",
				"publisher": ""
			},
			"platformFaultDomain": "0",
			"platformUpdateDomain": "0",
			"provider": "Microsoft.Compute",
			"publicKeys": [
				{
					"keyData": "<REDACTED>",
					"path": "/home/azureuser/.ssh/authorized_keys"
				}
			],
			"publisher": "Canonical",
			"resourceGroupName": "test-rsc-grp",
			"resourceId": "/subscriptions/3fced3a9-f7d3-47a8-8a4f-019a9c3468fc/resourceGroups/test-rsc-grp/providers/Microsoft.Compute/virtualMachines/test-ub",
			"sku": "18.04-LTS",
			"storageProfile": {
				"dataDisks": [],
				"imageReference": {
					"id": "",
					"offer": "UbuntuServer",
					"publisher": "Canonical",
					"sku": "18.04-LTS",
					"version": "latest"
				},
				"osDisk": {
					"caching": "ReadWrite",
					"createOption": "FromImage",
					"diffDiskSettings": {
						"option": ""
					},
					"diskSizeGB": "30",
					"encryptionSettings": {
						"enabled": "false"
					},
					"image": {
						"uri": ""
					},
					"managedDisk": {
						"id": "/subscriptions/3fced3a9-f7d3-47a8-8a4f-019a9c3468fc/resourceGroups/TEST-RSC-GRP/providers/Microsoft.Compute/disks/test-ub_OsDisk_1_96693e1c1ff24bf69cc1ccd2a1f0ea94",
						"storageAccountType": ""
					},
					"name": "test-ub_OsDisk_1_96693e1c1ff24bf69cc1ccd2a1f0ea94",
					"osType": "Linux",
					"vhd": {
						"uri": ""
					},
					"writeAcceleratorEnabled": "false"
				}
			},
			"subscriptionId": "3fced3a9-f7d3-47a8-8a4f-019a9c3468fc",
			"tags": "a:bn;d:b;df:e:tr,52;f:hj;th:\"et\"r",
			"tagsList": [
				{
					"name": "a",
					"value": "bn"
				},
				{
					"name": "d",
					"value": "b"
				},
				{
					"name": "df",
					"value": "e:tr,52"
				},
				{
					"name": "f",
					"value": "hj"
				},
				{
					"name": "th",
					"value": "\"et\"r"
				}
			],
			"version": "18.04.202007160",
			"vmId": "3fc366fa-a6df-4754-89a3-f3d2cce12c53",
			"vmScaleSetName": "",
			"vmSize": "Standard_B1s",
			"zone": ""
		},
		"network": {
			"interface": [
				{
					"ipv4": {
						"ipAddress": [
							{
								"privateIpAddress": "10.0.1.4",
								"publicIpAddress": "20.43.60.50"
							}
						],
						"subnet": [
							{
								"address": "10.0.1.0",
								"prefix": "24"
							}
						]
					},
					"ipv6": {
						"ipAddress": [
							{
								"privateIpAddress": "fd15::5"
							}
						]
					},
					"macAddress": "000D3AE799B7"
				},
				{
					"ipv4": {
						"ipAddress": [
							{
								"privateIpAddress": "10.0.1.5",
								"publicIpAddress": ""
							},
							{
								"privateIpAddress": "10.0.1.10",
								"publicIpAddress": ""
							}
						],
						"subnet": [
							{
								"address": "10.0.1.0",
								"prefix": "24"
							}
						]
					},
					"ipv6": {
						"ipAddress": [
							{
								"privateIpAddress": "fd15::4"
							}
						]
					},
					"macAddress": "000D3AE79B59"
				}
			]
		}
	}`

	gceTestInstance string = `{
		"attributes": {
			"a": "d,e:f",
			"edft": "\"\"\"45",
			"ssh-keys": "nightmared:ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGovbV6uGHeedQQmDxWOc+n30LDCnsnWRVz4VyWme+4f nightmared@laptop-nightmared"
		},
		"cpuPlatform": "Intel Haswell",
		"description": "",
		"disks": [
			{
				"deviceName": "instance-2",
				"index": 0,
				"interface": "SCSI",
				"mode": "READ_WRITE",
				"type": "PERSISTENT"
			}
		],
		"guestAttributes": {},
		"hostname": "instance-2.europe-west1-d.c.linen-inscriber-249613.internal",
		"id": 8849188986350618000,
		"image": "projects/debian-cloud/global/images/debian-9-stretch-v20200714",
		"legacyEndpointAccess": {
			"0.1": 0,
			"v1beta1": 0
		},
		"licenses": [
			{
				"id": "1000205"
			}
		],
		"machineType": "projects/383030834456/machineTypes/f1-micro",
		"maintenanceEvent": "NONE",
		"name": "instance-2",
		"networkInterfaces": [
			{
				"accessConfigs": [
					{
						"externalIp": "146.148.25.101",
						"type": "ONE_TO_ONE_NAT"
					}
				],
				"dnsServers": [
					"169.254.169.254"
				],
				"forwardedIps": [],
				"gateway": "10.0.0.1",
				"ip": "10.0.0.2",
				"ipAliases": [],
				"mac": "42:01:0a:00:00:02",
				"mtu": 1460,
				"network": "projects/383030834456/networks/default",
				"subnetmask": "255.255.255.0",
				"targetInstanceIps": []
			},
			{
				"accessConfigs": [
					{
						"externalIp": "",
						"type": "ONE_TO_ONE_NAT"
					}
				],
				"dnsServers": [
					"169.254.169.254"
				],
				"forwardedIps": [],
				"gateway": "10.0.1.1",
				"ip": "10.0.1.2",
				"ipAliases": [],
				"mac": "42:01:0a:00:01:02",
				"mtu": 1460,
				"network": "projects/383030834456/networks/vpc2",
				"subnetmask": "255.255.255.0",
				"targetInstanceIps": []
			}
		],
		"preempted": "FALSE",
		"remainingCpuTime": -1,
		"scheduling": {
			"automaticRestart": "TRUE",
			"onHostMaintenance": "MIGRATE",
			"preemptible": "FALSE"
		},
		"serviceAccounts": {
			"383030834456-compute@developer.gserviceaccount.com": {
				"aliases": [
					"default"
				],
				"email": "383030834456-compute@developer.gserviceaccount.com",
				"scopes": [
					"https://www.googleapis.com/auth/devstorage.read_only",
					"https://www.googleapis.com/auth/logging.write",
					"https://www.googleapis.com/auth/monitoring.write",
					"https://www.googleapis.com/auth/servicecontrol",
					"https://www.googleapis.com/auth/service.management.readonly",
					"https://www.googleapis.com/auth/trace.append"
				]
			},
			"default": {
				"aliases": [
					"default"
				],
				"email": "383030834456-compute@developer.gserviceaccount.com",
				"scopes": [
					"https://www.googleapis.com/auth/devstorage.read_only",
					"https://www.googleapis.com/auth/logging.write",
					"https://www.googleapis.com/auth/monitoring.write",
					"https://www.googleapis.com/auth/servicecontrol",
					"https://www.googleapis.com/auth/service.management.readonly",
					"https://www.googleapis.com/auth/trace.append"
				]
			}
		},
		"tags": [],
		"virtualClock": {
			"driftToken": "0"
		},
		"zone": "projects/383030834456/zones/europe-west1-d"
	}`
	gceTestProjectID int64 = 383030834456
)

func TestAzureDecodeMetadata(t *testing.T) {
	facts := map[string]string{}

	var inst azureInstance

	err := json.Unmarshal([]byte(azureTestInstance), &inst)
	if err != nil {
		t.Fatalf("Couldn't parse the metadata: %v", err)
	}

	parseAzureFacts(inst, facts)

	want := map[string]string{
		"azure_instance_id":             "3fc366fa-a6df-4754-89a3-f3d2cce12c53",
		"azure_instance_type":           "Standard_B1s",
		"azure_local_hostname":          "test-ub",
		"azure_location":                "FranceCentral",
		"azure_network_private_ips":     "10.0.1.4,10.0.1.5,10.0.1.10",
		"azure_network_private_subnets": "10.0.1.0/24,10.0.1.0/24,10.0.1.0/24",
		"azure_network_public_ips":      "20.43.60.50",
		"azure_tags":                    "a:bn,d:b,df:e\\:tr\\,52,f:hj,th:\"et\"r",
	}

	if !reflect.DeepEqual(facts, want) {
		t.Errorf("parseAzureFacts(...) = %v, want %v", facts, want)
	}
}

func TestGceDecodeMetadata(t *testing.T) {
	facts := map[string]string{}

	var inst gceInstance

	err := json.Unmarshal([]byte(gceTestInstance), &inst)
	if err != nil {
		t.Fatalf("Couldn't parse the metadata: %v", err)
	}

	parseGceFacts(gceTestProjectID, inst, facts)

	want := map[string]string{
		"gce_instance_id":             "8849188986350618000",
		"gce_instance_type":           "f1-micro",
		"gce_local_hostname":          "instance-2.europe-west1-d.c.linen-inscriber-249613.internal",
		"gce_local_shortname":         "instance-2",
		"gce_location":                "europe-west1-d",
		"gce_network_private_ips":     "10.0.0.2,10.0.1.2",
		"gce_network_private_subnets": "10.0.0.0/24,10.0.1.0/24",
		"gce_network_public_ips":      "146.148.25.101",
		"gce_tags":                    "a:d\\,e\\:f,edft:\"\"\"45",
	}

	if !reflect.DeepEqual(facts, want) {
		t.Errorf("parseAzureFacts(...) = %v, want %v", facts, want)
	}
}
