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

package synchronizer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
)

func (cl *wrapperClient) ListApplications(ctx context.Context) ([]bleemeoTypes.Application, error) {
	applications := make([]bleemeoTypes.Application, 0)

	iter := cl.Iterator(bleemeo.ResourceApplication, nil)
	for iter.Next(ctx) {
		var application bleemeoTypes.Application

		if err := json.Unmarshal(iter.At(), &application); err != nil {
			continue
		}

		applications = append(applications, application)
	}

	return applications, iter.Err()
}

func (cl *wrapperClient) CreateApplication(ctx context.Context, app bleemeoTypes.Application) (bleemeoTypes.Application, error) {
	var result bleemeoTypes.Application

	app.ID = "" // ID isn't allowed in creation

	err := cl.Create(ctx, bleemeo.ResourceApplication, app, "id,name,tag", &result)
	if err != nil {
		return bleemeoTypes.Application{}, err
	}

	return result, nil
}

func (cl *wrapperClient) ListAgentTypes(ctx context.Context) ([]bleemeoTypes.AgentType, error) {
	params := url.Values{
		"fields": {"id,name,display_name"},
	}

	var agentTypes []bleemeoTypes.AgentType

	iter := cl.Iterator(bleemeo.ResourceAgentType, params)
	for iter.Next(ctx) {
		var agentType bleemeoTypes.AgentType

		if err := json.Unmarshal(iter.At(), &agentType); err != nil {
			continue
		}

		agentTypes = append(agentTypes, agentType)
	}

	return agentTypes, iter.Err()
}

func (cl *wrapperClient) ListAccountConfigs(ctx context.Context) ([]bleemeoTypes.AccountConfig, error) {
	params := url.Values{
		"fields": {"id,name,live_process_resolution,live_process,docker_integration,snmp_integration,vsphere_integration,number_of_custom_metrics,suspended"},
	}

	var configs []bleemeoTypes.AccountConfig

	iter := cl.Iterator(bleemeo.ResourceAccountConfig, params)
	for iter.Next(ctx) {
		var config bleemeoTypes.AccountConfig

		if err := json.Unmarshal(iter.At(), &config); err != nil {
			continue
		}

		configs = append(configs, config)
	}

	return configs, iter.Err()
}

func (cl *wrapperClient) ListAgentConfigs(ctx context.Context) ([]bleemeoTypes.AgentConfig, error) {
	params := url.Values{
		"fields": {"id,account_config,agent_type,metrics_allowlist,metrics_resolution"},
	}

	var configs []bleemeoTypes.AgentConfig

	iter := cl.Iterator(bleemeo.ResourceAgentConfig, params)
	for iter.Next(ctx) {
		var config bleemeoTypes.AgentConfig

		if err := json.Unmarshal(iter.At(), &config); err != nil {
			continue
		}

		configs = append(configs, config)
	}

	return configs, iter.Err()
}

func (cl *wrapperClient) ListAgents(ctx context.Context) ([]bleemeoTypes.Agent, error) {
	params := url.Values{
		"fields": {agentFields},
	}

	var agents []bleemeoTypes.Agent

	iter := cl.Iterator(bleemeo.ResourceAgent, params)
	for iter.Next(ctx) {
		var agent bleemeoTypes.Agent

		if err := json.Unmarshal(iter.At(), &agent); err != nil {
			continue
		}

		agents = append(agents, agent)
	}

	return agents, iter.Err()
}

func (cl *wrapperClient) UpdateAgent(ctx context.Context, id string, data any) (bleemeoTypes.Agent, error) {
	var agent bleemeoTypes.Agent

	err := cl.Update(ctx, bleemeo.ResourceAgent, id, data, agentFields, &agent)

	return agent, err
}

func (cl *wrapperClient) DeleteAgent(ctx context.Context, id string) error {
	return cl.Delete(ctx, bleemeo.ResourceAgent, id)
}

func (cl *wrapperClient) ListGloutonConfigItems(ctx context.Context, agentID string) ([]bleemeoTypes.GloutonConfigItem, error) {
	params := url.Values{
		"fields": {"id,agent,key,value,priority,source,path,type"},
		"agent":  {agentID},
	}

	items := make([]bleemeoTypes.GloutonConfigItem, 0)

	iter := cl.Iterator(bleemeo.ResourceGloutonConfigItem, params)
	for iter.Next(ctx) {
		var item bleemeoTypes.GloutonConfigItem

		if err := json.Unmarshal(iter.At(), &item); err != nil {
			logger.V(2).Printf("Failed to unmarshal config item: %v", err)

			continue
		}

		items = append(items, item)
	}

	return items, iter.Err()
}

func (cl *wrapperClient) RegisterGloutonConfigItems(ctx context.Context, items []bleemeoTypes.GloutonConfigItem) error {
	return cl.Create(ctx, bleemeo.ResourceGloutonConfigItem, items, "", nil)
}

func (cl *wrapperClient) DeleteGloutonConfigItem(ctx context.Context, id string) error {
	return cl.Delete(ctx, bleemeo.ResourceGloutonConfigItem, id)
}

func (cl *wrapperClient) ListContainers(ctx context.Context, agentID string) ([]bleemeoTypes.Container, error) {
	params := url.Values{
		"host":   {agentID},
		"fields": {containerCacheFields},
	}

	var containers []bleemeoTypes.Container

	iter := cl.Iterator(bleemeo.ResourceContainer, params)
	for iter.Next(ctx) {
		var container bleemeoapi.ContainerPayload

		if err := json.Unmarshal(iter.At(), &container); err != nil {
			continue
		}

		containers = append(containers, container.Container)
	}

	return containers, iter.Err()
}

func (cl *wrapperClient) UpdateContainer(ctx context.Context, id string, payload any, result *bleemeoapi.ContainerPayload) error {
	return cl.Update(ctx, bleemeo.ResourceContainer, id, payload, containerRegisterFields, result)
}

func (cl *wrapperClient) RegisterContainer(ctx context.Context, payload bleemeoapi.ContainerPayload, result *bleemeoapi.ContainerPayload) error {
	return cl.Create(ctx, bleemeo.ResourceContainer, payload, containerRegisterFields, &result)
}

func (cl *wrapperClient) ListDiagnostics(ctx context.Context) ([]bleemeoapi.RemoteDiagnostic, error) {
	var diagnostics []bleemeoapi.RemoteDiagnostic

	iter := cl.Iterator(bleemeo.ResourceGloutonDiagnostic, nil)
	for iter.Next(ctx) {
		var remoteDiagnostic bleemeoapi.RemoteDiagnostic

		if err := json.Unmarshal(iter.At(), &remoteDiagnostic); err != nil {
			logger.V(2).Printf("Failed to unmarshal diagnostic: %v", err)

			continue
		}

		diagnostics = append(diagnostics, remoteDiagnostic)
	}

	return diagnostics, iter.Err()
}

func (cl *wrapperClient) UploadDiagnostic(ctx context.Context, contentType string, content io.Reader) error {
	statusCode, reqErr := cl.DoWithBody(ctx, bleemeo.ResourceGloutonDiagnostic, contentType, content)
	if reqErr != nil {
		return reqErr
	}

	if statusCode != http.StatusCreated {
		return fmt.Errorf("%w: status %d %s", errUploadFailed, statusCode, http.StatusText(statusCode))
	}

	return nil
}

func (cl *wrapperClient) ListFacts(ctx context.Context) ([]bleemeoTypes.AgentFact, error) {
	var facts []bleemeoTypes.AgentFact

	iter := cl.Iterator(bleemeo.ResourceAgentFact, nil)
	for iter.Next(ctx) {
		var fact bleemeoTypes.AgentFact

		if err := json.Unmarshal(iter.At(), &fact); err != nil {
			continue
		}

		facts = append(facts, fact)
	}

	return facts, iter.Err()
}

func (cl *wrapperClient) RegisterFact(ctx context.Context, payload any, result *bleemeoTypes.AgentFact) error {
	return cl.Create(ctx, bleemeo.ResourceAgentFact, payload, "", result)
}

func (cl *wrapperClient) DeleteFact(ctx context.Context, id string) error {
	return cl.Delete(ctx, bleemeo.ResourceAgentFact, id)
}

func (cl *wrapperClient) UpdateMetric(ctx context.Context, id string, payload any, fields string) error {
	return cl.Update(ctx, bleemeo.ResourceMetric, id, payload, fields, nil)
}

func (cl *wrapperClient) ListActiveMetrics(ctx context.Context, active bool) ([]bleemeoapi.MetricPayload, error) {
	params := url.Values{
		"fields": {metricFields},
	}

	if active {
		params.Set("active", stringFalse)
	} else {
		params.Set("active", stringTrue)
	}

	metrics := make([]bleemeoapi.MetricPayload, 0)

	iter := cl.Iterator(bleemeo.ResourceMetric, params)
	for iter.Next(ctx) {
		var metric bleemeoapi.MetricPayload

		if err := json.Unmarshal(iter.At(), &metric); err != nil {
			continue
		}

		metrics = append(metrics, metric)
	}

	return metrics, iter.Err()
}

func (cl *wrapperClient) CountInactiveMetrics(ctx context.Context) (int, error) {
	return cl.Count(
		ctx,
		bleemeo.ResourceMetric,
		url.Values{
			"fields": {"id"},
			"active": {"False"},
		},
	)
}

func (cl *wrapperClient) ListMetricsBy(ctx context.Context, params url.Values) (map[string]bleemeoTypes.Metric, error) {
	metricsByUUID := make(map[string]bleemeoTypes.Metric)

	iter := cl.Iterator(bleemeo.ResourceMetric, params)
	for iter.Next(ctx) {
		var metric bleemeoapi.MetricPayload

		if err := json.Unmarshal(iter.At(), &metric); err != nil {
			continue
		}

		metricsByUUID[metric.ID] = metricFromAPI(metric, metricsByUUID[metric.ID].FirstSeenAt)
	}

	return metricsByUUID, iter.Err()
}

func (cl *wrapperClient) GetMetricByID(ctx context.Context, id string) (bleemeoapi.MetricPayload, error) {
	var metric bleemeoapi.MetricPayload

	return metric, cl.Get(ctx, bleemeo.ResourceMetric, id, metricFields, &metric)
}

func (cl *wrapperClient) RegisterMetric(ctx context.Context, payload bleemeoapi.MetricPayload, result *bleemeoapi.MetricPayload) error {
	return cl.Create(ctx, bleemeo.ResourceMetric, payload, metricFields, result)
}

func (cl *wrapperClient) DeleteMetric(ctx context.Context, id string) error {
	return cl.Delete(ctx, bleemeo.ResourceMetric, id)
}

func (cl *wrapperClient) SetMetricActive(ctx context.Context, id string, active bool) error {
	var activeStr string

	if active {
		activeStr = stringTrue
	} else {
		activeStr = stringFalse
	}

	return cl.Update(ctx, bleemeo.ResourceMetric, id, map[string]string{"active": activeStr}, "active", nil)
}

func (cl *wrapperClient) ListMonitors(ctx context.Context) ([]bleemeoTypes.Monitor, error) {
	params := url.Values{
		"monitor": {"true"},
		"active":  {"true"},
		"fields":  {monitorFields},
	}

	var monitors []bleemeoTypes.Monitor

	iter := cl.Iterator(bleemeo.ResourceService, params)
	for iter.Next(ctx) {
		var monitor bleemeoTypes.Monitor

		if err := json.Unmarshal(iter.At(), &monitor); err != nil {
			return nil, fmt.Errorf("%w %v", errCannotParse, string(iter.At()))
		}

		monitors = append(monitors, monitor)
	}

	return monitors, iter.Err()
}

func (cl *wrapperClient) GetMonitorByID(ctx context.Context, id string) (bleemeoTypes.Monitor, error) {
	var result bleemeoTypes.Monitor

	return result, cl.Get(ctx, bleemeo.ResourceService, id, monitorFields, &result)
}

func (cl *wrapperClient) ListServices(ctx context.Context, agentID string, fields string) ([]bleemeoTypes.Service, error) {
	params := url.Values{
		"agent":  {agentID},
		"fields": {fields},
	}

	var services []bleemeoTypes.Service

	iter := cl.Iterator(bleemeo.ResourceService, params)
	for iter.Next(ctx) {
		var service bleemeoTypes.Service

		if err := json.Unmarshal(iter.At(), &service); err != nil {
			continue
		}

		services = append(services, service)
	}

	return services, iter.Err()
}

func (cl *wrapperClient) UpdateService(ctx context.Context, id string, payload bleemeoapi.ServicePayload, fields string) (bleemeoTypes.Service, error) {
	var result bleemeoTypes.Service

	return result, cl.Update(ctx, bleemeo.ResourceService, id, payload, fields, &result)
}

func (cl *wrapperClient) RegisterService(ctx context.Context, payload bleemeoapi.ServicePayload, fields string) (bleemeoTypes.Service, error) {
	var result bleemeoTypes.Service

	return result, cl.Create(ctx, bleemeo.ResourceService, payload, fields, &result)
}

func (cl *wrapperClient) RegisterSNMPAgent(ctx context.Context, payload bleemeoapi.AgentPayload) (bleemeoTypes.Agent, error) {
	var result bleemeoTypes.Agent

	return result, cl.Create(ctx, bleemeo.ResourceAgent, payload, snmpAgentFields, &result)
}

func (cl *wrapperClient) UpdateAgentLastDuplicationDate(ctx context.Context, agentID string, lastDuplicationDate time.Time) error {
	payload := map[string]time.Time{"last_duplication_date": lastDuplicationDate}

	return cl.Update(ctx, bleemeo.ResourceAgent, agentID, payload, "last_duplication_date", nil)
}

func (cl *wrapperClient) RegisterVSphereAgent(ctx context.Context, payload bleemeoapi.AgentPayload) (bleemeoTypes.Agent, error) {
	var result bleemeoTypes.Agent

	return result, cl.Create(ctx, bleemeo.ResourceAgent, payload, vSphereAgentFields, &result)
}
