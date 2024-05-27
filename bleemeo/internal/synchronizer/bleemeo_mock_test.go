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

	"github.com/bleemeo/glouton/bleemeo/types"
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
	metricPayload
	Active bool `json:"active"`
}

type dataHolder struct {
	agents             resSlice[types.Agent]
	agentfacts         resSlice[types.AgentFact]
	agenttypes         resSlice[types.AgentType]
	containers         resSlice[types.Container]
	metrics            resSlice[metricW]
	accountconfigs     resSlice[types.AccountConfig]
	agentconfigs       resSlice[types.AgentConfig]
	monitors           resSlice[types.Monitor]
	services           resSlice[types.Service]
	gloutonconfigitems resSlice[types.GloutonConfigItem]
	gloutondiagnostics resSlice[RemoteDiagnostic]
}

type wrapperClientMock struct {
	helper *syncTestHelper

	requestCounts map[string]int
	data          dataHolder
}

func (wcm *wrapperClientMock) ThrottleDeadline() time.Time {
	return wcm.helper.s.now()
}

func (wcm *wrapperClientMock) listAgentTypes(context.Context) ([]types.AgentType, error) {
	wcm.requestCounts[mockAPIResourceAgentType]++

	return wcm.data.agenttypes, nil
}

func (wcm *wrapperClientMock) listAccountConfigs(context.Context) ([]types.AccountConfig, error) {
	wcm.requestCounts[mockAPIResourceAccountConfig]++

	return wcm.data.accountconfigs, nil
}

func (wcm *wrapperClientMock) listAgentConfigs(context.Context) ([]types.AgentConfig, error) {
	wcm.requestCounts[mockAPIResourceAgentConfig]++

	return wcm.data.agentconfigs, nil
}

func (wcm *wrapperClientMock) listAgents(context.Context) ([]types.Agent, error) {
	wcm.requestCounts[mockAPIResourceAgent]++

	return wcm.data.agents, nil
}

func (wcm *wrapperClientMock) updateAgent(_ context.Context, id string, data any) (types.Agent, error) {
	wcm.requestCounts[mockAPIResourceAgent]++

	agent := wcm.data.agents.findResource(func(a types.Agent) bool { return a.ID == id })
	if agent == nil {
		return types.Agent{}, fmt.Errorf("agent with id %q %w", id, errNotFound)
	}

	return *agent, newMapstructJSONDecoder(agent, nil).Decode(data)
}

func (wcm *wrapperClientMock) deleteAgent(_ context.Context, id string) error {
	wcm.requestCounts[mockAPIResourceAgent]++

	return wcm.data.agents.deleteResource(func(a types.Agent) bool { return a.ID == id })
}

func (wcm *wrapperClientMock) listGloutonConfigItems(_ context.Context, agentID string) (map[comparableConfigItem]configItemValue, error) {
	wcm.requestCounts[mockAPIGloutonConfigItem]++

	result := make(map[comparableConfigItem]configItemValue, len(wcm.data.gloutonconfigitems))

	items := wcm.data.gloutonconfigitems.filterResources(func(i types.GloutonConfigItem) bool { return i.Agent == agentID })
	for _, item := range items {
		key := comparableConfigItem{
			Key:      item.Key,
			Priority: item.Priority,
			Source:   item.Source,
			Path:     item.Path,
			Type:     item.Type,
		}

		result[key] = configItemValue{
			ID:    item.ID,
			Value: item.Value,
		}
	}

	return result, nil
}

func (wcm *wrapperClientMock) registerGloutonConfigItems(ctx context.Context, items []types.GloutonConfigItem) error {
	wcm.requestCounts[mockAPIGloutonConfigItem]++

	panic("implement me")
}

func (wcm *wrapperClientMock) deleteGloutonConfigItem(ctx context.Context, id string) error {
	wcm.requestCounts[mockAPIGloutonConfigItem]++

	panic("implement me")
}

func (wcm *wrapperClientMock) listContainers(context.Context, string) ([]types.Container, error) {
	wcm.requestCounts[mockAPIResourceContainer]++

	return slices.Clone(wcm.data.containers), nil
}

func (wcm *wrapperClientMock) updateContainer(ctx context.Context, id string, payload any, result *containerPayload) error {
	wcm.requestCounts[mockAPIResourceContainer]++

	panic("implement me")
}

func (wcm *wrapperClientMock) registerContainer(ctx context.Context, payload containerPayload, result *containerPayload) error {
	wcm.requestCounts[mockAPIResourceContainer]++

	panic("implement me")
}

func (wcm *wrapperClientMock) listDiagnostics(context.Context) ([]RemoteDiagnostic, error) {
	wcm.requestCounts[mockAPIGloutonDiagnostic]++

	return slices.Clone(wcm.data.gloutondiagnostics), nil
}

func (wcm *wrapperClientMock) uploadDiagnostic(ctx context.Context, contentType string, content io.Reader) error {
	wcm.requestCounts[mockAPIGloutonDiagnostic]++

	panic("implement me")
}

func (wcm *wrapperClientMock) listFacts(context.Context) ([]types.AgentFact, error) {
	wcm.requestCounts[mockAPIResourceAgentFact]++

	return slices.Clone(wcm.data.agentfacts), nil
}

func (wcm *wrapperClientMock) registerFact(_ context.Context, payload any, result *types.AgentFact) error {
	wcm.requestCounts[mockAPIResourceAgentFact]++

	err := newMapstructJSONDecoder(result, nil).Decode(payload)
	if err != nil {
		return err
	}

	wcm.data.agentfacts = append(wcm.data.agentfacts, *result)

	return nil
}

func (wcm *wrapperClientMock) deleteFact(ctx context.Context, id string) error {
	wcm.requestCounts[mockAPIResourceAgentFact]++

	panic("implement me")
}

func (wcm *wrapperClientMock) updateMetric(_ context.Context, id string, payload any, _ string) error {
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

func (wcm *wrapperClientMock) listActiveMetrics(_ context.Context, active bool, filter func(payload metricPayload) bool) (map[string]types.Metric, error) {
	wcm.requestCounts[mockAPIResourceMetric]++

	metrics := wcm.data.metrics.filterResources(func(m metricW) bool {
		return m.Active == active && filter(m.metricPayload)
	})
	result := make(map[string]types.Metric, len(metrics))

	for _, m := range metrics {
		result[m.ID] = m.Metric
	}

	return result, nil
}

func (wcm *wrapperClientMock) countInactiveMetrics(context.Context) (int, error) {
	wcm.requestCounts[mockAPIResourceMetric]++

	return len(wcm.data.metrics.filterResources(func(m metricW) bool { return !m.Active })), nil
}

func (wcm *wrapperClientMock) listMetricsBy(_ context.Context, params url.Values, filter func(payload metricPayload) bool) (map[string]types.Metric, error) {
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
		return filter(m.metricPayload)
	})

	result := make(map[string]types.Metric, len(metrics))
	for _, m := range metrics {
		result[m.ID] = m.Metric
	}

	return result, nil
}

func (wcm *wrapperClientMock) getMetricByID(ctx context.Context, id string) (metricPayload, error) {
	wcm.requestCounts[mockAPIResourceMetric]++

	panic("implement me")
}

func (wcm *wrapperClientMock) registerMetric(_ context.Context, payload metricPayload, result *metricPayload) error {
	wcm.requestCounts[mockAPIResourceMetric]++

	err := newMapstructJSONDecoder(result, nil).Decode(payload)
	if err != nil {
		return err
	}

	result.ID = strconv.Itoa(len(wcm.data.metrics) + 1)
	wcm.data.metrics = append(wcm.data.metrics, metricW{metricPayload: *result, Active: true})

	return nil
}

func (wcm *wrapperClientMock) deleteMetric(ctx context.Context, id string) error {
	wcm.requestCounts[mockAPIResourceMetric]++

	panic("implement me")
}

func (wcm *wrapperClientMock) deactivateMetric(_ context.Context, id string) error {
	wcm.requestCounts[mockAPIResourceMetric]++

	predicate := func(m metricW) bool { return m.ID == id }

	metric := wcm.data.metrics.findResource(predicate)
	if metric == nil {
		return fmt.Errorf("metric with id %q %w", id, errNotFound)
	}

	metric.Active = false

	return nil
}

func (wcm *wrapperClientMock) listMonitors(context.Context) ([]types.Monitor, error) {
	wcm.requestCounts[mockAPIResourceService]++

	return slices.Clone(wcm.data.monitors), nil
}

func (wcm *wrapperClientMock) getMonitorByID(ctx context.Context, id string) (types.Monitor, error) {
	wcm.requestCounts[mockAPIResourceService]++

	panic("implement me")
}

func (wcm *wrapperClientMock) listServices(context.Context, string) ([]types.Service, error) {
	wcm.requestCounts[mockAPIResourceService]++

	return slices.Clone(wcm.data.services), nil
}

func (wcm *wrapperClientMock) updateService(_ context.Context, id string, payload servicePayload) (types.Service, error) {
	wcm.requestCounts[mockAPIResourceService]++

	service := wcm.data.services.findResource(func(s types.Service) bool { return s.ID == id })
	if service == nil {
		return types.Service{}, fmt.Errorf("service with id %q %w", id, errNotFound)
	}

	return *service, newMapstructJSONDecoder(service, nil).Decode(payload)
}

func (wcm *wrapperClientMock) registerService(_ context.Context, payload servicePayload) (types.Service, error) {
	wcm.requestCounts[mockAPIResourceService]++

	wcm.data.services = append(wcm.data.services, payload.Service)

	return payload.Service, nil
}

func (wcm *wrapperClientMock) registerSNMPAgent(ctx context.Context, payload payloadAgent) (types.Agent, error) {
	wcm.requestCounts[mockAPIResourceAgent]++

	panic("implement me")
}

func (wcm *wrapperClientMock) updateAgentLastDuplicationDate(ctx context.Context, agentID string, lastDuplicationDate time.Time) error {
	wcm.requestCounts[mockAPIResourceAgent]++

	panic("implement me")
}

func (wcm *wrapperClientMock) registerVSphereAgent(ctx context.Context, payload payloadAgent) (types.Agent, error) {
	wcm.requestCounts[mockAPIResourceAgent]++

	panic("implement me")
}

func (wcm *wrapperClientMock) AssertCallsPerResource(t *testing.T, resource string, expected int) {
	t.Helper()

	if count := wcm.requestCounts[resource]; count != expected {
		t.Errorf("Had %d requests on resource %s, want %d", count, resource, expected)
	}
}
