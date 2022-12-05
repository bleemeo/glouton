// Copyright 2015-2022 Bleemeo
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
	bleemeoTypes "glouton/bleemeo/types"
	"time"
)

const (
	// fixed "random" values are enought for tests.
	accountID          string = "9da59f53-1d90-4441-ae58-42c661cfea83"
	agentID            string = "33708da4-28d4-45aa-b811-49c82b594627"
	registrationKey    string = "e2c22e59-0621-49e6-b5dd-bdb02cbac9f1"
	containerID        string = "f21b2ac5-2173-42c2-a26a-db5ce53490cf"
	containerID2       string = "ce2ee1b5-6445-47a4-835e-9a001ec55c69"
	activeMonitorURL   string = "http://bleemeo.com"
	snmpAddress        string = "127.0.0.1"
	testAgentFQDN      string = "test.bleemeo.com"
	testK8SClusterName string = "k8s_demo"
)

// this is a go limitation, these are constants but we have to treat them as variables
//
//nolint:gochecknoglobals
var (
	newAccountConfig bleemeoTypes.AccountConfig = bleemeoTypes.AccountConfig{
		ID:                "02eb5b38-d4a0-4db4-9b43-06f63594a515",
		Name:              "the-default",
		SNMPIntegration:   true,
		DockerIntegration: true,
	}

	agentTypeAgent bleemeoTypes.AgentType = bleemeoTypes.AgentType{
		DisplayName: "A server monitored with Bleemeo agent",
		ID:          "61zb6a83-d90a-4165-bf04-944e0b2g2a10",
		Name:        bleemeoTypes.AgentTypeAgent,
	}
	agentTypeSNMP bleemeoTypes.AgentType = bleemeoTypes.AgentType{
		DisplayName: "A server monitored with SNMP agent",
		ID:          "823b6a83-d70a-4768-be64-50450b282a30",
		Name:        bleemeoTypes.AgentTypeSNMP,
	}
	agentTypeMonitor bleemeoTypes.AgentType = bleemeoTypes.AgentType{
		DisplayName: "A website monitored with connection check",
		ID:          "41afe63c-fa1c-4b84-b92b-028269101fde",
		Name:        bleemeoTypes.AgentTypeMonitor,
	}
	agentTypeKubernetes bleemeoTypes.AgentType = bleemeoTypes.AgentType{
		DisplayName: "k8s",
		ID:          "f8477dcd-36d8-489f-a6b8-e52f6bc013d2",
		Name:        bleemeoTypes.AgentTypeKubernetes,
	}

	agentConfigAgent = bleemeoTypes.AgentConfig{
		ID:               "cab64659-a765-4878-84d8-c7b0112aaecb",
		AccountConfig:    newAccountConfig.ID,
		AgentType:        agentTypeAgent.ID,
		MetricResolution: 10,
	}
	agentConfigSNMP = bleemeoTypes.AgentConfig{
		ID:               "a89d16c1-55be-4d89-9c9b-489c2d86d3fa",
		AccountConfig:    newAccountConfig.ID,
		AgentType:        agentTypeSNMP.ID,
		MetricResolution: 60,
	}
	agentConfigMonitor = bleemeoTypes.AgentConfig{
		ID:               "135aaa9d-5b73-4c38-b271-d3c98c039aef",
		AccountConfig:    newAccountConfig.ID,
		AgentType:        agentTypeMonitor.ID,
		MetricResolution: 60,
	}
	agentConfigKubernetes = bleemeoTypes.AgentConfig{
		ID:               "dcbd9b4f-8761-4363-8530-ca8d03570899",
		AccountConfig:    newAccountConfig.ID,
		AgentType:        agentTypeKubernetes.ID,
		MetricResolution: 60,
	}

	testAgent = payloadAgent{
		Agent: bleemeoTypes.Agent{
			ID:        agentID,
			AccountID: accountID,
			// same one as in newAccountConfig
			CurrentConfigID: newAccountConfig.ID,
			AgentType:       agentTypeAgent.ID,
			FQDN:            testAgentFQDN,
			DisplayName:     testAgentFQDN,
		},
		Abstracted:      false,
		InitialPassword: "password already set",
	}
	newMonitorAgent = payloadAgent{
		Agent: bleemeoTypes.Agent{
			ID:              "6b0ba586-0111-4a72-9cc7-f19d4f6558b9",
			AccountID:       accountID,
			CurrentConfigID: newAccountConfig.ID,
			AgentType:       agentTypeMonitor.ID,
			FQDN:            activeMonitorURL,
			DisplayName:     activeMonitorURL,
		},
		Abstracted:      true,
		InitialPassword: "password already set",
	}
	testK8SAgent = payloadAgent{
		Agent: bleemeoTypes.Agent{
			ID:              "efb48b0a-b03d-4ba6-b643-534e81a0acaa",
			AccountID:       accountID,
			CurrentConfigID: newAccountConfig.ID,
			AgentType:       agentTypeKubernetes.ID,
			FQDN:            testK8SClusterName,
			DisplayName:     testK8SClusterName,
		},
		Abstracted:      false,
		InitialPassword: "password already set",
	}

	newMonitor = serviceMonitor{
		Monitor: bleemeoTypes.Monitor{
			Service: bleemeoTypes.Service{
				ID:            "fdd9d999-e2ff-45d3-af2b-6519cf8e3e70",
				Active:        true,
				AccountConfig: newAccountConfig.ID,
				CreationDate:  "2020-01-03T04:05:06Z",
			},
			URL:     activeMonitorURL,
			AgentID: newMonitorAgent.ID,
		},
		IsMonitor: true,
	}

	testAgentMetric1 = metricPayload{
		Metric: bleemeoTypes.Metric{
			ID:            "decce8cf-c2f7-43c3-b66e-10429debd994",
			AgentID:       testAgent.ID,
			LabelsText:    "__name__=\"some_metric_1\",label=\"value\"",
			DeactivatedAt: time.Time{},
			FirstSeenAt:   time.Unix(0, 0),
		},
		Name: "some_metric_1",
	}
	testAgentMetric2 = metricPayload{
		Metric: bleemeoTypes.Metric{
			ID:          "055af752-5c01-4abc-9bb2-9d64032ef970",
			AgentID:     testAgent.ID,
			LabelsText:  "__name__=\"some_metric_2\",label=\"another_value !\"",
			FirstSeenAt: time.Unix(0, 0),
		},
		Name: "some_metric_2",
	}
	testMonitorMetricPrivateProbe = metricPayload{
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
