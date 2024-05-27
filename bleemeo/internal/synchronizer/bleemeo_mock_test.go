// Copyright 2015-2023 Bleemeo
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
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	gloutonTypes "github.com/bleemeo/glouton/types"
	"github.com/mitchellh/mapstructure"
)

func newMapstructJSONDecoder(target any, decodeHook mapstructure.DecodeHookFunc) *mapstructure.Decoder {
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		DecodeHook: decodeHook,
		Result:     target,
		TagName:    "json",
	})
	if err != nil {
		panic(fmt.Sprintf("invalid target %T for mapstructure decoding", target))
	}

	return decoder
}

type resSlice[T any] []T

func (s *resSlice[T]) findResource(predicate func(T) bool) *T {
	for i, e := range *s {
		if predicate(e) {
			return &(*s)[i]
		}
	}

	return nil
}

func (s *resSlice[T]) filterResources(predicate func(T) bool) []T {
	var result []T

	for _, e := range *s {
		if predicate(e) {
			result = append(result, e)
		}
	}

	return result
}

func (s *resSlice[T]) deleteResource(predicate func(T) bool) error {
	idx := slices.IndexFunc(*s, predicate)
	if idx < 0 {
		return errNotFound
	}

	*s = slices.Delete(*s, idx, idx+1)

	return nil
}

type metricW struct {
	bleemeoapi.MetricPayload
	Active bool `json:"active"`
}

type dataHolder struct {
	applications       resSlice[bleemeoTypes.Application]
	agents             resSlice[bleemeoTypes.Agent]
	agentfacts         resSlice[bleemeoTypes.AgentFact]
	agenttypes         resSlice[bleemeoTypes.AgentType]
	containers         resSlice[bleemeoTypes.Container]
	metrics            resSlice[metricW]
	accountconfigs     resSlice[bleemeoTypes.AccountConfig]
	agentconfigs       resSlice[bleemeoTypes.AgentConfig]
	monitors           resSlice[bleemeoTypes.Monitor]
	services           resSlice[bleemeoTypes.Service]
	gloutonconfigitems resSlice[bleemeoTypes.GloutonConfigItem]
	gloutondiagnostics resSlice[bleemeoapi.RemoteDiagnostic]
}

type wrapperClientMock struct {
	helper *syncTestHelper

	requestCounts map[string]int
	data          dataHolder
}

func (wcm *wrapperClientMock) ThrottleDeadline() time.Time {
	return wcm.helper.s.now()
}

func (wcm *wrapperClientMock) Do(ctx context.Context, method string, reqURI string, params url.Values, authenticated bool, body io.Reader, result any) (statusCode int, err error) {
	switch reqURI {
	case "v1/info/":
		return 200, nil
	default:
		return 404, nil
	}
}

func (wcm *wrapperClientMock) DoWithBody(ctx context.Context, reqURI string, contentType string, body io.Reader) (statusCode int, err error) {
	// TODO implement me
	panic("implement me")
}

func (wcm *wrapperClientMock) Iterator(resource bleemeo.Resource, params url.Values) bleemeo.Iterator {
	// TODO implement me
	panic("implement me")
}

func (wcm *wrapperClientMock) ListApplications(context.Context) ([]bleemeoTypes.Application, error) {
	wcm.requestCounts[mockAPIResourceApplication]++

	return wcm.data.applications, nil
}

func (wcm *wrapperClientMock) CreateApplication(ctx context.Context, payload bleemeoTypes.Application) (bleemeoTypes.Application, error) {
	wcm.requestCounts[mockAPIResourceApplication]++

	var app bleemeoTypes.Application

	err := newMapstructJSONDecoder(app, nil).Decode(payload)
	if err != nil {
		return bleemeoTypes.Application{}, err
	}

	wcm.data.applications = append(wcm.data.applications, app)

	return app, nil
}

func (wcm *wrapperClientMock) ListAgentTypes(context.Context) ([]bleemeoTypes.AgentType, error) {
	wcm.requestCounts[mockAPIResourceAgentType]++

	return wcm.data.agenttypes, nil
}

func (wcm *wrapperClientMock) ListAccountConfigs(context.Context) ([]bleemeoTypes.AccountConfig, error) {
	wcm.requestCounts[mockAPIResourceAccountConfig]++

	return wcm.data.accountconfigs, nil
}

func (wcm *wrapperClientMock) ListAgentConfigs(context.Context) ([]bleemeoTypes.AgentConfig, error) {
	wcm.requestCounts[mockAPIResourceAgentConfig]++

	return wcm.data.agentconfigs, nil
}

func (wcm *wrapperClientMock) ListAgents(context.Context) ([]bleemeoTypes.Agent, error) {
	wcm.requestCounts[mockAPIResourceAgent]++

	return wcm.data.agents, nil
}

func (wcm *wrapperClientMock) UpdateAgent(_ context.Context, id string, data any) (bleemeoTypes.Agent, error) {
	wcm.requestCounts[mockAPIResourceAgent]++

	agent := wcm.data.agents.findResource(func(a bleemeoTypes.Agent) bool { return a.ID == id })
	if agent == nil {
		return bleemeoTypes.Agent{}, fmt.Errorf("agent with id %q %w", id, errNotFound)
	}

	return *agent, newMapstructJSONDecoder(agent, nil).Decode(data)
}

func (wcm *wrapperClientMock) DeleteAgent(_ context.Context, id string) error {
	wcm.requestCounts[mockAPIResourceAgent]++

	return wcm.data.agents.deleteResource(func(a bleemeoTypes.Agent) bool { return a.ID == id })
}

func (wcm *wrapperClientMock) ListGloutonConfigItems(_ context.Context, agentID string) ([]bleemeoTypes.GloutonConfigItem, error) {
	wcm.requestCounts[mockAPIGloutonConfigItem]++

	return wcm.data.gloutonconfigitems.filterResources(func(i bleemeoTypes.GloutonConfigItem) bool { return i.Agent == agentID }), nil
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

	return slices.Clone(wcm.data.containers), nil
}

func (wcm *wrapperClientMock) UpdateContainer(ctx context.Context, id string, payload any, result *bleemeoapi.ContainerPayload) error {
	wcm.requestCounts[mockAPIResourceContainer]++

	panic("implement me")
}

func (wcm *wrapperClientMock) RegisterContainer(ctx context.Context, payload bleemeoapi.ContainerPayload, result *bleemeoapi.ContainerPayload) error {
	wcm.requestCounts[mockAPIResourceContainer]++

	panic("implement me")
}

func (wcm *wrapperClientMock) ListDiagnostics(context.Context) ([]bleemeoapi.RemoteDiagnostic, error) {
	wcm.requestCounts[mockAPIGloutonDiagnostic]++

	return slices.Clone(wcm.data.gloutondiagnostics), nil
}

func (wcm *wrapperClientMock) UploadDiagnostic(ctx context.Context, contentType string, content io.Reader) error {
	wcm.requestCounts[mockAPIGloutonDiagnostic]++

	panic("implement me")
}

func (wcm *wrapperClientMock) ListFacts(context.Context) ([]bleemeoTypes.AgentFact, error) {
	wcm.requestCounts[mockAPIResourceAgentFact]++

	return slices.Clone(wcm.data.agentfacts), nil
}

func (wcm *wrapperClientMock) RegisterFact(_ context.Context, payload any, result *bleemeoTypes.AgentFact) error {
	wcm.requestCounts[mockAPIResourceAgentFact]++

	err := newMapstructJSONDecoder(result, nil).Decode(payload)
	if err != nil {
		return err
	}

	wcm.data.agentfacts = append(wcm.data.agentfacts, *result)

	return nil
}

func (wcm *wrapperClientMock) DeleteFact(ctx context.Context, id string) error {
	wcm.requestCounts[mockAPIResourceAgentFact]++

	panic("implement me")
}

func (wcm *wrapperClientMock) UpdateMetric(_ context.Context, id string, payload any, _ string) error {
	wcm.requestCounts[mockAPIResourceMetric]++

	metric := wcm.data.metrics.findResource(func(m metricW) bool { return m.ID == id })
	if metric == nil {
		return fmt.Errorf("metric with id %q %w", id, errNotFound)
	}

	// A DecoderHook is needed to convert the {"active": "True"} into a boolean
	return newMapstructJSONDecoder(metric, func(from, to reflect.Kind, v any) (any, error) {
		if from == reflect.String && to == reflect.Bool {
			return strconv.ParseBool(v.(string))
		}

		return v, nil
	}).Decode(payload)
}

func (wcm *wrapperClientMock) ListActiveMetrics(_ context.Context, active bool, filter func(payload bleemeoapi.MetricPayload) bool) (map[string]bleemeoTypes.Metric, error) {
	wcm.requestCounts[mockAPIResourceMetric]++

	metrics := wcm.data.metrics.filterResources(func(m metricW) bool {
		return m.Active == active && filter(m.MetricPayload)
	})
	result := make(map[string]bleemeoTypes.Metric, len(metrics))

	for _, m := range metrics {
		result[m.ID] = m.Metric
	}

	return result, nil
}

func (wcm *wrapperClientMock) CountInactiveMetrics(context.Context) (int, error) {
	wcm.requestCounts[mockAPIResourceMetric]++

	return len(wcm.data.metrics.filterResources(func(m metricW) bool { return !m.Active })), nil
}

func (wcm *wrapperClientMock) ListMetricsBy(_ context.Context, params url.Values, filter func(payload bleemeoapi.MetricPayload) bool) (map[string]bleemeoTypes.Metric, error) {
	wcm.requestCounts[mockAPIResourceMetric]++

	metrics := wcm.data.metrics.filterResources(func(m metricW) bool {
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
		return filter(m.MetricPayload)
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

func (wcm *wrapperClientMock) RegisterMetric(_ context.Context, payload bleemeoapi.MetricPayload, result *bleemeoapi.MetricPayload) error {
	wcm.requestCounts[mockAPIResourceMetric]++

	err := newMapstructJSONDecoder(result, nil).Decode(payload)
	if err != nil {
		return err
	}

	result.ID = strconv.Itoa(len(wcm.data.metrics) + 1)
	wcm.data.metrics = append(wcm.data.metrics, metricW{MetricPayload: *result, Active: true})

	return nil
}

func (wcm *wrapperClientMock) DeleteMetric(ctx context.Context, id string) error {
	wcm.requestCounts[mockAPIResourceMetric]++

	panic("implement me")
}

func (wcm *wrapperClientMock) DeactivateMetric(_ context.Context, id string) error {
	wcm.requestCounts[mockAPIResourceMetric]++

	predicate := func(m metricW) bool { return m.ID == id }

	metric := wcm.data.metrics.findResource(predicate)
	if metric == nil {
		return fmt.Errorf("metric with id %q %w", id, errNotFound)
	}

	metric.Active = false

	return nil
}

func (wcm *wrapperClientMock) ListMonitors(context.Context) ([]bleemeoTypes.Monitor, error) {
	wcm.requestCounts[mockAPIResourceService]++

	return slices.Clone(wcm.data.monitors), nil
}

func (wcm *wrapperClientMock) GetMonitorByID(ctx context.Context, id string) (bleemeoTypes.Monitor, error) {
	wcm.requestCounts[mockAPIResourceService]++

	panic("implement me")
}

func (wcm *wrapperClientMock) ListServices(_ context.Context, agentID string, _ string) ([]bleemeoTypes.Service, error) {
	wcm.requestCounts[mockAPIResourceService]++

	return slices.Clone(wcm.data.services), nil // TODO: take agentID into account
}

func (wcm *wrapperClientMock) UpdateService(_ context.Context, id string, payload bleemeoapi.ServicePayload, _ string) (bleemeoTypes.Service, error) {
	wcm.requestCounts[mockAPIResourceService]++

	service := wcm.data.services.findResource(func(s bleemeoTypes.Service) bool { return s.ID == id })
	if service == nil {
		return bleemeoTypes.Service{}, fmt.Errorf("service with id %q %w", id, errNotFound)
	}

	return *service, newMapstructJSONDecoder(service, nil).Decode(payload)
}

func (wcm *wrapperClientMock) RegisterService(_ context.Context, payload bleemeoapi.ServicePayload, _ string) (bleemeoTypes.Service, error) {
	wcm.requestCounts[mockAPIResourceService]++

	wcm.data.services = append(wcm.data.services, payload.Service)

	return payload.Service, nil
}

func (wcm *wrapperClientMock) RegisterSNMPAgent(ctx context.Context, payload bleemeoapi.AgentPayload) (bleemeoTypes.Agent, error) {
	wcm.requestCounts[mockAPIResourceAgent]++

	panic("implement me")
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
