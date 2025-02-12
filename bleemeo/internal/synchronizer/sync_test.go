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
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/prometheus/exporter/snmp"
	"github.com/bleemeo/glouton/prometheus/model"
	gloutonTypes "github.com/bleemeo/glouton/types"

	"dario.cat/mergo"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"
	"github.com/prometheus/prometheus/model/labels"
)

var (
	errInvalidAccountID    = errors.New("invalid accountId supplied")
	errUnexpectedOperation = errors.New("unexpected action")
	errClientError         = errors.New("had client error")
)

func TestSync(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.wrapperClientMock.resources.metrics.add(testAgentMetric1, testAgentMetric2, testMonitorMetricPrivateProbe)
	helper.wrapperClientMock.resources.monitors.add(newMonitor.Monitor)

	helper.wrapperClientMock.resources.agents.createHook = func(*bleemeoapi.AgentPayload) error {
		return fmt.Errorf("%w: agent is already registered, shouldn't re-register", errUnexpectedOperation)
	}

	helper.initSynchronizer(t)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: "cpu_used"}),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	// Did we store all the metrics ?
	syncedMetrics := helper.s.option.Cache.Metrics()
	want := []bleemeoTypes.Metric{
		metricFromAPI(testAgentMetric1, time.Time{}),
		metricFromAPI(testAgentMetric2, time.Time{}),
		metricFromAPI(testMonitorMetricPrivateProbe, time.Time{}),
		metricFromAPI(bleemeoapi.MetricPayload{
			Metric: bleemeoTypes.Metric{
				ID:      "1",
				AgentID: testAgent.ID,
			},
			Name: "agent_status",
		}, time.Time{}),
		metricFromAPI(bleemeoapi.MetricPayload{
			Metric: bleemeoTypes.Metric{
				ID:      "2",
				AgentID: testAgent.ID,
			},
			Name: "cpu_used",
		}, time.Time{}),
	}

	optMetricSort := cmpopts.SortSlices(func(x bleemeoTypes.Metric, y bleemeoTypes.Metric) bool { return x.ID < y.ID })
	if diff := cmp.Diff(want, syncedMetrics, optMetricSort); diff != "" {
		t.Errorf("metrics mismatch (-want +got)\n%s", diff)
	}

	// Did we sync and enable the monitor present in the configuration ?
	syncedMonitors := helper.s.option.Cache.Monitors()
	wantMonitor := []bleemeoTypes.Monitor{
		newMonitor.Monitor,
	}

	if diff := cmp.Diff(wantMonitor, syncedMonitors); diff != "" {
		t.Errorf("monitors mismatch (-want +got)\n%s", diff)
	}
}

func TestSyncWithSNMP(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.SNMP = []*snmp.Target{
		snmp.NewMock(config.SNMPTarget{InitialName: "Z-The-Initial-Name", Target: snmpAddress}, map[string]string{}),
	}

	helper.initSynchronizer(t)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: "cpu_used"}),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	var (
		idAgentMain string
		idAgentSNMP string
	)

	agents := helper.wrapperClientMock.resources.agents.clone()
	for _, a := range agents {
		if a.FQDN == testAgentFQDN {
			idAgentMain = a.ID
		}

		if a.FQDN == snmpAddress {
			idAgentSNMP = a.ID
		}
	}

	wantAgents := []bleemeoapi.AgentPayload{
		{
			Agent: bleemeoTypes.Agent{
				ID:                     idAgentMain,
				CreatedAt:              helper.Now(),
				AccountID:              accountID,
				CurrentAccountConfigID: newAccountConfig.ID,
				AgentType:              agentTypeAgent.ID,
				FQDN:                   testAgentFQDN,
				DisplayName:            testAgentFQDN,
			},
			Abstracted:      false,
			InitialPassword: "password already set",
		},
		{
			Agent: bleemeoTypes.Agent{
				ID:                     idAgentSNMP,
				CreatedAt:              helper.Now(),
				AccountID:              accountID,
				CurrentAccountConfigID: newAccountConfig.ID,
				AgentType:              agentTypeSNMP.ID,
				FQDN:                   snmpAddress,
				DisplayName:            "Z-The-Initial-Name",
			},
			Abstracted:      true,
			InitialPassword: "password already set",
		},
	}

	optAgentSort := cmpopts.SortSlices(func(x, y bleemeoapi.AgentPayload) bool { return x.ID < y.ID })
	if diff := cmp.Diff(wantAgents, agents, cmpopts.EquateEmpty(), optAgentSort); diff != "" {
		t.Errorf("agents mismatch (-want +got)\n%s", diff)
	}

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: "cpu_used"}),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "ifOutOctets"},
			labels.Label{Name: gloutonTypes.LabelSNMPTarget, Value: snmpAddress},
			labels.Label{Name: gloutonTypes.LabelMetaBleemeoTargetAgentUUID, Value: idAgentSNMP},
		),
	})

	helper.AddTime(time.Second)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	var metrics []bleemeoapi.MetricPayload

	metrics = helper.wrapperClientMock.resources.metrics.clone()

	wantMetrics := []bleemeoapi.MetricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:      "1",
				AgentID: idAgentMain,
			},
			Name: "agent_status",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:      "2",
				AgentID: idAgentMain,
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

	optMetricSort := cmpopts.SortSlices(func(x, y bleemeoapi.MetricPayload) bool { return x.ID < y.ID })
	if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
		t.Errorf("metrics mismatch (-want +got)\n%s", diff)
	}

	helper.AddTime(10 * time.Second)

	helper.initSynchronizer(t)

	for n := 1; n <= 2; n++ {
		t.Run(fmt.Sprintf("sub-run-%d", n), func(t *testing.T) {
			helper.AddTime(time.Second)

			helper.pushPoints(t, []labels.Labels{
				labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: "cpu_used"}),
				labels.New(
					labels.Label{Name: gloutonTypes.LabelName, Value: "ifOutOctets"},
					labels.Label{Name: gloutonTypes.LabelSNMPTarget, Value: snmpAddress},
					labels.Label{Name: gloutonTypes.LabelMetaBleemeoTargetAgentUUID, Value: idAgentSNMP},
				),
			})

			if err := helper.runOnce(t); err != nil {
				t.Fatal(err)
			}

			metrics = helper.wrapperClientMock.resources.metrics.clone()

			if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
				t.Errorf("metrics mismatch (-want +got)\n%s", diff)
			}
		})
	}

	helper.wrapperClientMock.resources.metrics.add(bleemeoapi.MetricPayload{
		Metric: bleemeoTypes.Metric{
			ID:         "4",
			AgentID:    idAgentSNMP,
			LabelsText: fmt.Sprintf(`__name__="ifInOctets",snmp_target="%s"`, snmpAddress),
		},
		Name: "ifInOctets",
	})

	helper.AddTime(2 * time.Hour)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	metrics = helper.wrapperClientMock.resources.metrics.clone()

	wantMetrics = []bleemeoapi.MetricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:      "1",
				AgentID: idAgentMain,
			},
			Name: "agent_status",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:            "2",
				AgentID:       idAgentMain,
				DeactivatedAt: helper.Now(),
			},
			Name: "cpu_used",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:            "3",
				AgentID:       idAgentSNMP,
				LabelsText:    fmt.Sprintf(`__name__="ifOutOctets",snmp_target="%s"`, snmpAddress),
				DeactivatedAt: helper.Now(),
			},
			Name: "ifOutOctets",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:            "4",
				AgentID:       idAgentSNMP,
				LabelsText:    fmt.Sprintf(`__name__="ifInOctets",snmp_target="%s"`, snmpAddress),
				DeactivatedAt: helper.Now(),
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
	helper.NotifyLabelsUpdate = func() {
		l.Lock()
		defer l.Unlock()

		updateLabelsCallCount++
	}

	helper.initSynchronizer(t)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: "cpu_used"}),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	var agents []bleemeoapi.AgentPayload

	agents = helper.wrapperClientMock.resources.agents.clone()

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

	wantAgents := []bleemeoapi.AgentPayload{
		{
			Agent: bleemeoTypes.Agent{
				ID:                     idAgentMain,
				CreatedAt:              helper.Now(),
				AccountID:              accountID,
				CurrentAccountConfigID: newAccountConfig.ID,
				AgentType:              agentTypeAgent.ID,
				FQDN:                   testAgentFQDN,
				DisplayName:            testAgentFQDN,
			},
			Abstracted:      false,
			InitialPassword: "password already set",
		},
		{
			Agent: bleemeoTypes.Agent{
				ID:                     idAgentSNMP,
				CreatedAt:              helper.Now(),
				AccountID:              accountID,
				CurrentAccountConfigID: newAccountConfig.ID,
				AgentType:              agentTypeSNMP.ID,
				FQDN:                   snmpAddress,
				DisplayName:            "Z-The-Initial-Name",
			},
			Abstracted:      true,
			InitialPassword: "password already set",
		},
	}

	optAgentSort := cmpopts.SortSlices(func(x, y bleemeoapi.AgentPayload) bool { return x.ID < y.ID })
	if diff := cmp.Diff(wantAgents, agents, cmpopts.EquateEmpty(), optAgentSort); diff != "" {
		t.Errorf("agents mismatch (-want +got)\n%s", diff)
	}

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: "cpu_used"}),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "ifOutOctets"},
			labels.Label{Name: gloutonTypes.LabelSNMPTarget, Value: snmpAddress},
			labels.Label{Name: gloutonTypes.LabelMetaBleemeoTargetAgentUUID, Value: idAgentSNMP},
		),
	})

	helper.AddTime(time.Second)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	var metrics []bleemeoapi.MetricPayload

	metrics = helper.wrapperClientMock.resources.metrics.clone()

	wantMetrics := []bleemeoapi.MetricPayload{
		{
			Metric: bleemeoTypes.Metric{
				ID:      "1",
				AgentID: idAgentMain,
			},
			Name: "agent_status",
		},
		{
			Metric: bleemeoTypes.Metric{
				ID:      "2",
				AgentID: idAgentMain,
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

	optMetricSort := cmpopts.SortSlices(func(x, y bleemeoapi.MetricPayload) bool { return x.ID < y.ID })
	if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
		t.Errorf("metrics mismatch (-want +got)\n%s", diff)
	}

	helper.AddTime(10 * time.Second)

	// Delete the SNMP agent on API.
	callCountBefore := updateLabelsCallCount

	helper.wrapperClientMock.resources.agents.dropByID(idAgentSNMP)
	helper.wrapperClientMock.resources.metrics.dropByID("3")

	helper.initSynchronizer(t)

	helper.AddTime(time.Second)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: "cpu_used"}),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	agents = helper.wrapperClientMock.resources.agents.clone()

	for _, a := range agents {
		if a.FQDN == snmpAddress {
			idAgentSNMP = a.ID
		}
	}

	wantAgents = []bleemeoapi.AgentPayload{
		wantAgents[0],
		{
			Agent: bleemeoTypes.Agent{
				ID:                     idAgentSNMP,
				CreatedAt:              helper.Now(),
				AccountID:              accountID,
				CurrentAccountConfigID: newAccountConfig.ID,
				AgentType:              agentTypeSNMP.ID,
				FQDN:                   snmpAddress,
				DisplayName:            "Z-The-Initial-Name",
			},
			Abstracted:      true,
			InitialPassword: "password already set",
		},
	}

	if diff := cmp.Diff(wantAgents, agents, cmpopts.EquateEmpty(), optAgentSort); diff != "" {
		t.Errorf("agents mismatch (-want +got)\n%s", diff)
	}

	helper.AddTime(time.Second)

	helper.pushPoints(t, []labels.Labels{
		labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: "cpu_used"}),
		labels.New(
			labels.Label{Name: gloutonTypes.LabelName, Value: "ifOutOctets"},
			labels.Label{Name: gloutonTypes.LabelSNMPTarget, Value: snmpAddress},
			labels.Label{Name: gloutonTypes.LabelMetaBleemeoTargetAgentUUID, Value: idAgentSNMP},
		),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	wantMetrics[2] = bleemeoapi.MetricPayload{
		Metric: bleemeoTypes.Metric{
			ID:         "4",
			AgentID:    idAgentSNMP,
			LabelsText: fmt.Sprintf(`__name__="ifOutOctets",snmp_target="%s"`, snmpAddress),
		},
		Name: "ifOutOctets",
	}

	metrics = helper.wrapperClientMock.resources.metrics.clone()

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
			labels.Label{Name: gloutonTypes.LabelName, Value: gloutonTypes.MetricServiceStatus},
			labels.Label{Name: gloutonTypes.LabelService, Value: "redis"},
			labels.Label{Name: gloutonTypes.LabelServiceInstance, Value: "my_redis_1"},
			labels.Label{Name: gloutonTypes.LabelMetaContainerID, Value: containerID},
		),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	// Did we store container & metrics?
	var containers []bleemeoapi.ContainerPayload

	containers = helper.wrapperClientMock.resources.containers.clone()

	wantContainer := []bleemeoapi.ContainerPayload{
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
		t.Errorf("container mismatch (-want +got)\n%s", diff)
	}

	var metrics []bleemeoapi.MetricPayload

	metrics = helper.wrapperClientMock.resources.metrics.clone()

	wantMetrics := []bleemeoapi.MetricPayload{
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
			Name: gloutonTypes.MetricServiceStatus,
			Item: "",
		},
	}

	optMetricSort := cmpopts.SortSlices(func(x, y bleemeoapi.MetricPayload) bool { return x.ID < y.ID })
	if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
		t.Errorf("metrics mismatch (-want +got)\n%s", diff)
	}

	helper.AddTime(time.Minute)
	helper.containers = []facts.Container{}
	helper.discovery.UpdatedAt = helper.s.now()

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	containers = helper.wrapperClientMock.resources.containers.clone()

	wantContainer = []bleemeoapi.ContainerPayload{
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
		t.Errorf("container mismatch (-want +got)\n%s", diff)
	}

	metrics = helper.wrapperClientMock.resources.metrics.clone()

	wantMetrics = []bleemeoapi.MetricPayload{
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
			Name: gloutonTypes.MetricServiceStatus,
			Item: "",
		},
	}

	if diff := cmp.Diff(wantMetrics, metrics, cmpopts.EquateEmpty(), optMetricSort); diff != "" {
		t.Errorf("metrics mismatch (-want +got)\n%s", diff)
	}

	helper.AddTime(2 * time.Hour)
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
			labels.Label{Name: gloutonTypes.LabelName, Value: gloutonTypes.MetricServiceStatus},
			labels.Label{Name: gloutonTypes.LabelService, Value: "redis"},
			labels.Label{Name: gloutonTypes.LabelServiceInstance, Value: "my_redis_1"},
			labels.Label{Name: gloutonTypes.LabelMetaContainerID, Value: containerID2},
		),
	})

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	containers = helper.wrapperClientMock.resources.containers.clone()

	wantContainer = []bleemeoapi.ContainerPayload{
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
		t.Errorf("container mismatch (-want +got)\n%s", diff)
	}

	metrics = helper.wrapperClientMock.resources.metrics.clone()

	wantMetrics = []bleemeoapi.MetricPayload{
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
			Name: gloutonTypes.MetricServiceStatus,
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
				labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: "cpu_used"}),
			})

			if err := helper.runOnce(t); err != nil {
				t.Fatal(err)
			}

			var (
				idAgentMain string
				idAgentSNMP string
			)

			agents := helper.wrapperClientMock.resources.agents.clone()
			for _, a := range agents {
				if a.FQDN == testAgentFQDN {
					idAgentMain = a.ID
				}

				if a.FQDN == snmpAddress {
					idAgentSNMP = a.ID
				}
			}

			wantAgents := []bleemeoapi.AgentPayload{
				{
					Agent: bleemeoTypes.Agent{
						ID:                     idAgentMain,
						CreatedAt:              helper.Now(),
						AccountID:              accountID,
						CurrentAccountConfigID: newAccountConfig.ID,
						AgentType:              agentTypeAgent.ID,
						FQDN:                   testAgentFQDN,
						DisplayName:            testAgentFQDN,
					},
					Abstracted:         false,
					InitialPassword:    "password already set",
					InitialServerGroup: tt.wantGroupForMainAgent,
				},
				{
					Agent: bleemeoTypes.Agent{
						ID:                     idAgentSNMP,
						CreatedAt:              helper.Now(),
						AccountID:              accountID,
						CurrentAccountConfigID: newAccountConfig.ID,
						AgentType:              agentTypeSNMP.ID,
						FQDN:                   snmpAddress,
						DisplayName:            "Z-The-Initial-Name",
					},
					Abstracted:         true,
					InitialPassword:    "password already set",
					InitialServerGroup: tt.wantGroupForSNMPAgent,
				},
			}

			optAgentSort := cmpopts.SortSlices(func(x, y bleemeoapi.AgentPayload) bool { return x.ID < y.ID })
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
				ID:                 configID1,
				Name:               "all-enable",
				MaxCustomMetrics:   999,
				LiveProcess:        true,
				DockerIntegration:  true,
				SNMPIntegration:    true,
				VSphereIntegration: true,
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
				ID:                 configID2,
				Name:               "no-docker",
				MaxCustomMetrics:   999,
				LiveProcess:        true,
				DockerIntegration:  false,
				SNMPIntegration:    true,
				VSphereIntegration: true,
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
				ID:                 configID2,
				Name:               "no-docker-limit-list",
				MaxCustomMetrics:   999,
				LiveProcess:        true,
				DockerIntegration:  false,
				SNMPIntegration:    true,
				VSphereIntegration: true,
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
				ID:                 configID2,
				Name:               "no-no-no",
				MaxCustomMetrics:   999,
				LiveProcess:        false,
				DockerIntegration:  false,
				SNMPIntegration:    false,
				VSphereIntegration: false,
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
				labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: "cpu_used"}),
				labels.New(labels.Label{Name: gloutonTypes.LabelName, Value: "custom_metric"}),
				model.AnnotationToMetaLabels(labels.FromMap(srvRedis1.LabelsOfStatus()), srvRedis1.AnnotationsOfStatus()),
				labels.New(
					labels.Label{Name: gloutonTypes.LabelName, Value: "probe_success"},
					labels.Label{Name: gloutonTypes.LabelScraperUUID, Value: idAgentMain},
					labels.Label{Name: gloutonTypes.LabelInstance, Value: newMonitor.URL},
					labels.Label{Name: gloutonTypes.LabelInstanceUUID, Value: newMonitor.AgentID},
					labels.Label{Name: gloutonTypes.LabelMetaBleemeoTargetAgentUUID, Value: newMonitor.AgentID},
				),
				labels.New(
					labels.Label{Name: gloutonTypes.LabelName, Value: "ifOutOctets"},
					labels.Label{Name: gloutonTypes.LabelSNMPTarget, Value: snmpAddress},
					labels.Label{Name: gloutonTypes.LabelMetaBleemeoTargetAgentUUID, Value: idAgentSNMP},
				),
				labels.New(
					labels.Label{Name: gloutonTypes.LabelName, Value: "redis_commands"},
					labels.Label{Name: gloutonTypes.LabelMetaServiceName, Value: "redis"},
					labels.Label{Name: gloutonTypes.LabelMetaServiceInstance, Value: "short-redis-container-name"},
					labels.Label{Name: gloutonTypes.LabelMetaContainerID, Value: "1234"},
					labels.Label{Name: gloutonTypes.LabelItem, Value: "short-redis-container-name"},
				),
			})

			if err := helper.runOnceWithResult(t).Check(); err != nil {
				t.Error(err)
			}

			wantAgents := []bleemeoapi.AgentPayload{
				{
					Agent: bleemeoTypes.Agent{
						ID:                     idAgentMain,
						CreatedAt:              helper.Now(),
						AccountID:              accountID,
						CurrentAccountConfigID: tt.accountConfig.ID,
						AgentType:              agentTypeAgent.ID,
						FQDN:                   testAgentFQDN,
						DisplayName:            testAgentFQDN,
					},
					Abstracted:      false,
					InitialPassword: "password already set",
				},
			}

			if tt.wantSNMP {
				wantAgents = append(wantAgents, bleemeoapi.AgentPayload{
					Agent: bleemeoTypes.Agent{
						ID:                     idAny,
						CreatedAt:              helper.Now(),
						AccountID:              accountID,
						CurrentAccountConfigID: tt.accountConfig.ID,
						AgentType:              agentTypeSNMP.ID,
						FQDN:                   snmpAddress,
						DisplayName:            "The-Initial-Name",
					},
					Abstracted:      true,
					InitialPassword: "password already set",
				})
			}

			helper.assertAgentsInAPI(t, wantAgents)

			helper.assertServicesInAPI(t, []bleemeoapi.ServicePayload{
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

			optSort := cmpopts.SortSlices(func(x bleemeoapi.ServicePayload, y bleemeoapi.ServicePayload) bool { return x.ID < y.ID })
			if diff := cmp.Diff([]bleemeoTypes.Monitor{monitor.Monitor}, helper.wrapperClientMock.resources.monitors.clone(), optSort); diff != "" {
				t.Errorf("monitors mismatch (-want +got)\n%s", diff)
			}

			wantMetrics := []bleemeoapi.MetricPayload{
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

			redisMetric := bleemeoapi.MetricPayload{
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
				wantMetrics = append(wantMetrics, bleemeoapi.MetricPayload{
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
				wantMetrics = append(wantMetrics, bleemeoapi.MetricPayload{
					Metric: bleemeoTypes.Metric{
						ID:      idAny,
						AgentID: idAgentMain,
					},
					Name: "custom_metric",
				})
			}

			if tt.wantSNMP {
				wantMetrics = append(wantMetrics, bleemeoapi.MetricPayload{
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

func makeAgentFactMap(facts map[string]string) map[string]bleemeoTypes.AgentFact {
	result := make(map[string]bleemeoTypes.AgentFact, len(facts))

	for k, v := range facts {
		result[k] = bleemeoTypes.AgentFact{
			ID:      uuid.New().String(),
			AgentID: "",
			Key:     k,
			Value:   v,
		}
	}

	return result
}

// nolint: dupl,nolintlint
func Test_isDuplicatedUsingFacts(t *testing.T) { //nolint:maintidx
	tests := []struct {
		name           string
		agentStartedAt []time.Time
		oldFacts       map[string]bleemeoTypes.AgentFact
		newFacts       map[string]bleemeoTypes.AgentFact
		wantDuplicated bool
		wantMessage    string
	}{
		{
			name:           "nil",
			oldFacts:       nil,
			newFacts:       nil,
			wantDuplicated: false,
		},
		{
			name: "new-server",
			agentStartedAt: []time.Time{
				time.Date(2023, 3, 24, 12, 41, 57, 0, time.UTC),
			},
			oldFacts:       map[string]bleemeoTypes.AgentFact{},
			newFacts:       map[string]bleemeoTypes.AgentFact{},
			wantDuplicated: false,
		},
		{
			// New server, at its 2nd synchronization (after essential facts)
			name: "new-server-second-sync",
			agentStartedAt: []time.Time{
				time.Date(2023, 3, 24, 12, 41, 57, 0, time.UTC),
			},
			oldFacts: makeAgentFactMap(map[string]string{
				"agent_version":       "23.03.24.091453",
				"architecture":        "arm64",
				"fqdn":                "ubuntu2210",
				"glouton_version":     "23.03.24.091453",
				"hostname":            "ubuntu2210",
				"installation_format": "Package (deb)",
				"kernel":              "Linux",
				"os_name":             "Ubuntu",
				"os_pretty_name":      "Ubuntu 22.10",
				"public_ip":           "12.23.45.67",
				"virtual":             "docker",
			}),
			newFacts: makeAgentFactMap(map[string]string{
				"agent_version":       "23.03.24.091453",
				"architecture":        "arm64",
				"fqdn":                "ubuntu2210",
				"glouton_version":     "23.03.24.091453",
				"hostname":            "ubuntu2210",
				"installation_format": "Package (deb)",
				"kernel":              "Linux",
				"os_name":             "Ubuntu",
				"os_pretty_name":      "Ubuntu 22.10",
				"public_ip":           "12.23.45.67",
				"virtual":             "docker",
			}),
			wantDuplicated: false,
		},
		{
			// New server, at its 3rd synchronization (after all facts)
			name: "existing-full",
			agentStartedAt: []time.Time{
				time.Date(2023, 3, 24, 12, 41, 57, 0, time.UTC),
			},
			oldFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "false",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T12:42:22Z",
				"fqdn":                 "ubuntu2210",
				"glouton_pid":          "999",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2210",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "kinetic",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.10",
				"os_version":           "22.10",
				"os_version_long":      "22.10 (Kinetic Kudu)",
				"primary_address":      "172.25.0.3",
				"primary_mac_address":  "01:02:03:04:00:03",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			newFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "false",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T12:42:22Z",
				"fqdn":                 "ubuntu2210",
				"glouton_pid":          "999",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2210",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "kinetic",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.10",
				"os_version":           "22.10",
				"os_version_long":      "22.10 (Kinetic Kudu)",
				"primary_address":      "172.25.0.3",
				"primary_mac_address":  "01:02:03:04:00:03",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			wantDuplicated: false,
		},
		{
			// A existing agent restarted normally
			name: "existing-full2",
			agentStartedAt: []time.Time{
				time.Date(2023, 3, 24, 12, 38, 42, 0, time.UTC),
				time.Date(2023, 2, 12, 4, 5, 9, 12358, time.UTC),
				time.Date(2021, 2, 24, 12, 38, 42, 0, time.UTC),
			},
			oldFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "true",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T12:38:08Z",
				"fqdn":                 "ubuntu2204",
				"glouton_pid":          "2699",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2204",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "jammy",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.04.1 LTS",
				"os_version":           "22.04",
				"os_version_long":      "22.04.1 LTS (Jammy Jellyfish)",
				"primary_address":      "172.25.0.2",
				"primary_mac_address":  "01:02:03:04:00:02",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			newFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "true",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T12:38:08Z",
				"fqdn":                 "ubuntu2204",
				"glouton_pid":          "2699",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2204",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "jammy",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.04.1 LTS",
				"os_version":           "22.04",
				"os_version_long":      "22.04.1 LTS (Jammy Jellyfish)",
				"primary_address":      "172.25.0.2",
				"primary_mac_address":  "01:02:03:04:00:02",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			wantDuplicated: false,
		},
		{
			// Existing server, we removed its state.cache.json
			name: "removed-state-cache-json",
			agentStartedAt: []time.Time{
				time.Date(2023, 3, 24, 12, 46, 48, 0, time.UTC),
				time.Date(2024, 3, 24, 12, 46, 48, 0, time.UTC),
			},
			oldFacts: makeAgentFactMap(map[string]string{}),
			newFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "true",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T12:38:47Z",
				"fqdn":                 "ubuntu2204",
				"glouton_pid":          "2721",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2204",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "jammy",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.04.1 LTS",
				"os_version":           "22.04",
				"os_version_long":      "22.04.1 LTS (Jammy Jellyfish)",
				"primary_address":      "172.25.0.2",
				"primary_mac_address":  "01:02:03:04:00:02",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			wantDuplicated: false,
		},
		{
			// Glouton crash just after all Bleemeo synchronzation (added a panic() in the code to simulate the case)
			// and then it restart (without the panic() in code).
			name: "glouton-crash-restart",
			agentStartedAt: []time.Time{
				time.Date(2023, 3, 24, 12, 48, 25, 0, time.UTC),
				time.Date(2023, 3, 24, 12, 47, 56, 0, time.UTC),
				time.Date(2024, 3, 24, 12, 48, 25, 0, time.UTC),
			},
			oldFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "true",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T12:46:53Z",
				"fqdn":                 "ubuntu2204",
				"glouton_pid":          "2745",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2204",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "jammy",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.04.1 LTS",
				"os_version":           "22.04",
				"os_version_long":      "22.04.1 LTS (Jammy Jellyfish)",
				"primary_address":      "172.25.0.2",
				"primary_mac_address":  "01:02:03:04:00:02",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			newFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "true",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T12:47:55Z",
				"fqdn":                 "ubuntu2204",
				"glouton_pid":          "2769",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2204",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "jammy",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.04.1 LTS",
				"os_version":           "22.04",
				"os_version_long":      "22.04.1 LTS (Jammy Jellyfish)",
				"primary_address":      "172.25.0.2",
				"primary_mac_address":  "01:02:03:04:00:02",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			wantDuplicated: false,
		},
		{
			// Agent restarted with an older version of state.cache.json
			name: "revert-state-json",
			agentStartedAt: []time.Time{
				time.Date(2023, 3, 24, 13, 14, 5, 0, time.UTC),
				time.Date(2024, 3, 24, 13, 14, 5, 0, time.UTC),
			},
			oldFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "true",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T12:48:30Z",
				"fqdn":                 "ubuntu2204",
				"glouton_pid":          "2790",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2204",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "jammy",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.04.1 LTS",
				"os_version":           "22.04",
				"os_version_long":      "22.04.1 LTS (Jammy Jellyfish)",
				"primary_address":      "172.25.0.2",
				"primary_mac_address":  "01:02:03:04:00:02",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			newFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "true",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T13:11:49Z",
				"fqdn":                 "ubuntu2204",
				"glouton_pid":          "2873",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2204",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "jammy",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.04.1 LTS",
				"os_version":           "22.04",
				"os_version_long":      "22.04.1 LTS (Jammy Jellyfish)",
				"primary_address":      "172.25.0.2",
				"primary_mac_address":  "01:02:03:04:00:02",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			wantDuplicated: false,
		},
		{
			// Agent running on two different server using the same state.json.
			name: "duplicated-two-servers",
			agentStartedAt: []time.Time{
				time.Date(2023, 3, 24, 12, 48, 25, 0, time.UTC),
				time.Date(2022, 3, 24, 12, 48, 25, 0, time.UTC),
			},
			oldFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "true",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T12:52:30Z",
				"fqdn":                 "ubuntu2204",
				"glouton_pid":          "2790",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2204",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "jammy",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.04.1 LTS",
				"os_version":           "22.04",
				"os_version_long":      "22.04.1 LTS (Jammy Jellyfish)",
				"primary_address":      "172.25.0.2",
				"primary_mac_address":  "01:02:03:04:00:02",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			newFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "false",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T12:52:49Z",
				"fqdn":                 "ubuntu2210",
				"glouton_pid":          "1032",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2210",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "kinetic",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.10",
				"os_version":           "22.10",
				"os_version_long":      "22.10 (Kinetic Kudu)",
				"primary_address":      "172.25.0.3",
				"primary_mac_address":  "01:02:03:04:00:03",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			wantDuplicated: true,
			wantMessage:    `Detected duplicated state.json. Another agent changed "fqdn" from "ubuntu2204" to "ubuntu2210"`,
		},
		{
			// Two agent on the same server
			name: "duplicated-same-server",
			agentStartedAt: []time.Time{
				time.Date(2023, 3, 24, 13, 10, 28, 0, time.UTC),
				time.Date(2022, 3, 24, 13, 10, 28, 0, time.UTC),
			},
			oldFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "true",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T13:11:23Z",
				"fqdn":                 "ubuntu2204",
				"glouton_pid":          "2829",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2204",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "jammy",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.04.1 LTS",
				"os_version":           "22.04",
				"os_version_long":      "22.04.1 LTS (Jammy Jellyfish)",
				"primary_address":      "172.25.0.2",
				"primary_mac_address":  "01:02:03:04:00:02",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			newFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "true",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T13:11:27Z",
				"fqdn":                 "ubuntu2204",
				"glouton_pid":          "2873",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2204",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "jammy",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.04.1 LTS",
				"os_version":           "22.04",
				"os_version_long":      "22.04.1 LTS (Jammy Jellyfish)",
				"primary_address":      "172.25.0.2",
				"primary_mac_address":  "01:02:03:04:00:02",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "false",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			wantDuplicated: true,
			wantMessage:    `Detected duplicated state.json. Another agent changed "glouton_pid" from "2829" to "2873"`,
		},
		{
			// Two agent on the same server
			name: "duplicated-two-server-with-old-version",
			agentStartedAt: []time.Time{
				time.Date(2023, 3, 24, 13, 22, 59, 0, time.UTC),
				time.Date(2022, 3, 24, 13, 10, 28, 0, time.UTC),
			},
			oldFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "true",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T13:42:44Z",
				"fqdn":                 "ubuntu2204",
				"glouton_pid":          "2963",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2204",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "jammy",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.04.1 LTS",
				"os_version":           "22.04",
				"os_version_long":      "22.04.1 LTS (Jammy Jellyfish)",
				"primary_address":      "172.25.0.2",
				"primary_mac_address":  "01:02:03:04:00:02",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			newFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.20.162215",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "true",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T13:54:19Z",
				"fqdn":                 "ubuntu2210",
				"glouton_version":      "23.03.20.162215",
				"hostname":             "ubuntu2210",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "kinetic",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.10",
				"os_version":           "22.10",
				"os_version_long":      "22.10 (Kinetic Kudu)",
				"primary_address":      "172.25.0.3",
				"primary_mac_address":  "01:02:03:04:00:03",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			wantDuplicated: true,
			wantMessage:    `Detected duplicated state.json. Another agent changed "fqdn" from "ubuntu2204" to "ubuntu2210"`,
		},

		{
			// FQDN change from a server 30 minutes in the past. View from server in correct time
			name: "duplicated-two-servers-time-drift-1",
			agentStartedAt: []time.Time{
				time.Date(2023, 3, 24, 13, 18, 58, 0, time.UTC),
				time.Date(2022, 3, 24, 13, 18, 58, 0, time.UTC),
			},
			oldFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "false",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T13:19:43Z",
				"fqdn":                 "ubuntu2210",
				"glouton_pid":          "1071",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2210",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "kinetic",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.10",
				"os_version":           "22.10",
				"os_version_long":      "22.10 (Kinetic Kudu)",
				"primary_address":      "172.25.0.3",
				"primary_mac_address":  "01:02:03:04:00:03",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			newFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "true",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T12:53:04Z",
				"fqdn":                 "ubuntu2204",
				"glouton_pid":          "2963",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2204",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "jammy",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.04.1 LTS",
				"os_version":           "22.04",
				"os_version_long":      "22.04.1 LTS (Jammy Jellyfish)",
				"primary_address":      "172.25.0.2",
				"primary_mac_address":  "01:02:03:04:00:02",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			wantDuplicated: true,
			wantMessage:    `Detected duplicated state.json. Another agent changed "fqdn" from "ubuntu2210" to "ubuntu2204"`,
		},
		{
			// FQDN change from a server 30 minutes in the past. View from server in past time
			name: "duplicated-two-servers-time-drift-1",
			agentStartedAt: []time.Time{
				time.Date(2023, 2, 24, 12, 48, 58, 0, time.UTC),
				time.Date(2022, 2, 24, 13, 18, 58, 0, time.UTC),
			},
			oldFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "true",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T12:55:30Z",
				"fqdn":                 "ubuntu2204",
				"glouton_pid":          "2963",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2204",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "jammy",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.04.1 LTS",
				"os_version":           "22.04",
				"os_version_long":      "22.04.1 LTS (Jammy Jellyfish)",
				"primary_address":      "172.25.0.2",
				"primary_mac_address":  "01:02:03:04:00:02",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			newFacts: makeAgentFactMap(map[string]string{
				"agent_version":        "23.03.24.091453",
				"architecture":         "arm64",
				"auto_upgrade_enabled": "false",
				"cpu_cores":            "5",
				"fact_updated_at":      "2023-03-24T13:25:30Z",
				"fqdn":                 "ubuntu2210",
				"glouton_pid":          "1098",
				"glouton_version":      "23.03.24.091453",
				"hostname":             "ubuntu2210",
				"installation_format":  "Package (deb)",
				"kernel":               "Linux",
				"kernel_major_version": "5.15",
				"kernel_release":       "5.15.49-linuxkit",
				"kernel_version":       "5.15.49",
				"memory":               "15.61 GB",
				"os_codename":          "kinetic",
				"os_family":            "debian",
				"os_name":              "Ubuntu",
				"os_pretty_name":       "Ubuntu 22.10",
				"os_version":           "22.10",
				"os_version_long":      "22.10 (Kinetic Kudu)",
				"primary_address":      "172.25.0.3",
				"primary_mac_address":  "01:02:03:04:00:03",
				"public_ip":            "12.23.45.67",
				"statsd_enable":        "true",
				"swap_present":         "true",
				"virtual":              "docker",
			}),
			wantDuplicated: true,
			wantMessage:    `Detected duplicated state.json. Another agent changed "fqdn" from "ubuntu2204" to "ubuntu2210"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, startAt := range tt.agentStartedAt {
				got, got1 := isDuplicatedUsingFacts(startAt, tt.oldFacts, tt.newFacts)

				if got != tt.wantDuplicated {
					t.Errorf("isDuplicatedUsingFacts() = %v, want %v [startAt=%s]", got, tt.wantDuplicated, startAt)
				}

				if got1 != tt.wantMessage {
					t.Errorf("isDuplicatedUsingFacts() message = %v, want %v [startAt=%s]", got1, tt.wantMessage, startAt)
				}
			}
		})
	}
}
