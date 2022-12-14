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
	"errors"
	"fmt"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/config"
	"glouton/discovery"
	"glouton/facts"
	"glouton/prometheus/exporter/snmp"
	"glouton/prometheus/model"
	"glouton/types"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/imdario/mergo"
	"github.com/prometheus/prometheus/model/labels"
)

var (
	errUnknownURLFormat    = errors.New("unknown URL format")
	errUnknownResource     = errors.New("unknown resource")
	errUnknownBool         = errors.New("unknown boolean")
	errUnknownRequestType  = errors.New("type of request unknown")
	errIncorrectID         = errors.New("incorrect id")
	errInvalidAccountID    = errors.New("invalid accountId supplied")
	errUnexpectedOperation = errors.New("unexpected action")
	errServerError         = errors.New("had server error")
	errClientError         = errors.New("had client error")
)

type serviceMonitor struct {
	bleemeoTypes.Monitor
	Account   string `json:"account"`
	IsMonitor bool   `json:"monitor"`
}

func TestSync(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.api.resources[mockAPIResourceMetric].AddStore(testAgentMetric1, testAgentMetric2, testMonitorMetricPrivateProbe)
	helper.api.resources[mockAPIResourceService].AddStore(newMonitor)

	agentResource, _ := helper.api.resources[mockAPIResourceAgent].(*genericResource)
	agentResource.CreateHook = func(r *http.Request, body []byte, valuePtr interface{}) error {
		return fmt.Errorf("%w: agent is already registered, shouldn't re-register", errUnexpectedOperation)
	}

	helper.initSynchronizer(t)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	// Did we store all the metrics ?
	syncedMetrics := helper.s.option.Cache.Metrics()
	want := []bleemeoTypes.Metric{
		testAgentMetric1.metricFromAPI(time.Time{}),
		testAgentMetric2.metricFromAPI(time.Time{}),
		testMonitorMetricPrivateProbe.metricFromAPI(time.Time{}),
		metricPayload{
			Metric: bleemeoTypes.Metric{
				ID:      "1",
				AgentID: testAgent.ID,
			},
			Name: "agent_status",
		}.metricFromAPI(time.Time{}),
		metricPayload{
			Metric: bleemeoTypes.Metric{
				ID:      "2",
				AgentID: testAgent.ID,
			},
			Name: "cpu_used",
		}.metricFromAPI(time.Time{}),
	}

	optMetricSort := cmpopts.SortSlices(func(x bleemeoTypes.Metric, y bleemeoTypes.Metric) bool { return x.ID < y.ID })
	if diff := cmp.Diff(want, syncedMetrics, optMetricSort); diff != "" {
		t.Errorf("metrics mistmatch (-want +got)\n%s", diff)
	}

	// Did we sync and enable the monitor present in the configuration ?
	syncedMonitors := helper.s.option.Cache.Monitors()
	wantMonitor := []bleemeoTypes.Monitor{
		newMonitor.Monitor,
	}

	if diff := cmp.Diff(wantMonitor, syncedMonitors); diff != "" {
		t.Errorf("monitors mistmatch (-want +got)\n%s", diff)
	}
}

func TestSyncWithSNMP(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.SNMP = []*snmp.Target{
		snmp.NewMock(config.SNMPTarget{InitialName: "Z-The-Initial-Name", Target: snmpAddress}, map[string]string{}),
	}
	helper.MetricFormat = types.MetricFormatPrometheus

	helper.initSynchronizer(t)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	var agents []payloadAgent

	helper.api.resources[mockAPIResourceAgent].Store(&agents)

	var (
		idAgentMain string
		idAgentSNMP string
	)

	for _, a := range agents {
		if a.FQDN == testAgentFQDN {
			idAgentMain = a.ID
		}

		if a.FQDN == snmpAddress {
			idAgentSNMP = a.ID
		}
	}

	wantAgents := []payloadAgent{
		{
			Agent: bleemeoTypes.Agent{
				ID:              idAgentMain,
				CreatedAt:       helper.api.now.Now(),
				AccountID:       accountID,
				CurrentConfigID: newAccountConfig.ID,
				AgentType:       agentTypeAgent.ID,
				FQDN:            testAgentFQDN,
				DisplayName:     testAgentFQDN,
			},
			Abstracted:      false,
			InitialPassword: "password already set",
		},
		{
			Agent: bleemeoTypes.Agent{
				ID:              idAgentSNMP,
				CreatedAt:       helper.api.now.Now(),
				AccountID:       accountID,
				CurrentConfigID: newAccountConfig.ID,
				AgentType:       agentTypeSNMP.ID,
				FQDN:            snmpAddress,
				DisplayName:     "Z-The-Initial-Name",
			},
			Abstracted:      true,
			InitialPassword: "password already set",
		},
	}

	optAgentSort := cmpopts.SortSlices(func(x payloadAgent, y payloadAgent) bool { return x.ID < y.ID })
	if diff := cmp.Diff(wantAgents, agents, cmpopts.EquateEmpty(), optAgentSort); diff != "" {
		t.Errorf("agents mismatch (-want +got)\n%s", diff)
	}

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
		labels.New(
			labels.Label{Name: types.LabelName, Value: "ifOutOctets"},
			labels.Label{Name: types.LabelSNMPTarget, Value: snmpAddress},
			labels.Label{Name: types.LabelMetaBleemeoTargetAgentUUID, Value: idAgentSNMP},
		),
	})

	helper.api.now.Advance(time.Second)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	var metrics []metricPayload

	helper.api.resources[mockAPIResourceMetric].Store(&metrics)

	wantMetrics := []metricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:      "1",
				AgentID: idAgentMain,
				LabelsText: fmt.Sprintf(
					`__name__="agent_status",instance_uuid="%s"`,
					idAgentMain,
				),
			},
			Name: "agent_status",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:      "2",
				AgentID: idAgentMain,
				LabelsText: fmt.Sprintf(
					`__name__="cpu_used",instance_uuid="%s"`,
					idAgentMain,
				),
			},
			Name: "cpu_used",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:         "3",
				AgentID:    idAgentSNMP,
				LabelsText: fmt.Sprintf(`__name__="ifOutOctets",snmp_target="%s"`, snmpAddress),
			},
			Name: "ifOutOctets",
		},
	}

	optMetricSort := cmpopts.SortSlices(func(x metricPayload, y metricPayload) bool { return x.ID < y.ID })
	if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
		t.Errorf("metrics mismatch (-want +got)\n%s", diff)
	}

	helper.api.now.Advance(10 * time.Second)

	helper.initSynchronizer(t)

	for n := 1; n <= 2; n++ {
		n := n
		t.Run(fmt.Sprintf("sub-run-%d", n), func(t *testing.T) {
			helper.api.now.Advance(time.Second)

			helper.pushPoints(t, []labels.Labels{
				labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
				labels.New(
					labels.Label{Name: types.LabelName, Value: "ifOutOctets"},
					labels.Label{Name: types.LabelSNMPTarget, Value: snmpAddress},
					labels.Label{Name: types.LabelMetaBleemeoTargetAgentUUID, Value: idAgentSNMP},
				),
			})

			if err := helper.runOnce(t); err != nil {
				t.Fatal(err)
			}

			helper.api.resources[mockAPIResourceMetric].Store(&metrics)

			if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
				t.Errorf("metrics mismatch (-want +got)\n%s", diff)
			}
		})
	}

	helper.api.resources[mockAPIResourceMetric].AddStore(metricPayload{
		Metric: bleemeoTypes.Metric{
			ID:         "4",
			AgentID:    idAgentSNMP,
			LabelsText: fmt.Sprintf(`__name__="ifInOctets",snmp_target="%s"`, snmpAddress),
		},
		Name: "ifInOctets",
	})

	helper.api.now.Advance(2 * time.Hour)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	helper.api.resources[mockAPIResourceMetric].Store(&metrics)

	wantMetrics = []metricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:      "1",
				AgentID: idAgentMain,
				LabelsText: fmt.Sprintf(
					`__name__="agent_status",instance_uuid="%s"`,
					idAgentMain,
				),
			},
			Name: "agent_status",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:      "2",
				AgentID: idAgentMain,
				LabelsText: fmt.Sprintf(
					`__name__="cpu_used",instance_uuid="%s"`,
					idAgentMain,
				),
				DeactivatedAt: helper.api.now.Now(),
			},
			Name: "cpu_used",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:            "3",
				AgentID:       idAgentSNMP,
				LabelsText:    fmt.Sprintf(`__name__="ifOutOctets",snmp_target="%s"`, snmpAddress),
				DeactivatedAt: helper.api.now.Now(),
			},
			Name: "ifOutOctets",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:            "4",
				AgentID:       idAgentSNMP,
				LabelsText:    fmt.Sprintf(`__name__="ifInOctets",snmp_target="%s"`, snmpAddress),
				DeactivatedAt: helper.api.now.Now(),
			},
			Name: "ifInOctets",
		},
	}

	if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
		t.Errorf("metrics mismatch (-want +got)\n%s", diff)
	}
}

func TestSyncWithSNMPDelete(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	var (
		updateLabelsCallCount int
		l                     sync.Mutex
	)

	helper.SNMP = []*snmp.Target{
		snmp.NewMock(config.SNMPTarget{InitialName: "Z-The-Initial-Name", Target: snmpAddress}, map[string]string{}),
	}
	helper.MetricFormat = types.MetricFormatPrometheus
	helper.NotifyLabelsUpdate = func() {
		l.Lock()
		defer l.Unlock()

		updateLabelsCallCount++
	}

	helper.initSynchronizer(t)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	var agents []payloadAgent

	helper.api.resources[mockAPIResourceAgent].Store(&agents)

	var (
		idAgentMain string
		idAgentSNMP string
	)

	for _, a := range agents {
		if a.FQDN == testAgentFQDN {
			idAgentMain = a.ID
		}

		if a.FQDN == snmpAddress {
			idAgentSNMP = a.ID
		}
	}

	wantAgents := []payloadAgent{
		{
			Agent: bleemeoTypes.Agent{
				ID:              idAgentMain,
				CreatedAt:       helper.api.now.Now(),
				AccountID:       accountID,
				CurrentConfigID: newAccountConfig.ID,
				AgentType:       agentTypeAgent.ID,
				FQDN:            testAgentFQDN,
				DisplayName:     testAgentFQDN,
			},
			Abstracted:      false,
			InitialPassword: "password already set",
		},
		{
			Agent: bleemeoTypes.Agent{
				ID:              idAgentSNMP,
				CreatedAt:       helper.api.now.Now(),
				AccountID:       accountID,
				CurrentConfigID: newAccountConfig.ID,
				AgentType:       agentTypeSNMP.ID,
				FQDN:            snmpAddress,
				DisplayName:     "Z-The-Initial-Name",
			},
			Abstracted:      true,
			InitialPassword: "password already set",
		},
	}

	optAgentSort := cmpopts.SortSlices(func(x payloadAgent, y payloadAgent) bool { return x.ID < y.ID })
	if diff := cmp.Diff(wantAgents, agents, cmpopts.EquateEmpty(), optAgentSort); diff != "" {
		t.Errorf("agents mismatch (-want +got)\n%s", diff)
	}

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
		labels.New(
			labels.Label{Name: types.LabelName, Value: "ifOutOctets"},
			labels.Label{Name: types.LabelSNMPTarget, Value: snmpAddress},
			labels.Label{Name: types.LabelMetaBleemeoTargetAgentUUID, Value: idAgentSNMP},
		),
	})

	helper.api.now.Advance(time.Second)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	var metrics []metricPayload

	helper.api.resources[mockAPIResourceMetric].Store(&metrics)

	wantMetrics := []metricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:      "1",
				AgentID: idAgentMain,
				LabelsText: fmt.Sprintf(
					`__name__="agent_status",instance_uuid="%s"`,
					idAgentMain,
				),
			},
			Name: "agent_status",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:      "2",
				AgentID: idAgentMain,
				LabelsText: fmt.Sprintf(
					`__name__="cpu_used",instance_uuid="%s"`,
					idAgentMain,
				),
			},
			Name: "cpu_used",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:         "3",
				AgentID:    idAgentSNMP,
				LabelsText: fmt.Sprintf(`__name__="ifOutOctets",snmp_target="%s"`, snmpAddress),
			},
			Name: "ifOutOctets",
		},
	}

	optMetricSort := cmpopts.SortSlices(func(x metricPayload, y metricPayload) bool { return x.ID < y.ID })
	if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
		t.Errorf("metrics mismatch (-want +got)\n%s", diff)
	}

	helper.api.now.Advance(10 * time.Second)

	// Delete the SNMP agent on API.
	callCountBefore := updateLabelsCallCount

	helper.api.resources[mockAPIResourceAgent].DelStore(idAgentSNMP)
	helper.api.resources[mockAPIResourceMetric].DelStore("3")

	helper.initSynchronizer(t)

	helper.api.now.Advance(time.Second)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	helper.api.resources[mockAPIResourceAgent].Store(&agents)

	for _, a := range agents {
		if a.FQDN == snmpAddress {
			idAgentSNMP = a.ID
		}
	}

	wantAgents = []payloadAgent{
		wantAgents[0],
		{
			Agent: bleemeoTypes.Agent{
				ID:              idAgentSNMP,
				CreatedAt:       helper.api.now.Now(),
				AccountID:       accountID,
				CurrentConfigID: newAccountConfig.ID,
				AgentType:       agentTypeSNMP.ID,
				FQDN:            snmpAddress,
				DisplayName:     "Z-The-Initial-Name",
			},
			Abstracted:      true,
			InitialPassword: "password already set",
		},
	}

	if diff := cmp.Diff(wantAgents, agents, cmpopts.EquateEmpty(), optAgentSort); diff != "" {
		t.Errorf("agents mismatch (-want +got)\n%s", diff)
	}

	helper.api.now.Advance(time.Second)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
		labels.New(
			labels.Label{Name: types.LabelName, Value: "ifOutOctets"},
			labels.Label{Name: types.LabelSNMPTarget, Value: snmpAddress},
			labels.Label{Name: types.LabelMetaBleemeoTargetAgentUUID, Value: idAgentSNMP},
		),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	wantMetrics[2] = metricPayload{
		Metric: bleemeoTypes.Metric{
			ID:         "4",
			AgentID:    idAgentSNMP,
			LabelsText: fmt.Sprintf(`__name__="ifOutOctets",snmp_target="%s"`, snmpAddress),
		},
		Name: "ifOutOctets",
	}

	helper.api.resources[mockAPIResourceMetric].Store(&metrics)

	if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
		t.Errorf("metrics mismatch (-want +got)\n%s", diff)
	}

	if callCountBefore == updateLabelsCallCount {
		t.Errorf("updateLabelsCallCount = %d, want > %d", updateLabelsCallCount, callCountBefore)
	}
}

// TestContainerSync will create a container with one metric. Delete the container. And finally re-created it with the metric.
func TestContainerSync(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)

	helper.containers = []facts.Container{
		facts.FakeContainer{
			FakeContainerName: "my_redis_1",
			FakeState:         facts.ContainerRunning,
			FakeID:            containerID,
		},
	}

	helper.initSynchronizer(t)

	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: types.LabelName, Value: types.MetricServiceStatus},
			labels.Label{Name: types.LabelService, Value: "redis"},
			labels.Label{Name: types.LabelServiceInstance, Value: "my_redis_1"},
			labels.Label{Name: types.LabelMetaContainerID, Value: containerID},
		),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	// Did we store container & metrics?
	var containers []containerPayload

	helper.api.resources[mockAPIResourceContainer].Store(&containers)

	wantContainer := []containerPayload{
		{
			Container: bleemeoTypes.Container{
				ID:          "1",
				ContainerID: containerID,
				Status:      "running",
				Runtime:     "fake",
				Name:        "my_redis_1",
			},
			Host: testAgent.ID,
		},
	}

	if diff := cmp.Diff(wantContainer, containers); diff != "" {
		t.Errorf("container mistmatch (-want +got)\n%s", diff)
	}

	var metrics []metricPayload

	helper.api.resources[mockAPIResourceMetric].Store(&metrics)

	wantMetrics := []metricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:         "1",
				AgentID:    testAgent.ID,
				LabelsText: "",
			},
			Name: "agent_status",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:          "2",
				AgentID:     testAgent.ID,
				LabelsText:  `__name__="service_status",service="redis",service_instance="my_redis_1"`,
				ContainerID: "1",
			},
			Name: types.MetricServiceStatus,
			Item: "",
		},
	}

	optMetricSort := cmpopts.SortSlices(func(x metricPayload, y metricPayload) bool { return x.ID < y.ID })
	if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
		t.Errorf("metrics mismatch (-want +got)\n%s", diff)
	}

	helper.api.now.Advance(time.Minute)
	helper.containers = []facts.Container{}
	helper.discovery.UpdatedAt = helper.s.now()

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	helper.api.resources[mockAPIResourceContainer].Store(&containers)

	wantContainer = []containerPayload{
		{
			Container: bleemeoTypes.Container{
				ID:          "1",
				ContainerID: containerID,
				Status:      "running",
				Runtime:     "fake",
				Name:        "my_redis_1",
				DeletedAt:   bleemeoTypes.NullTime(helper.s.now()),
			},
			Host: testAgent.ID,
		},
	}

	if diff := cmp.Diff(wantContainer, containers); diff != "" {
		t.Errorf("container mistmatch (-want +got)\n%s", diff)
	}

	helper.api.resources[mockAPIResourceMetric].Store(&metrics)

	wantMetrics = []metricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:         "1",
				AgentID:    testAgent.ID,
				LabelsText: "",
			},
			Name: "agent_status",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:            "2",
				AgentID:       testAgent.ID,
				LabelsText:    `__name__="service_status",service="redis",service_instance="my_redis_1"`,
				ContainerID:   "1",
				DeactivatedAt: helper.s.now(),
			},
			Name: types.MetricServiceStatus,
			Item: "",
		},
	}

	if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
		t.Errorf("metrics mismatch (-want +got)\n%s", diff)
	}

	helper.api.now.Advance(2 * time.Hour)
	helper.containers = []facts.Container{
		facts.FakeContainer{
			FakeContainerName: "my_redis_1",
			FakeState:         facts.ContainerRunning,
			FakeID:            containerID2,
		},
	}
	helper.discovery.UpdatedAt = helper.s.now()

	helper.pushPoints(t, []labels.Labels{
		labels.New(
			labels.Label{Name: types.LabelName, Value: types.MetricServiceStatus},
			labels.Label{Name: types.LabelService, Value: "redis"},
			labels.Label{Name: types.LabelServiceInstance, Value: "my_redis_1"},
			labels.Label{Name: types.LabelMetaContainerID, Value: containerID2},
		),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	helper.api.resources[mockAPIResourceContainer].Store(&containers)

	wantContainer = []containerPayload{
		{
			Container: bleemeoTypes.Container{
				ID:          "1",
				ContainerID: containerID2,
				Status:      "running",
				Runtime:     "fake",
				Name:        "my_redis_1",
			},
			Host: testAgent.ID,
		},
	}

	if diff := cmp.Diff(wantContainer, containers); diff != "" {
		t.Errorf("container mistmatch (-want +got)\n%s", diff)
	}

	helper.api.resources[mockAPIResourceMetric].Store(&metrics)

	wantMetrics = []metricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:         "1",
				AgentID:    testAgent.ID,
				LabelsText: "",
			},
			Name: "agent_status",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:          "2",
				AgentID:     testAgent.ID,
				LabelsText:  `__name__="service_status",service="redis",service_instance="my_redis_1"`,
				ContainerID: "1",
			},
			Name: types.MetricServiceStatus,
			Item: "",
		},
	}

	if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
		t.Errorf("metrics mismatch (-want +got)\n%s", diff)
	}
}

func TestSyncServerGroup(t *testing.T) {
	tests := []struct {
		name                  string
		cfg                   config.Config
		wantGroupForMainAgent string
		wantGroupForSNMPAgent string
	}{
		{
			name:                  "no config",
			cfg:                   config.Config{},
			wantGroupForMainAgent: "",
			wantGroupForSNMPAgent: "",
		},
		{
			name: "both set",
			cfg: config.Config{
				Bleemeo: config.Bleemeo{
					InitialServerGroupName:        "group1",
					InitialServerGroupNameForSNMP: "group2",
				},
			},
			wantGroupForMainAgent: "group1",
			wantGroupForSNMPAgent: "group2",
		},
		{
			name: "only main set",
			cfg: config.Config{
				Bleemeo: config.Bleemeo{
					InitialServerGroupName: "group3",
				},
			},
			wantGroupForMainAgent: "group3",
			wantGroupForSNMPAgent: "group3",
		},
		{
			name: "only SNMP set",
			cfg: config.Config{
				Bleemeo: config.Bleemeo{
					InitialServerGroupNameForSNMP: "group4",
				},
			},
			wantGroupForMainAgent: "",
			wantGroupForSNMPAgent: "group4",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			helper := newHelper(t)
			defer helper.Close()

			err := mergo.Merge(&helper.cfg, tt.cfg)
			if err != nil {
				t.Fatalf("Failed to merge configs: %s", err)
			}

			helper.SNMP = []*snmp.Target{
				snmp.NewMock(config.SNMPTarget{InitialName: "Z-The-Initial-Name", Target: snmpAddress}, map[string]string{}),
			}

			helper.initSynchronizer(t)

			helper.pushPoints(t, []labels.Labels{
				labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
			})

			if err := helper.runOnce(t); err != nil {
				t.Fatal(err)
			}

			var agents []payloadAgent

			helper.api.resources[mockAPIResourceAgent].Store(&agents)

			var (
				idAgentMain string
				idAgentSNMP string
			)

			for _, a := range agents {
				if a.FQDN == testAgentFQDN {
					idAgentMain = a.ID
				}

				if a.FQDN == snmpAddress {
					idAgentSNMP = a.ID
				}
			}

			wantAgents := []payloadAgent{
				{
					Agent: bleemeoTypes.Agent{
						ID:              idAgentMain,
						CreatedAt:       helper.api.now.Now(),
						AccountID:       accountID,
						CurrentConfigID: newAccountConfig.ID,
						AgentType:       agentTypeAgent.ID,
						FQDN:            testAgentFQDN,
						DisplayName:     testAgentFQDN,
					},
					Abstracted:         false,
					InitialPassword:    "password already set",
					InitialServerGroup: tt.wantGroupForMainAgent,
				},
				{
					Agent: bleemeoTypes.Agent{
						ID:              idAgentSNMP,
						CreatedAt:       helper.api.now.Now(),
						AccountID:       accountID,
						CurrentConfigID: newAccountConfig.ID,
						AgentType:       agentTypeSNMP.ID,
						FQDN:            snmpAddress,
						DisplayName:     "Z-The-Initial-Name",
					},
					Abstracted:         true,
					InitialPassword:    "password already set",
					InitialServerGroup: tt.wantGroupForSNMPAgent,
				},
			}

			optAgentSort := cmpopts.SortSlices(func(x payloadAgent, y payloadAgent) bool { return x.ID < y.ID })
			if diff := cmp.Diff(wantAgents, agents, cmpopts.EquateEmpty(), optAgentSort); diff != "" {
				t.Errorf("agents mismatch (-want +got)\n%s", diff)
			}
		})
	}
}

// TestBleemeoPlan ensure Glouton works as expected in various plan.
func TestBleemeoPlan(t *testing.T) { //nolint:maintidx
	const (
		configID1      = "86c0b76c-587a-48c6-a799-dd753719151c"
		configID2      = "a0740441-88ee-4ec1-b023-b9d3e9398cf7"
		agentConfigID1 = "62fe90d2-00cc-4b81-b49d-308de2ae22e1"
		agentConfigID2 = "f520e10b-7718-4fb1-87d9-b1f65f90ca31"
		agentConfigID3 = "81620241-3318-4e3b-8b33-70ff40c9e31a"
		agentConfigID4 = "96110f90-b724-473f-8077-3244661cb68d"
	)

	cases := []struct {
		name                string
		accountConfig       bleemeoTypes.AccountConfig
		agentConfigs        []bleemeoTypes.AgentConfig
		wantSNMP            bool
		wantCustomMetric    bool
		wantContainerFK     bool
		wantContainerMetric bool
	}{
		{
			name:          "default",
			accountConfig: newAccountConfig,
			agentConfigs: []bleemeoTypes.AgentConfig{
				agentConfigAgent,
				agentConfigSNMP,
				agentConfigMonitor,
			},
			wantSNMP:            true,
			wantCustomMetric:    true,
			wantContainerFK:     true,
			wantContainerMetric: true,
		},
		{
			name: "all-enable",
			accountConfig: bleemeoTypes.AccountConfig{
				ID:                configID1,
				Name:              "all-enable",
				MaxCustomMetrics:  999,
				LiveProcess:       true,
				DockerIntegration: true,
				SNMPIntegration:   true,
			},
			agentConfigs: []bleemeoTypes.AgentConfig{
				{
					ID:            agentConfigID1,
					AccountConfig: configID1,
					AgentType:     agentTypeAgent.ID,
				},
				{
					ID:            agentConfigID2,
					AccountConfig: configID1,
					AgentType:     agentTypeSNMP.ID,
				},
				{
					ID:            agentConfigID3,
					AccountConfig: configID1,
					AgentType:     agentTypeMonitor.ID,
				},
			},
			wantSNMP:            true,
			wantCustomMetric:    true,
			wantContainerFK:     true,
			wantContainerMetric: true,
		},
		{
			name: "no-docker",
			accountConfig: bleemeoTypes.AccountConfig{
				ID:                configID2,
				Name:              "no-docker",
				MaxCustomMetrics:  999,
				LiveProcess:       true,
				DockerIntegration: false,
				SNMPIntegration:   true,
			},
			agentConfigs: []bleemeoTypes.AgentConfig{
				{
					ID:            agentConfigID1,
					AccountConfig: configID2,
					AgentType:     agentTypeAgent.ID,
				},
				{
					ID:            agentConfigID2,
					AccountConfig: configID2,
					AgentType:     agentTypeSNMP.ID,
				},
				{
					ID:            agentConfigID3,
					AccountConfig: configID2,
					AgentType:     agentTypeMonitor.ID,
				},
			},
			wantSNMP:            true,
			wantCustomMetric:    true,
			wantContainerFK:     false,
			wantContainerMetric: false,
		},
		{
			name: "no-docker-limit-list",
			accountConfig: bleemeoTypes.AccountConfig{
				ID:                    configID2,
				Name:                  "no-docker-limit-list",
				MaxCustomMetrics:      999,
				LiveProcess:           true,
				DockerIntegration:     false,
				SNMPIntegration:       true,
				MetricsAgentWhitelist: "this isn't used",
			},
			agentConfigs: []bleemeoTypes.AgentConfig{
				{
					ID:               agentConfigID1,
					AccountConfig:    configID2,
					AgentType:        agentTypeAgent.ID,
					MetricsAllowlist: "mem_used,cpu_used,probe_success,service_status",
				},
				{
					ID:            agentConfigID2,
					AccountConfig: configID2,
					AgentType:     agentTypeSNMP.ID,
				},
				{
					ID:            agentConfigID3,
					AccountConfig: configID2,
					AgentType:     agentTypeMonitor.ID,
				},
			},
			wantSNMP:            true,
			wantCustomMetric:    false,
			wantContainerFK:     false,
			wantContainerMetric: false,
		},
		{
			name: "no-docker-no-snmp-limit-list",
			accountConfig: bleemeoTypes.AccountConfig{
				ID:                    configID2,
				Name:                  "no-no-no",
				MaxCustomMetrics:      999,
				LiveProcess:           false,
				DockerIntegration:     false,
				SNMPIntegration:       false,
				MetricsAgentWhitelist: "still-not-used",
			},
			agentConfigs: []bleemeoTypes.AgentConfig{
				{
					ID:               agentConfigID1,
					AccountConfig:    configID2,
					AgentType:        agentTypeAgent.ID,
					MetricsAllowlist: "mem_used,cpu_used,probe_success,service_status",
				},
				{
					ID:            agentConfigID3,
					AccountConfig: configID2,
					AgentType:     agentTypeMonitor.ID,
				},
			},
			wantSNMP:            false,
			wantCustomMetric:    false,
			wantContainerFK:     false,
			wantContainerMetric: false,
		},
	}

	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			helper := newHelper(t)
			defer helper.Close()

			helper.SetAPIAccountConfig(tt.accountConfig, tt.agentConfigs)
			helper.SNMP = []*snmp.Target{
				snmp.NewMock(config.SNMPTarget{InitialName: "The-Initial-Name", Target: snmpAddress}, map[string]string{}),
			}
			helper.containers = []facts.Container{
				facts.FakeContainer{
					FakeContainerName: "short-redis-container-name",
					FakeState:         facts.ContainerRunning,
					FakeID:            "1234",
				},
			}

			helper.initSynchronizer(t)
			monitor := helper.addMonitorOnAPI(t)

			srvRedis1 := discovery.Service{
				Name:        "redis",
				Instance:    "short-redis-container-name",
				ServiceType: discovery.RedisService,
				ContainerID: "1234",
				Active:      true,
			}

			helper.discovery.SetResult([]discovery.Service{srvRedis1}, nil)

			if err := helper.runOnceWithResult(t).Check(); err != nil {
				t.Error(err)
			}

			// two run, because very first is "onlyEssential"
			if err := helper.runOnceWithResult(t).Check(); err != nil {
				t.Error(err)
			}

			idAgentMain, _ := helper.state.BleemeoCredentials()
			if idAgentMain == "" {
				t.Fatal("idAgentMain == '', want something")
			}

			var idAgentSNMP string

			for _, a := range helper.AgentsFromAPI() {
				if a.FQDN == snmpAddress {
					idAgentSNMP = a.ID
				}
			}

			helper.pushPoints(t, []labels.Labels{
				labels.New(labels.Label{Name: types.LabelName, Value: "cpu_used"}),
				labels.New(labels.Label{Name: types.LabelName, Value: "custom_metric"}),
				model.AnnotationToMetaLabels(labels.FromMap(srvRedis1.LabelsOfStatus()), srvRedis1.AnnotationsOfStatus()),
				labels.New(
					labels.Label{Name: types.LabelName, Value: "probe_success"},
					labels.Label{Name: types.LabelScraperUUID, Value: idAgentMain},
					labels.Label{Name: types.LabelInstance, Value: newMonitor.URL},
					labels.Label{Name: types.LabelInstanceUUID, Value: newMonitor.AgentID},
					labels.Label{Name: types.LabelMetaBleemeoTargetAgentUUID, Value: newMonitor.AgentID},
				),
				labels.New(
					labels.Label{Name: types.LabelName, Value: "ifOutOctets"},
					labels.Label{Name: types.LabelSNMPTarget, Value: snmpAddress},
					labels.Label{Name: types.LabelMetaBleemeoTargetAgentUUID, Value: idAgentSNMP},
				),
				labels.New(
					labels.Label{Name: types.LabelName, Value: "redis_commands"},
					labels.Label{Name: types.LabelMetaServiceName, Value: "redis"},
					labels.Label{Name: types.LabelMetaServiceInstance, Value: "short-redis-container-name"},
					labels.Label{Name: types.LabelMetaContainerID, Value: "1234"},
					labels.Label{Name: types.LabelMetaBleemeoItem, Value: "short-redis-container-name"},
					labels.Label{Name: types.LabelItem, Value: "short-redis-container-name"},
				),
			})

			if err := helper.runOnceWithResult(t).Check(); err != nil {
				t.Error(err)
			}

			wantAgents := []payloadAgent{
				{
					Agent: bleemeoTypes.Agent{
						ID:              idAgentMain,
						CreatedAt:       helper.Now(),
						AccountID:       accountID,
						CurrentConfigID: tt.accountConfig.ID,
						AgentType:       agentTypeAgent.ID,
						FQDN:            testAgentFQDN,
						DisplayName:     testAgentFQDN,
					},
					Abstracted:      false,
					InitialPassword: "password already set",
				},
			}

			if tt.wantSNMP {
				wantAgents = append(wantAgents, payloadAgent{
					Agent: bleemeoTypes.Agent{
						ID:              idAny,
						CreatedAt:       helper.Now(),
						AccountID:       accountID,
						CurrentConfigID: tt.accountConfig.ID,
						AgentType:       agentTypeSNMP.ID,
						FQDN:            snmpAddress,
						DisplayName:     "The-Initial-Name",
					},
					Abstracted:      true,
					InitialPassword: "password already set",
				})
			}

			helper.assertAgentsInAPI(t, wantAgents)

			helper.assertServicesInAPI(t, []serviceMonitor{
				monitor,
				{
					Account: accountID,
					Monitor: bleemeoTypes.Monitor{
						Service: bleemeoTypes.Service{
							ID:       idAny,
							Label:    "redis",
							Instance: "short-redis-container-name",
							Active:   true,
						},
						AgentID: idAgentMain,
					},
				},
			})

			wantMetrics := []metricPayload{
				{
					Metric: bleemeoTypes.Metric{
						ID:      idAny,
						AgentID: idAgentMain,
					},
					Name: "agent_status",
				},
				{
					Metric: bleemeoTypes.Metric{
						ID:      idAny,
						AgentID: idAgentMain,
					},
					Name: "cpu_used",
				},
				{
					Metric: bleemeoTypes.Metric{
						ID:      idAny,
						AgentID: newMonitor.AgentID,
						LabelsText: fmt.Sprintf(
							"__name__=\"probe_success\",instance=\"%s\",instance_uuid=\"%s\",scraper_uuid=\"%s\"",
							newMonitor.URL,
							newMonitor.AgentID,
							idAgentMain,
						),
						ServiceID: newMonitor.ID,
					},
					Name: "probe_success",
				},
			}

			redisMetric := metricPayload{
				Metric: bleemeoTypes.Metric{
					ID:          idAny,
					AgentID:     idAgentMain,
					LabelsText:  `__name__="service_status",service="redis",service_instance="short-redis-container-name"`,
					ServiceID:   "1",
					ContainerID: "",
				},
				Name: "service_status",
				Item: "",
			}

			if tt.wantContainerFK {
				redisMetric.ContainerID = "1"
			}

			wantMetrics = append(wantMetrics, redisMetric)

			if tt.wantContainerMetric {
				wantMetrics = append(wantMetrics, metricPayload{
					Metric: bleemeoTypes.Metric{
						ID:          idAny,
						AgentID:     idAgentMain,
						ServiceID:   "1",
						ContainerID: "1",
					},
					Name: "redis_commands",
					Item: "short-redis-container-name",
				})
			}

			if tt.wantCustomMetric {
				wantMetrics = append(wantMetrics, metricPayload{
					Metric: bleemeoTypes.Metric{
						ID:      idAny,
						AgentID: idAgentMain,
					},
					Name: "custom_metric",
				})
			}

			if tt.wantSNMP {
				wantMetrics = append(wantMetrics, metricPayload{
					Metric: bleemeoTypes.Metric{
						ID:         idAny,
						AgentID:    idAgentSNMP,
						LabelsText: fmt.Sprintf(`__name__="ifOutOctets",snmp_target="%s"`, snmpAddress),
					},
					Name: "ifOutOctets",
				})
			}

			helper.assertMetricsInAPI(t, wantMetrics)
		})
	}
}
