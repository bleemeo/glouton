package synchronizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
)

var errAgentConfigNotSupported = errors.New("Bleemeo API doesn't support AgentConfig")

func (cl *wrapperClient) listAgentTypes(ctx context.Context) ([]types.AgentType, error) {
	params := url.Values{
		"fields": {"id,name,display_name"},
	}

	var agentTypes []types.AgentType

	iter := cl.Iterator(bleemeo.ResourceAgentType, params)
	for iter.Next(ctx) {
		var agentType types.AgentType

		if err := json.Unmarshal(iter.At(), &agentType); err != nil {
			continue
		}

		agentTypes = append(agentTypes, agentType)
	}

	return agentTypes, iter.Err()
}

func (cl *wrapperClient) listAccountConfigs(ctx context.Context) ([]types.AccountConfig, error) {
	params := url.Values{
		"fields": {"id,name,live_process_resolution,live_process,docker_integration,snmp_integration,vsphere_integration,number_of_custom_metrics,suspended"},
	}

	var configs []types.AccountConfig

	iter := cl.Iterator(bleemeo.ResourceAccountConfig, params)
	for iter.Next(ctx) {
		var config types.AccountConfig

		if err := json.Unmarshal(iter.At(), &config); err != nil {
			continue
		}

		configs = append(configs, config)
	}

	return configs, iter.Err()
}

func (cl *wrapperClient) listAgentConfigs(ctx context.Context) ([]types.AgentConfig, error) {
	params := url.Values{
		"fields": {"id,account_config,agent_type,metrics_allowlist,metrics_resolution"},
	}

	var configs []types.AgentConfig

	iter := cl.Iterator(bleemeo.ResourceAgentConfig, params)
	for iter.Next(ctx) {
		var config types.AgentConfig

		if err := json.Unmarshal(iter.At(), &config); err != nil {
			continue
		}

		configs = append(configs, config)
	}

	if iter.Err() != nil {
		if apiErr := new(bleemeo.APIError); errors.As(iter.Err(), &apiErr) {
			mediatype, _, err := mime.ParseMediaType(apiErr.ContentType)
			if err == nil && mediatype == "text/html" {
				return nil, errAgentConfigNotSupported
			}
		}

		return nil, iter.Err()
	}

	return configs, nil
}

func (cl *wrapperClient) listAgents(ctx context.Context) ([]types.Agent, error) {
	params := url.Values{
		"fields": {agentFields},
	}

	var agents []types.Agent

	iter := cl.Iterator(bleemeo.ResourceAgent, params)
	for iter.Next(ctx) {
		var agent types.Agent

		if err := json.Unmarshal(iter.At(), &agent); err != nil {
			continue
		}

		agents = append(agents, agent)
	}

	return agents, iter.Err()
}

func (cl *wrapperClient) updateAgent(ctx context.Context, id string, data any) (types.Agent, error) {
	var agent types.Agent

	err := cl.Update(ctx, bleemeo.ResourceAgent, id, data, agentFields, &agent)

	return agent, err
}

func (cl *wrapperClient) registerGloutonConfigItems(ctx context.Context, items []types.GloutonConfigItem) error {
	return cl.Create(ctx, bleemeo.ResourceGloutonConfigItem, items, "", nil)
}

func (cl *wrapperClient) deleteGloutonConfigItem(ctx context.Context, id string) error {
	return cl.Delete(ctx, bleemeo.ResourceGloutonConfigItem, id)
}

func (cl *wrapperClient) listContainers(ctx context.Context, agentID string) ([]types.Container, error) {
	params := url.Values{
		"host":   {agentID},
		"fields": {containerCacheFields},
	}

	var containers []types.Container

	iter := cl.Iterator(bleemeo.ResourceContainer, params)
	for iter.Next(ctx) {
		var container containerPayload

		if err := json.Unmarshal(iter.At(), &container); err != nil {
			continue
		}

		containers = append(containers, container.Container)
	}

	return containers, iter.Err()
}

func (cl *wrapperClient) updateContainer(ctx context.Context, id string, payload any, result *containerPayload) error {
	return cl.Update(ctx, bleemeo.ResourceContainer, id, payload, containerRegisterFields, result)
}

func (cl *wrapperClient) registerContainer(ctx context.Context, payload containerPayload, result *containerPayload) error {
	return cl.Create(ctx, bleemeo.ResourceContainer, payload, containerRegisterFields, &result)
}

func (cl *wrapperClient) listDiagnostics(ctx context.Context) ([]RemoteDiagnostic, error) {
	var diagnostics []RemoteDiagnostic

	iter := cl.Iterator(bleemeo.ResourceGloutonDiagnostic, nil)
	for iter.Next(ctx) {
		var remoteDiagnostic RemoteDiagnostic

		if err := json.Unmarshal(iter.At(), &remoteDiagnostic); err != nil {
			logger.V(2).Printf("Failed to unmarshal diagnostic: %v", err)

			continue
		}

		diagnostics = append(diagnostics, remoteDiagnostic)
	}

	return diagnostics, iter.Err()
}

func (cl *wrapperClient) uploadDiagnostic(ctx context.Context, contentType string, content io.Reader) error {
	statusCode, reqErr := cl.DoWithBody(ctx, bleemeo.ResourceGloutonDiagnostic, contentType, content)
	if reqErr != nil {
		return reqErr
	}

	if statusCode != http.StatusCreated {
		return fmt.Errorf("%w: status %d %s", errUploadFailed, statusCode, http.StatusText(statusCode))
	}

	return nil
}

func (cl *wrapperClient) listFacts(ctx context.Context) ([]types.AgentFact, error) {
	var facts []types.AgentFact

	iter := cl.Iterator(bleemeo.ResourceAgentFact, nil)
	for iter.Next(ctx) {
		var fact types.AgentFact

		if err := json.Unmarshal(iter.At(), &fact); err != nil {
			continue
		}

		facts = append(facts, fact)
	}

	return facts, iter.Err()
}

func (cl *wrapperClient) registerFact(ctx context.Context, payload any, result *types.AgentFact) error {
	return cl.Create(ctx, bleemeo.ResourceAgentFact, payload, "", result)
}

func (cl *wrapperClient) deleteFact(ctx context.Context, id string) error {
	return cl.Delete(ctx, bleemeo.ResourceAgentFact, id)
}

func (cl *wrapperClient) updateMetric(ctx context.Context, id string, payload any, fields string) error {

	return cl.Update(ctx, bleemeo.ResourceMetric, id, payload, fields, nil)
}

func (cl *wrapperClient) listActiveMetrics(ctx context.Context, active bool, filter func(payload metricPayload) bool) (map[string]types.Metric, error) {
	params := url.Values{
		"fields": {metricFields},
	}

	if active {
		params.Set("active", stringFalse)
	} else {
		params.Set("active", stringTrue)
	}

	metricsByUUID := make(map[string]types.Metric)

	iter := cl.Iterator(bleemeo.ResourceMetric, params)
	for iter.Next(ctx) {
		var metric metricPayload

		if err := json.Unmarshal(iter.At(), &metric); err != nil {
			continue
		}

		if !filter(metric) {
			continue
		}

		metricsByUUID[metric.ID] = metric.metricFromAPI(metricsByUUID[metric.ID].FirstSeenAt)
	}

	return metricsByUUID, iter.Err()
}

func (cl *wrapperClient) countInactiveMetrics(ctx context.Context) (int, error) {
	return cl.Count(
		ctx,
		bleemeo.ResourceMetric,
		url.Values{
			"fields": {"id"},
			"active": {"False"},
		},
	)
}

func (cl *wrapperClient) listMetricsBy(ctx context.Context, params url.Values, filter func(payload metricPayload) bool) (map[string]types.Metric, error) {
	metricsByUUID := make(map[string]types.Metric)

	iter := cl.Iterator(bleemeo.ResourceMetric, params)
	for iter.Next(ctx) {
		var metric metricPayload

		if err := json.Unmarshal(iter.At(), &metric); err != nil {
			continue
		}

		if !filter(metric) {
			continue
		}

		metricsByUUID[metric.ID] = metric.metricFromAPI(metricsByUUID[metric.ID].FirstSeenAt)
	}

	return metricsByUUID, iter.Err()
}

func (cl *wrapperClient) getMetricByID(ctx context.Context, id string) (metricPayload, error) {
	var metric metricPayload

	return metric, cl.Get(ctx, bleemeo.ResourceMetric, id, metricFields, &metric)
}

func (cl *wrapperClient) registerMetric(ctx context.Context, payload metricPayload, result *metricPayload) error {
	return cl.Create(ctx, bleemeo.ResourceMetric, payload, metricFields, result)
}

func (cl *wrapperClient) deleteMetric(ctx context.Context, id string) error {
	return cl.Delete(ctx, bleemeo.ResourceMetric, id)
}

func (cl *wrapperClient) deactivateMetric(ctx context.Context, id string) error {
	return cl.Update(ctx, bleemeo.ResourceMetric, id, map[string]string{"active": stringFalse}, "active", nil)
}

func (cl *wrapperClient) listMonitors(ctx context.Context) ([]types.Monitor, error) {
	params := url.Values{
		"monitor": {"true"},
		"active":  {"true"},
		"fields":  {monitorFields},
	}

	var monitors []types.Monitor

	iter := cl.Iterator(bleemeo.ResourceService, params)
	for iter.Next(ctx) {
		var monitor types.Monitor

		if err := json.Unmarshal(iter.At(), &monitor); err != nil {
			return nil, fmt.Errorf("%w %v", errCannotParse, string(iter.At()))
		}

		monitors = append(monitors, monitor)
	}

	return monitors, iter.Err()
}

func (cl *wrapperClient) getMonitorByID(ctx context.Context, id string) (types.Monitor, error) {
	var result types.Monitor

	return result, cl.Get(ctx, bleemeo.ResourceService, id, monitorFields, &result)
}

func (cl *wrapperClient) listServices(ctx context.Context, agentID string) ([]types.Service, error) {
	params := url.Values{
		"agent":  {agentID},
		"fields": {"id,label,instance,listen_addresses,exe_path,stack,active,created_at"},
	}

	var services []types.Service

	iter := cl.Iterator(bleemeo.ResourceService, params)
	for iter.Next(ctx) {
		var service types.Service

		if err := json.Unmarshal(iter.At(), &service); err != nil {
			continue
		}

		services = append(services, service)
	}

	return services, iter.Err()
}

func (cl *wrapperClient) updateService(ctx context.Context, id string, payload servicePayload) (types.Service, error) {
	var result types.Service

	return result, cl.Update(ctx, bleemeo.ResourceService, id, payload, serviceFields, &result)
}

func (cl *wrapperClient) registerService(ctx context.Context, payload servicePayload) (types.Service, error) {
	var result types.Service

	return result, cl.Create(ctx, bleemeo.ResourceService, payload, serviceFields, &result)
}

func (cl *wrapperClient) registerSNMPAgent(ctx context.Context, payload payloadAgent) (types.Agent, error) {
	var result types.Agent

	return result, cl.Create(ctx, bleemeo.ResourceAgent, payload, snmpAgentFields, &result)
}

func (cl *wrapperClient) updateAgentLastDuplicationDate(ctx context.Context, agentID string, lastDuplicationDate time.Time) error {
	payload := map[string]time.Time{"last_duplication_date": lastDuplicationDate}

	return cl.Update(ctx, bleemeo.ResourceAgent, agentID, payload, "last_duplication_date", nil)
}

func (cl *wrapperClient) registerVSphereAgent(ctx context.Context, payload payloadAgent) (types.Agent, error) {
	var result types.Agent

	return result, cl.Create(ctx, bleemeo.ResourceAgent, payload, vSphereAgentFields, &result)
}

func (cl *wrapperClient) deleteAgent(ctx context.Context, id string) error {
	return cl.Delete(ctx, bleemeo.ResourceAgent, id)
}
