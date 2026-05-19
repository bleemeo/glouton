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

package synchronizer

import (
	"fmt"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
)

const (
	// fixed "random" values are enough for tests.
	accountID          string = "9da59f53-1d90-4441-ae58-42c661cfea83"
	agentID            string = "33708da4-28d4-45aa-b811-49c82b594627"
	registrationKey    string = "e2c22e59-0621-49e6-b5dd-bdb02cbac9f1"
	containerID        string = "f21b2ac5-2173-42c2-a26a-db5ce53490cf"
	containerID2       string = "ce2ee1b5-6445-47a4-835e-9a001ec55c69"
	activeMonitorURL   string = "http://bleemeo.com"
	snmpAddress        string = "127.0.0.1"
	testAgentFQDN      string = "test.bleemeo.com"
	testK8SClusterName string = "k8s_demo"

	// passwordAlreadySet is used in test agent payloads to indicate the password was set during registration.
	passwordAlreadySet = "password already set"

	// Config IDs used in default test account configs.
	cfgIDDocker   = "cfg-docker"
	cfgIDSNMPInt  = "cfg-snmp-int"
	cfgIDAgentRes = "cfg-agent-res"
	cfgIDSNMPRes  = "cfg-snmp-res"
	cfgIDMonRes   = "cfg-mon-res"
)

// this is a go limitation, these are constants but we have to treat them as variables
//
//nolint:gochecknoglobals
var (
	agentTypeAgent = bleemeoTypes.AgentType{
		DisplayName: "A server monitored with Bleemeo agent",
		ID:          "61zb6a83-d90a-4165-bf04-944e0b2g2a10",
		Name:        bleemeo.AgentType_Agent,
	}
	agentTypeSNMP = bleemeoTypes.AgentType{
		DisplayName: "A server monitored with SNMP agent",
		ID:          "823b6a83-d70a-4768-be64-50450b282a30",
		Name:        bleemeo.AgentType_SNMP,
	}
	agentTypeMonitor = bleemeoTypes.AgentType{
		DisplayName: "A website monitored with connection check",
		ID:          "41afe63c-fa1c-4b84-b92b-028269101fde",
		Name:        bleemeo.AgentType_Monitor,
	}
	agentTypeKubernetes = bleemeoTypes.AgentType{
		DisplayName: "k8s",
		ID:          "f8477dcd-36d8-489f-a6b8-e52f6bc013d2",
		Name:        bleemeo.AgentType_K8s,
	}
	agentTypeVSphereCluster = bleemeoTypes.AgentType{
		DisplayName: "A vSphere cluster",
		ID:          "a424f3d1-5824-49c2-a4c7-08ebb58f1e1c",
		Name:        bleemeo.AgentType_vSphereCluster,
	}
	agentTypeVSphereHost = bleemeoTypes.AgentType{
		DisplayName: "A vSphere host",
		ID:          "eef9553e-f6f8-483d-9360-979ae24974af",
		Name:        bleemeo.AgentType_vSphereHost,
	}
	agentTypeVSphereVM = bleemeoTypes.AgentType{
		DisplayName: "A vSphere VM",
		ID:          "ae5d4581-e74a-4c11-8c9d-fde62a7073e5",
		Name:        bleemeo.AgentType_vSphereVM,
	}

	// defaultAccountConfigs is the default set of Config objects used by the test mock.
	// It corresponds to an account with SNMP and Docker integration enabled,
	// with per-agent-type metric resolutions.
	defaultAccountConfigs = []bleemeoTypes.Config{
		{ID: cfgIDDocker, Type: bleemeoTypes.ConfigTypeDockerIntegration, Account: accountID, Value: true},
		{ID: cfgIDSNMPInt, Type: bleemeoTypes.ConfigTypeSNMPIntegration, Account: accountID, Value: true},
		{ID: cfgIDAgentRes, Type: bleemeoTypes.ConfigTypeAgentMetricsResolution, Account: accountID, AgentType: agentTypeAgent.ID, Value: float64(10)},
		{ID: cfgIDSNMPRes, Type: bleemeoTypes.ConfigTypeAgentMetricsResolution, Account: accountID, AgentType: agentTypeSNMP.ID, Value: float64(60)},
		{ID: cfgIDMonRes, Type: bleemeoTypes.ConfigTypeAgentMetricsResolution, Account: accountID, AgentType: agentTypeMonitor.ID, Value: float64(60)},
		{ID: "cfg-k8s-res", Type: bleemeoTypes.ConfigTypeAgentMetricsResolution, Account: accountID, AgentType: agentTypeKubernetes.ID, Value: float64(60)},
		{ID: "cfg-vsphere-cluster-res", Type: bleemeoTypes.ConfigTypeAgentMetricsResolution, Account: accountID, AgentType: agentTypeVSphereCluster.ID, Value: float64(60)},
		{ID: "cfg-vsphere-host-res", Type: bleemeoTypes.ConfigTypeAgentMetricsResolution, Account: accountID, AgentType: agentTypeVSphereHost.ID, Value: float64(60)},
		{ID: "cfg-vsphere-vm-res", Type: bleemeoTypes.ConfigTypeAgentMetricsResolution, Account: accountID, AgentType: agentTypeVSphereVM.ID, Value: float64(60)},
	}

	testAgent = bleemeoapi.AgentPayload{
		Agent: bleemeoTypes.Agent{
			ID:          agentID,
			AccountID:   accountID,
			AgentType:   agentTypeAgent.ID,
			FQDN:        testAgentFQDN,
			DisplayName: testAgentFQDN,
		},
		Abstracted:      false,
		InitialPassword: passwordAlreadySet,
	}
	newMonitorAgent = bleemeoapi.AgentPayload{
		Agent: bleemeoTypes.Agent{
			ID:          "6b0ba586-0111-4a72-9cc7-f19d4f6558b9",
			AccountID:   accountID,
			AgentType:   agentTypeMonitor.ID,
			FQDN:        activeMonitorURL,
			DisplayName: activeMonitorURL,
		},
		Abstracted:      true,
		InitialPassword: passwordAlreadySet,
	}
	testK8SAgent = bleemeoapi.AgentPayload{
		Agent: bleemeoTypes.Agent{
			ID:          "efb48b0a-b03d-4ba6-b643-534e81a0acaa",
			AccountID:   accountID,
			AgentType:   agentTypeKubernetes.ID,
			FQDN:        testK8SClusterName,
			DisplayName: testK8SClusterName,
		},
		Abstracted:      false,
		InitialPassword: passwordAlreadySet,
	}

	newMonitor = bleemeoapi.ServicePayload{
		Monitor: bleemeoTypes.Monitor{
			Service: bleemeoTypes.Service{
				ID:           "fdd9d999-e2ff-45d3-af2b-6519cf8e3e70",
				Active:       true,
				Account:      accountID,
				CreationDate: "2020-01-03T04:05:06Z",
			},
			URL:     activeMonitorURL,
			AgentID: newMonitorAgent.ID,
		},
		IsMonitor: true,
	}

	testAgentMetric1 = bleemeoapi.MetricPayload{
		Metric: bleemeoTypes.Metric{
			ID:            "decce8cf-c2f7-43c3-b66e-10429debd994",
			AgentID:       testAgent.ID,
			LabelsText:    "__name__=\"some_metric_1\",label=\"value\"",
			DeactivatedAt: time.Time{},
			FirstSeenAt:   time.Unix(0, 0),
		},
		Name: "some_metric_1",
	}
	testAgentMetric2 = bleemeoapi.MetricPayload{
		Metric: bleemeoTypes.Metric{
			ID:          "055af752-5c01-4abc-9bb2-9d64032ef970",
			AgentID:     testAgent.ID,
			LabelsText:  "__name__=\"some_metric_2\",label=\"another_value !\"",
			FirstSeenAt: time.Unix(0, 0),
		},
		Name: "some_metric_2",
	}
	testMonitorMetricPrivateProbe = bleemeoapi.MetricPayload{
		Metric: bleemeoTypes.Metric{
			ID:      "52b9c46e-00b9-4e80-a852-781426a3a193",
			AgentID: newMonitor.AgentID,
			LabelsText: fmt.Sprintf(
				"__name__=\"probe_whatever\",instance=\"http://bleemeo.com\",scraper_uuid=\"%s\"",
				testAgent.ID,
			),
			ServiceID:   newMonitor.ID,
			FirstSeenAt: time.Unix(0, 0),
		},
		Name: "probe_whatever",
	}
)
