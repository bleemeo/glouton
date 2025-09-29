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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
	gloutonTypes "github.com/bleemeo/glouton/types"
)

const (
	accountConfigFields = "id,name,live_process_resolution,live_process,docker_integration,snmp_integration,vsphere_integration,number_of_custom_metrics,suspended"
	agentConfigFields   = "id,account_config,agent_type,metrics_allowlist,metrics_resolution"
	agentFactFields     = "id,agent,key,value"
	agentFields         = "id,account,agent_type,created_at,current_config,display_name,fqdn,is_cluster_leader,next_config_at,tags"
	agentTypeFields     = "id,name,display_name"
	applicationFields   = "id,name,tag"
	configItemFields    = "id,agent,key,value,priority,source,path,type"
	diagnosticFields    = "name"
	monitorFields       = "id,account_config,agent,created_at,monitor_url,monitor_expected_content,monitor_expected_response_code,monitor_unexpected_content,monitor_ca_file,monitor_headers"
	serviceFields       = "id,account_config,label,instance,listen_addresses,exe_path,tags,active,created_at"
)

func (cl *wrapperClient) GetGlobalInfo(ctx context.Context) (bleemeoTypes.GlobalInfo, error) {
	var globalInfo bleemeoTypes.GlobalInfo

	statusCode, err := cl.Do(ctx, http.MethodGet, "/v1/info/", nil, false, nil, &globalInfo)
	if err != nil {
		return bleemeoTypes.GlobalInfo{}, err
	}

	if statusCode != http.StatusOK {
		logger.V(2).Printf("Couldn't retrieve global information, got HTTP status code %d", statusCode)
	}

	return globalInfo, nil
}

func (cl *wrapperClient) RegisterSelf(ctx context.Context, accountID, password, initialServerGroupName, name, fqdn, registrationKey string) (id string, err error) {
	reqBody, err := bleemeo.JSONReaderFrom(map[string]string{
		"account":                   accountID,
		"initial_password":          password,
		"initial_server_group_name": initialServerGroupName,
		"display_name":              name,
		"fqdn":                      fqdn,
	})
	if err != nil {
		return "", err
	}

	req, err := cl.client.ParseRequest(http.MethodPost, bleemeo.ResourceAgent, nil, nil, reqBody)
	if err != nil {
		return "", err
	}

	req.SetBasicAuth(accountID+"@bleemeo.com", registrationKey)

	resp, err := cl.client.DoRequest(ctx, req, false)
	if err != nil {
		return "", err
	}

	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusCreated {
		return "", parseRegistrationError(resp.StatusCode, resp.Body)
	}

	var objectID struct {
		ID string `json:"id"`
	}

	err = json.NewDecoder(resp.Body).Decode(&objectID)
	if err != nil {
		return "", err
	}

	return objectID.ID, nil
}

func parseRegistrationError(code int, body io.Reader) error {
	content, err := io.ReadAll(io.LimitReader(body, 1024))
	if err != nil {
		return fmt.Errorf("%w: response code %d - can't read response: %v", errRegistrationFailed, code, err)
	}

	// apiRespList may represent a 400 error
	var apiRespList []string

	dec := json.NewDecoder(bytes.NewReader(content))
	dec.DisallowUnknownFields()

	err = dec.Decode(&apiRespList)
	if err == nil {
		return fmt.Errorf("%w: response code %d: %s", errRegistrationFailed, code, strings.Join(apiRespList, ", "))
	}

	// apiRespDetail may represent a 401 error
	var apiRespDetail struct {
		Detail string `json:"detail"`
	}

	dec = json.NewDecoder(bytes.NewReader(content))
	dec.DisallowUnknownFields()

	err = dec.Decode(&apiRespDetail)
	if err == nil {
		return fmt.Errorf("%w: response code %d: %s", errRegistrationFailed, code, apiRespDetail.Detail)
	}

	return fmt.Errorf("%w: response code %d: <%s>", errRegistrationFailed, code, content)
}

func (cl *wrapperClient) ListApplications(ctx context.Context) ([]bleemeoTypes.Application, error) {
	applications := make([]bleemeoTypes.Application, 0)
	params := url.Values{
		"fields": {applicationFields},
	}

	iter := cl.Iterator(ctx, bleemeo.ResourceApplication, params)
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

	err := cl.Create(ctx, bleemeo.ResourceApplication, app, applicationFields, &result)
	if err != nil {
		return bleemeoTypes.Application{}, err
	}

	return result, nil
}

func (cl *wrapperClient) ListAgentTypes(ctx context.Context) ([]bleemeoTypes.AgentType, error) {
	params := url.Values{
		"fields": {agentTypeFields},
	}

	var agentTypes []bleemeoTypes.AgentType

	iter := cl.Iterator(ctx, bleemeo.ResourceAgentType, params)
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
		"fields": {accountConfigFields},
	}

	var configs []bleemeoTypes.AccountConfig

	iter := cl.Iterator(ctx, bleemeo.ResourceAccountConfig, params)
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
		"fields": {agentConfigFields},
	}

	var configs []bleemeoTypes.AgentConfig

	iter := cl.Iterator(ctx, bleemeo.ResourceAgentConfig, params)
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

	iter := cl.Iterator(ctx, bleemeo.ResourceAgent, params)
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
		"fields": {configItemFields},
		"agent":  {agentID},
	}

	items := make([]bleemeoTypes.GloutonConfigItem, 0)

	iter := cl.Iterator(ctx, bleemeo.ResourceGloutonConfigItem, params)
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
	// There are no fields and no result, because it creates a LIST of GloutonConfigItem.
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

	iter := cl.Iterator(ctx, bleemeo.ResourceContainer, params)
	for iter.Next(ctx) {
		var container bleemeoapi.ContainerPayload

		if err := json.Unmarshal(iter.At(), &container); err != nil {
			continue
		}

		containers = append(containers, container.Container)
	}

	return containers, iter.Err()
}

func (cl *wrapperClient) UpdateContainer(ctx context.Context, id string, payload any) (bleemeoapi.ContainerPayload, error) {
	var result bleemeoapi.ContainerPayload

	err := cl.Update(ctx, bleemeo.ResourceContainer, id, payload, containerRegisterFields, &result)

	return result, err
}

func (cl *wrapperClient) RegisterContainer(ctx context.Context, payload bleemeoapi.ContainerPayload) (bleemeoapi.ContainerPayload, error) {
	var result bleemeoapi.ContainerPayload

	err := cl.Create(ctx, bleemeo.ResourceContainer, payload, containerRegisterFields, &result)

	return result, err
}

func (cl *wrapperClient) ListDiagnostics(ctx context.Context) ([]bleemeoapi.RemoteDiagnostic, error) {
	params := url.Values{
		"fields": {diagnosticFields},
	}

	var diagnostics []bleemeoapi.RemoteDiagnostic

	iter := cl.Iterator(ctx, bleemeo.ResourceGloutonDiagnostic, params)
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

func (cl *wrapperClient) UploadDiagnostic(ctx context.Context, contentType string, content io.Reader) (disableDelay time.Duration, err error) {
	resp, reqErr := cl.DoWithBody(ctx, bleemeo.ResourceGloutonDiagnostic, contentType, content)
	if reqErr != nil {
		return 0, reqErr
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		return 0, nil
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		delaySecond, err := strconv.Atoi(resp.Header.Get("Retry-After"))
		if err == nil {
			return time.Duration(delaySecond) * time.Second, nil
		}
	}

	return 0, fmt.Errorf("%w: status %d %s", errUploadFailed, resp.StatusCode, resp.Status)
}

func (cl *wrapperClient) ListFacts(ctx context.Context) ([]bleemeoTypes.AgentFact, error) {
	params := url.Values{
		"fields": {agentFactFields},
	}

	var facts []bleemeoTypes.AgentFact

	iter := cl.Iterator(ctx, bleemeo.ResourceAgentFact, params)
	for iter.Next(ctx) {
		var fact bleemeoTypes.AgentFact

		if err := json.Unmarshal(iter.At(), &fact); err != nil {
			continue
		}

		facts = append(facts, fact)
	}

	return facts, iter.Err()
}

func (cl *wrapperClient) RegisterFact(ctx context.Context, payload bleemeoTypes.AgentFact) (bleemeoTypes.AgentFact, error) {
	var result bleemeoTypes.AgentFact

	err := cl.Create(ctx, bleemeo.ResourceAgentFact, payload, agentFactFields, &result)

	return result, err
}

func (cl *wrapperClient) DeleteFact(ctx context.Context, id string) error {
	return cl.Delete(ctx, bleemeo.ResourceAgentFact, id)
}

func (cl *wrapperClient) UpdateMetric(ctx context.Context, id string, payload any, fields string) error {
	return cl.Update(ctx, bleemeo.ResourceMetric, id, payload, fields, nil)
}

func (cl *wrapperClient) ListActiveMetrics(ctx context.Context) ([]bleemeoapi.MetricPayload, error) {
	return cl.listMetrics(ctx, true, nil)
}

func (cl *wrapperClient) ListInactiveMetrics(ctx context.Context, stopSearchingPredicate func(labelsText string) bool) ([]bleemeoapi.MetricPayload, error) {
	return cl.listMetrics(ctx, false, stopSearchingPredicate)
}

func (cl *wrapperClient) listMetrics(ctx context.Context, active bool, stopSearchingPredicate func(labelsText string) bool) ([]bleemeoapi.MetricPayload, error) {
	params := url.Values{
		"fields": {metricFields},
	}

	if active {
		params.Set("active", stringTrue)
	} else {
		params.Set("active", stringFalse)
	}

	metrics := make([]bleemeoapi.MetricPayload, 0)

	iter := cl.Iterator(ctx, bleemeo.ResourceMetric, params)
	for iter.Next(ctx) {
		var metric bleemeoapi.MetricPayload

		if err := json.Unmarshal(iter.At(), &metric); err != nil {
			continue
		}

		metrics = append(metrics, metric)

		if stopSearchingPredicate != nil {
			labelsText := metric.LabelsText
			if labelsText == "" {
				labelsText = gloutonTypes.LabelsToText(metric.Labels)
			}

			if stopSearchingPredicate(labelsText) {
				break
			}
		}
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

	iter := cl.Iterator(ctx, bleemeo.ResourceMetric, params)
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

	err := cl.Get(ctx, bleemeo.ResourceMetric, id, metricFields, &metric)

	return metric, err
}

func (cl *wrapperClient) RegisterMetric(ctx context.Context, payload bleemeoapi.MetricPayload) (bleemeoapi.MetricPayload, error) {
	var result bleemeoapi.MetricPayload

	err := cl.Create(ctx, bleemeo.ResourceMetric, payload, metricFields, &result)

	return result, err
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

	iter := cl.Iterator(ctx, bleemeo.ResourceService, params)
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

	err := cl.Get(ctx, bleemeo.ResourceService, id, monitorFields, &result)

	return result, err
}

func (cl *wrapperClient) ListServices(ctx context.Context, agentID string, fields string) ([]bleemeoTypes.Service, error) {
	params := url.Values{
		"agent":  {agentID},
		"fields": {fields},
	}

	var services []bleemeoTypes.Service

	iter := cl.Iterator(ctx, bleemeo.ResourceService, params)
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

	err := cl.Update(ctx, bleemeo.ResourceService, id, payload, fields, &result)

	return result, err
}

func (cl *wrapperClient) RegisterService(ctx context.Context, payload bleemeoapi.ServicePayload) (bleemeoTypes.Service, error) {
	var result bleemeoTypes.Service

	err := cl.Create(ctx, bleemeo.ResourceService, payload, serviceFields, &result)

	return result, err
}

func (cl *wrapperClient) DeleteService(ctx context.Context, id string) error {
	err := cl.Delete(ctx, bleemeo.ResourceService, id)
	if err != nil && IsNotFound(err) {
		return nil
	}

	return err
}

func (cl *wrapperClient) RegisterSNMPAgent(ctx context.Context, payload bleemeoapi.AgentPayload) (bleemeoTypes.Agent, error) {
	var result bleemeoTypes.Agent

	err := cl.Create(ctx, bleemeo.ResourceAgent, payload, agentFields, &result)

	return result, err
}

func (cl *wrapperClient) UpdateAgentLastDuplicationDate(ctx context.Context, agentID string, lastDuplicationDate time.Time) error {
	payload := map[string]time.Time{"last_duplication_date": lastDuplicationDate}

	return cl.Update(ctx, bleemeo.ResourceAgent, agentID, payload, "last_duplication_date", nil)
}

func (cl *wrapperClient) RegisterVSphereAgent(ctx context.Context, payload bleemeoapi.AgentPayload) (bleemeoTypes.Agent, error) {
	var result bleemeoTypes.Agent

	err := cl.Create(ctx, bleemeo.ResourceAgent, payload, agentFields, &result)

	return result, err
}
