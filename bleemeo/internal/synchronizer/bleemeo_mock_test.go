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
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	gloutonTypes "github.com/bleemeo/glouton/types"
)

type wrapperClientMock struct {
	helper *syncTestHelper

	resources     resourcesSet
	requestCounts map[string]int
	errorsCount   int

	accountConfigNewAgent string
	username, password    string
}

func newClientMock(helper *syncTestHelper) *wrapperClientMock {
	clientMock := &wrapperClientMock{
		helper:        helper,
		requestCounts: make(map[string]int),
		resources: resourcesSet{
			agentTypes: resourceHolder[bleemeoTypes.AgentType]{
				elems: []bleemeoTypes.AgentType{
					agentTypeAgent,
					agentTypeSNMP,
					agentTypeMonitor,
					agentTypeKubernetes,
					agentTypeVSphereHost,
					agentTypeVSphereVM,
				},
			},
			accountConfigs: resourceHolder[bleemeoTypes.AccountConfig]{
				elems: []bleemeoTypes.AccountConfig{
					newAccountConfig,
				},
			},
			agentConfigs: resourceHolder[bleemeoTypes.AgentConfig]{
				elems: []bleemeoTypes.AgentConfig{
					agentConfigAgent,
					agentConfigSNMP,
					agentConfigMonitor,
					agentConfigKubernetes,
					agentConfigVSphereCluster,
					agentConfigVSphereHost,
					agentConfigVSphereVM,
				},
			},
		},
		accountConfigNewAgent: newAccountConfig.ID,
	}

	clientMock.resources.agents.createHook = func(agent *bleemeoapi.AgentPayload) error {
		// TODO: Glouton currently doesn't send the AccountID for SNMP type but does for
		// the main agent. We should be consistent (and API should NOT trust the value
		// from Glouton, so likely drop it ?).
		if agent.AccountID == "" && agent.AgentType == agentTypeSNMP.ID {
			agent.AccountID = accountID
		}

		if agent.AccountID != accountID {
			err := fmt.Errorf("%w, got %v, want %v", errInvalidAccountID, agent.AccountID, accountID)

			return err
		}

		if agent.AgentType == "" {
			agent.AgentType = agentTypeAgent.ID
		}

		if agent.AgentType == agentTypeAgent.ID {
			clientMock.username = agent.ID + "@bleemeo.com"
			clientMock.password = agent.InitialPassword
		}

		agent.CurrentAccountConfigID = clientMock.accountConfigNewAgent
		agent.InitialPassword = "password already set"
		agent.CreatedAt = helper.Now()

		return nil
	}

	return clientMock
}

func (wcm *wrapperClientMock) resetCount() {
	wcm.requestCounts = make(map[string]int)
	wcm.errorsCount = 0
}

func (wcm *wrapperClientMock) ThrottleDeadline() time.Time {
	return wcm.helper.s.now()
}

func (wcm *wrapperClientMock) GetGlobalInfo(context.Context) (bleemeoTypes.GlobalInfo, error) {
	return bleemeoTypes.GlobalInfo{}, nil
}

func (wcm *wrapperClientMock) RegisterSelf(_ context.Context, accountID, password, initialServerGroupName, name, fqdn, _ string) (id string, err error) {
	payload := bleemeoapi.AgentPayload{
		Agent: bleemeoTypes.Agent{
			AccountID:   accountID,
			DisplayName: name,
			FQDN:        fqdn,
		},
		InitialPassword:    password,
		InitialServerGroup: initialServerGroupName,
	}

	payload.ID = wcm.resources.agents.incID()
	err = wcm.resources.agents.createResource(&payload, &wcm.errorsCount)

	return payload.ID, err
}

func (wcm *wrapperClientMock) ListApplications(context.Context) ([]bleemeoTypes.Application, error) {
	wcm.requestCounts[mockAPIResourceApplication]++

	return wcm.resources.applications.clone(), nil
}

func (wcm *wrapperClientMock) CreateApplication(_ context.Context, payload bleemeoTypes.Application) (bleemeoTypes.Application, error) {
	wcm.requestCounts[mockAPIResourceApplication]++

	var app bleemeoTypes.Application

	err := newMapstructJSONDecoder(app, nil).Decode(payload)
	if err != nil {
		return bleemeoTypes.Application{}, err
	}

	app.ID = wcm.resources.applications.incID()

	return app, wcm.resources.applications.createResource(&app, &wcm.errorsCount)
}

func (wcm *wrapperClientMock) ListAgentTypes(context.Context) ([]bleemeoTypes.AgentType, error) {
	wcm.requestCounts[mockAPIResourceAgentType]++

	return wcm.resources.agentTypes.clone(), nil
}

func (wcm *wrapperClientMock) ListAccountConfigs(context.Context) ([]bleemeoTypes.AccountConfig, error) {
	wcm.requestCounts[mockAPIResourceAccountConfig]++

	return wcm.resources.accountConfigs.clone(), nil
}

func (wcm *wrapperClientMock) ListAgentConfigs(context.Context) ([]bleemeoTypes.AgentConfig, error) {
	wcm.requestCounts[mockAPIResourceAgentConfig]++

	return wcm.resources.agentConfigs.clone(), nil
}

func (wcm *wrapperClientMock) ListAgents(context.Context) ([]bleemeoTypes.Agent, error) {
	wcm.requestCounts[mockAPIResourceAgent]++

	agents := make([]bleemeoTypes.Agent, len(wcm.resources.agents.elems))
	for i, agent := range wcm.resources.agents.elems {
		agents[i] = agent.Agent
	}

	return agents, nil
}

func (wcm *wrapperClientMock) UpdateAgent(_ context.Context, id string, data any) (bleemeoTypes.Agent, error) {
	wcm.requestCounts[mockAPIResourceAgent]++

	agent := wcm.resources.agents.findResource(func(a bleemeoapi.AgentPayload) bool { return a.ID == id })
	if agent == nil {
		return bleemeoTypes.Agent{}, &bleemeo.APIError{
			StatusCode: http.StatusNotFound,
			Err:        fmt.Errorf("%w: agent id with %q", bleemeo.ErrResourceNotFound, id),
		}
	}

	err := newMapstructJSONDecoder(agent, nil).Decode(data)
	if err != nil {
		return bleemeoTypes.Agent{}, err
	}

	if agent.ID == "" {
		// The ID may have been wiped out during the payload decoding
		agent.ID = id
	}

	return agent.Agent, wcm.resources.agents.applyPatchHook(agent, &wcm.errorsCount)
}

func (wcm *wrapperClientMock) DeleteAgent(_ context.Context, id string) error {
	wcm.requestCounts[mockAPIResourceAgent]++

	return wcm.resources.agents.deleteResource(func(a bleemeoapi.AgentPayload) bool { return a.ID == id })
}

func (wcm *wrapperClientMock) ListGloutonConfigItems(_ context.Context, agentID string) ([]bleemeoTypes.GloutonConfigItem, error) {
	wcm.requestCounts[mockAPIGloutonConfigItem]++

	return wcm.resources.gloutonConfigItems.filterResources(func(i bleemeoTypes.GloutonConfigItem) bool { return i.Agent == agentID }), nil
}

func (wcm *wrapperClientMock) RegisterGloutonConfigItems(ctx context.Context, items []bleemeoTypes.GloutonConfigItem) error {
	wcm.requestCounts[mockAPIGloutonConfigItem]++

	panic("implement me")
}

func (wcm *wrapperClientMock) DeleteGloutonConfigItem(ctx context.Context, id string) error {
	wcm.requestCounts[mockAPIGloutonConfigItem]++

	panic("implement me")
}

func (wcm *wrapperClientMock) ListContainers(context.Context, string) ([]bleemeoTypes.Container, error) {
	wcm.requestCounts[mockAPIResourceContainer]++

	containers := make([]bleemeoTypes.Container, len(wcm.resources.containers.elems))

	for i, c := range wcm.resources.containers.elems {
		containers[i] = c.Container
	}

	return containers, nil
}

func (wcm *wrapperClientMock) UpdateContainer(_ context.Context, id string, payload any) (bleemeoapi.ContainerPayload, error) {
	wcm.requestCounts[mockAPIResourceContainer]++

	container := wcm.resources.containers.findResource(func(m bleemeoapi.ContainerPayload) bool { return m.ID == id })
	if container == nil {
		return bleemeoapi.ContainerPayload{}, &bleemeo.APIError{
			StatusCode: http.StatusNotFound,
			Err:        fmt.Errorf("%w: container with id %q", bleemeo.ErrResourceNotFound, id),
		}
	}

	wasActive := time.Time(container.DeletedAt).IsZero()

	err := newMapstructJSONDecoder(container, nil).Decode(payload)
	if err != nil {
		return bleemeoapi.ContainerPayload{}, err
	}

	if container.ID == "" {
		// The ID may have been wiped out during the payload decoding
		container.ID = id
	}

	if wasActive && !time.Time(container.DeletedAt).IsZero() {
		// The container has been deactivated. Do what API does: deactivate all associated metrics.
		for i, metric := range wcm.resources.metrics.elems {
			if metric.ContainerID == id {
				wcm.resources.metrics.elems[i].DeactivatedAt = time.Time(container.DeletedAt)
			}
		}
	}

	err = wcm.resources.containers.applyPatchHook(container, &wcm.errorsCount)
	if err != nil {
		return bleemeoapi.ContainerPayload{}, err
	}

	return *container, nil
}

func (wcm *wrapperClientMock) RegisterContainer(_ context.Context, payload bleemeoapi.ContainerPayload) (bleemeoapi.ContainerPayload, error) {
	wcm.requestCounts[mockAPIResourceContainer]++

	var result bleemeoapi.ContainerPayload

	err := newMapstructJSONDecoder(&result, nil).Decode(payload)
	if err != nil {
		return bleemeoapi.ContainerPayload{}, err
	}

	result.ID = wcm.resources.containers.incID()
	err = wcm.resources.containers.createResource(&result, &wcm.errorsCount)

	return result, err
}

func (wcm *wrapperClientMock) ListDiagnostics(context.Context) ([]bleemeoapi.RemoteDiagnostic, error) {
	wcm.requestCounts[mockAPIGloutonDiagnostic]++

	return wcm.resources.gloutonDiagnostics.clone(), nil
}

func (wcm *wrapperClientMock) UploadDiagnostic(ctx context.Context, contentType string, content io.Reader) error {
	wcm.requestCounts[mockAPIGloutonDiagnostic]++

	panic("implement me")
}

func (wcm *wrapperClientMock) ListFacts(context.Context) ([]bleemeoTypes.AgentFact, error) {
	wcm.requestCounts[mockAPIResourceAgentFact]++

	return wcm.resources.agentFacts.clone(), nil
}

func (wcm *wrapperClientMock) RegisterFact(_ context.Context, payload bleemeoTypes.AgentFact) (bleemeoTypes.AgentFact, error) {
	wcm.requestCounts[mockAPIResourceAgentFact]++

	var result bleemeoTypes.AgentFact

	err := newMapstructJSONDecoder(&result, nil).Decode(payload)
	if err != nil {
		return bleemeoTypes.AgentFact{}, err
	}

	result.ID = wcm.resources.agentFacts.incID()
	err = wcm.resources.agentFacts.createResource(&result, &wcm.errorsCount)

	return result, err
}

func (wcm *wrapperClientMock) DeleteFact(ctx context.Context, id string) error {
	wcm.requestCounts[mockAPIResourceAgentFact]++

	panic("implement me")
}

func (wcm *wrapperClientMock) UpdateMetric(_ context.Context, id string, payload any, _ string) error {
	wcm.requestCounts[mockAPIResourceMetric]++

	metric := wcm.resources.metrics.findResource(func(m bleemeoapi.MetricPayload) bool { return m.ID == id })
	if metric == nil {
		return &bleemeo.APIError{
			StatusCode: http.StatusNotFound,
			Err:        fmt.Errorf("%w: metric with id %q", bleemeo.ErrResourceNotFound, id),
		}
	}

	metricW := struct {
		*bleemeoapi.MetricPayload

		Active *bool `json:"active"`
	}{
		MetricPayload: metric,
	}

	// A DecoderHook is necessary to convert the {"active": "True"} into a boolean
	err := newMapstructJSONDecoder(&metricW, func(from, to reflect.Kind, v any) (any, error) {
		if from == reflect.String && to == reflect.Bool {
			return strconv.ParseBool(v.(string)) //nolint: forcetypeassert
		}

		return v, nil
	}).Decode(payload)
	if err != nil {
		return err
	}

	if metric.ID == "" {
		// The ID may have been wiped out during the payload decoding
		metric.ID = id
	}

	if metricW.Active != nil {
		if *metricW.Active {
			metric.DeactivatedAt = time.Time{}
		} else {
			metric.DeactivatedAt = wcm.helper.Now()
		}
	}

	return wcm.resources.metrics.applyPatchHook(metric, &wcm.errorsCount)
}

func (wcm *wrapperClientMock) ListActiveMetrics(ctx context.Context) ([]bleemeoapi.MetricPayload, error) {
	return wcm.listMetrics(ctx, true, nil)
}

func (wcm *wrapperClientMock) ListInactiveMetrics(ctx context.Context, stopSearchingPredicate func(string) bool) ([]bleemeoapi.MetricPayload, error) {
	return wcm.listMetrics(ctx, false, stopSearchingPredicate)
}

func (wcm *wrapperClientMock) listMetrics(_ context.Context, active bool, _ func(string) bool) ([]bleemeoapi.MetricPayload, error) {
	wcm.requestCounts[mockAPIResourceMetric]++

	metrics := wcm.resources.metrics.filterResources(func(m bleemeoapi.MetricPayload) bool {
		return active == m.DeactivatedAt.IsZero()
	})

	return metrics, nil
}

func (wcm *wrapperClientMock) CountInactiveMetrics(context.Context) (int, error) {
	wcm.requestCounts[mockAPIResourceMetric]++

	return len(wcm.resources.metrics.filterResources(func(m bleemeoapi.MetricPayload) bool { return !m.DeactivatedAt.IsZero() })), nil
}

func (wcm *wrapperClientMock) ListMetricsBy(_ context.Context, params url.Values) (map[string]bleemeoTypes.Metric, error) {
	wcm.requestCounts[mockAPIResourceMetric]++

	metrics := wcm.resources.metrics.filterResources(func(m bleemeoapi.MetricPayload) bool {
		if params.Has("labels_text") && m.LabelsText != params.Get("labels_text") {
			return false
		}

		if params.Has("agent") && m.AgentID != params.Get("agent") {
			return false
		}

		if params.Has("label") && m.Labels[gloutonTypes.LabelName] != params.Get("label") {
			return false
		}

		if params.Has("item") && m.Item != params.Get("item") {
			return false
		}

		return true
	})

	result := make(map[string]bleemeoTypes.Metric, len(metrics))
	for _, m := range metrics {
		result[m.ID] = m.Metric
	}

	return result, nil
}

func (wcm *wrapperClientMock) GetMetricByID(ctx context.Context, id string) (bleemeoapi.MetricPayload, error) {
	wcm.requestCounts[mockAPIResourceMetric]++

	panic("implement me")
}

func (wcm *wrapperClientMock) RegisterMetric(_ context.Context, payload bleemeoapi.MetricPayload) (bleemeoapi.MetricPayload, error) {
	wcm.requestCounts[mockAPIResourceMetric]++

	var result bleemeoapi.MetricPayload

	err := newMapstructJSONDecoder(&result, nil).Decode(payload)
	if err != nil {
		return bleemeoapi.MetricPayload{}, err
	}

	result.Metric.ID = wcm.resources.metrics.incID()
	err = wcm.resources.metrics.createResource(&result, &wcm.errorsCount)

	return result, err
}

func (wcm *wrapperClientMock) DeleteMetric(ctx context.Context, id string) error {
	wcm.requestCounts[mockAPIResourceMetric]++

	panic("implement me")
}

func (wcm *wrapperClientMock) SetMetricActive(_ context.Context, id string, active bool) error {
	wcm.requestCounts[mockAPIResourceMetric]++

	predicate := func(m bleemeoapi.MetricPayload) bool { return m.ID == id }

	metric := wcm.resources.metrics.findResource(predicate)
	if metric == nil {
		return &bleemeo.APIError{
			StatusCode: http.StatusNotFound,
			Err:        fmt.Errorf("%w: metric with id %q", bleemeo.ErrResourceNotFound, id),
		}
	}

	// Prevent applying the update if the patch hook fails - should be implemented wherever tests rely on this logic.
	tmp := *metric

	if active {
		tmp.DeactivatedAt = time.Time{}
	} else {
		tmp.DeactivatedAt = wcm.helper.Now()
	}

	err := wcm.resources.metrics.applyPatchHook(&tmp, &wcm.errorsCount)
	if err != nil {
		return err
	}

	*metric = tmp

	return nil
}

func (wcm *wrapperClientMock) ListMonitors(context.Context) ([]bleemeoTypes.Monitor, error) {
	wcm.requestCounts[mockAPIResourceService]++

	return wcm.resources.monitors.clone(), nil
}

func (wcm *wrapperClientMock) GetMonitorByID(ctx context.Context, id string) (bleemeoTypes.Monitor, error) {
	wcm.requestCounts[mockAPIResourceService]++

	panic("implement me")
}

func (wcm *wrapperClientMock) ListServices(_ context.Context, agentID string, _ string) ([]bleemeoTypes.Service, error) {
	wcm.requestCounts[mockAPIResourceService]++

	services := make([]bleemeoTypes.Service, 0, len(wcm.resources.services.elems))

	for _, s := range wcm.resources.services.elems {
		if s.AgentID != agentID {
			continue
		}

		services = append(services, s.Service)
	}

	return services, nil
}

func (wcm *wrapperClientMock) UpdateService(_ context.Context, id string, payload bleemeoapi.ServicePayload, _ string) (bleemeoTypes.Service, error) {
	wcm.requestCounts[mockAPIResourceService]++

	service := wcm.resources.services.findResource(func(s bleemeoapi.ServicePayload) bool { return s.ID == id })
	if service == nil {
		return bleemeoTypes.Service{}, &bleemeo.APIError{
			StatusCode: http.StatusNotFound,
			Err:        fmt.Errorf("%w: service with id %q", bleemeo.ErrResourceNotFound, id),
		}
	}

	err := newMapstructJSONDecoder(service, nil).Decode(payload)
	if err != nil {
		return bleemeoTypes.Service{}, err
	}

	if service.ID == "" {
		// The ID may have been wiped out during the payload decoding
		service.ID = id
	}

	return service.Service, wcm.resources.services.applyPatchHook(service, &wcm.errorsCount)
}

func (wcm *wrapperClientMock) RegisterService(_ context.Context, payload bleemeoapi.ServicePayload) (bleemeoTypes.Service, error) {
	wcm.requestCounts[mockAPIResourceService]++

	payload.ID = wcm.resources.services.incID()

	return payload.Service, wcm.resources.services.createResource(&payload, &wcm.errorsCount)
}

func (wcm *wrapperClientMock) DeleteService(ctx context.Context, id string) error {
	wcm.requestCounts[mockAPIResourceService]++

	panic("implement me")
}

func (wcm *wrapperClientMock) RegisterSNMPAgent(_ context.Context, payload bleemeoapi.AgentPayload) (bleemeoTypes.Agent, error) {
	wcm.requestCounts[mockAPIResourceAgent]++

	payload.ID = wcm.resources.agents.incID()

	return payload.Agent, wcm.resources.agents.createResource(&payload, &wcm.errorsCount)
}

func (wcm *wrapperClientMock) UpdateAgentLastDuplicationDate(ctx context.Context, agentID string, lastDuplicationDate time.Time) error {
	wcm.requestCounts[mockAPIResourceAgent]++

	panic("implement me")
}

func (wcm *wrapperClientMock) RegisterVSphereAgent(ctx context.Context, payload bleemeoapi.AgentPayload) (bleemeoTypes.Agent, error) {
	wcm.requestCounts[mockAPIResourceAgent]++

	panic("implement me")
}

func (wcm *wrapperClientMock) AssertCallsPerResource(t *testing.T, resource string, expected int) {
	t.Helper()

	if count := wcm.requestCounts[resource]; count != expected {
		t.Errorf("Had %d requests on resource %s, want %d", count, resource, expected)
	}
}
