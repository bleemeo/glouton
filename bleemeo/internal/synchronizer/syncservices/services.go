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

package syncservices

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/common"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/logger"
)

// serviceFields is the fields used on the API for a service, we always use all fields to
// make sure no Metric object is returned with some empty fields which could create bugs.
// Write (POST, PUT & PATCH) use account and agent field in addition. During reading we use filter to known the account/agent.
const (
	serviceReadFields  = "id,label,instance,listen_addresses,exe_path,active,tags,created_at"
	serviceWriteFields = serviceReadFields + ",account,agent"
)

type SyncServices struct {
	lastDenyReasonLogAt time.Time
}

func New() *SyncServices {
	return &SyncServices{}
}

func (s *SyncServices) Name() types.EntityName {
	return types.EntityService
}

func (s *SyncServices) EnabledInMaintenance() bool {
	return false
}

func (s *SyncServices) EnabledInSuspendedMode() bool {
	return false
}

func (s *SyncServices) PrepareExecution(_ context.Context, execution types.SynchronizationExecution) (types.EntitySynchronizerExecution, error) {
	r := &syncServicesExecution{
		currentExecution:     execution,
		parent:               s,
		previousKnownService: execution.Option().Cache.ServicesByUUID(),
	}

	return r, nil
}

type syncServicesExecution struct {
	currentExecution     types.SynchronizationExecution
	previousKnownService map[string]bleemeoTypes.Service
	parent               *SyncServices
}

func (s *syncServicesExecution) NeedSynchronization(_ context.Context) (bool, error) {
	if s.currentExecution == nil {
		return false, fmt.Errorf("%w: currentExecution is nil", types.ErrUnexpectedWorkflow)
	}

	_, lastDiscovery := s.currentExecution.Option().Discovery.GetLatestDiscovery()
	_, minDelayed := s.currentExecution.GlobalState().DelayedContainers()

	if s.currentExecution.LastSync().Before(lastDiscovery) || (!minDelayed.IsZero() && s.currentExecution.StartedAt().After(minDelayed)) {
		// Metrics registration may need services to be synced, trigger metrics synchronization to be sure any
		// possible blocked metrics get registered after service are registered
		s.currentExecution.RequestSynchronization(types.EntityMetric, false)

		return true, nil
	}

	return false, nil
}

func (s *syncServicesExecution) RefreshCache(ctx context.Context, syncType types.SyncType) error {
	if s.currentExecution == nil {
		return fmt.Errorf("%w: currentExecution is nil", types.ErrUnexpectedWorkflow)
	}

	if s.currentExecution.GlobalState().SuccessiveErrors() == 3 {
		// After 3 error, try to force a full synchronization to see if it solve the issue.
		syncType = types.SyncTypeForceCacheRefresh
	}

	if syncType == types.SyncTypeForceCacheRefresh {
		err := serviceUpdateList(ctx, s.currentExecution)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *syncServicesExecution) SyncRemoteAndLocal(ctx context.Context, syncType types.SyncType) error {
	_ = syncType

	if s.currentExecution == nil {
		return fmt.Errorf("%w: currentExecution is nil", types.ErrUnexpectedWorkflow)
	}

	return s.syncRemoteAndLocal(ctx, s.currentExecution)
}

func (s *syncServicesExecution) FinishExecution(_ context.Context) {
	s.currentExecution = nil
}

// logThrottle logs a message at most once per hour, all other logs are dropped to prevent spam.
func (s *SyncServices) logThrottle(msg string) {
	if time.Since(s.lastDenyReasonLogAt) > time.Hour {
		logger.V(1).Println(msg)

		s.lastDenyReasonLogAt = time.Now()
	}
}

func ServicePayloadFromDiscovery(service discovery.Service, listenAddresses string, accountID string, agentID string, serviceID string) bleemeoapi.ServicePayload {
	tags := getTagsFromLocal(service)

	return bleemeoapi.ServicePayload{
		Monitor: bleemeoTypes.Monitor{
			Service: bleemeoTypes.Service{
				ID:              serviceID,
				Label:           service.Name,
				Instance:        service.Instance,
				ListenAddresses: listenAddresses,
				ExePath:         service.ExePath,
				Active:          service.Active,
				Tags:            tags,
			},
			AgentID: agentID,
		},
		Account: accountID,
	}
}

func getListenAddress(addresses []facts.ListenAddress) string {
	stringList := make([]string, 0, len(addresses))

	for _, v := range addresses {
		if v.Network() == "unix" {
			continue
		}

		stringList = append(stringList, v.String()+"/"+v.Network())
	}

	sort.Strings(stringList)

	return strings.Join(stringList, ",")
}

func (s *syncServicesExecution) syncRemoteAndLocal(ctx context.Context, execution types.SynchronizationExecution) error {
	localServices, _ := execution.Option().Discovery.GetLatestDiscovery()
	localServices = ServiceExcludeUnregistrable(localServices, s.parent.logThrottle)

	serviceRemoveDeletedFromRemote(execution, localServices, s.previousKnownService)

	if execution.IsOnlyEssential() {
		// no essential services, skip registering.
		return nil
	}

	localServices, _ = execution.Option().Discovery.GetLatestDiscovery()
	localServices = ServiceExcludeUnregistrable(localServices, s.parent.logThrottle)

	if err := s.serviceRegisterAndUpdate(ctx, execution, localServices); err != nil {
		return err
	}

	serviceDeleteNonLocal(ctx, execution, localServices)

	return nil
}

func serviceUpdateList(ctx context.Context, execution types.SynchronizationExecution) error {
	apiClient := execution.BleemeoAPIClient()
	agentID, _ := execution.Option().State.BleemeoCredentials()

	services, err := apiClient.ListServices(ctx, agentID, serviceReadFields)
	if err != nil {
		return err
	}

	execution.Option().Cache.SetServices(services)

	return nil
}

// serviceRemoveDeletedFromRemote removes the local services that were deleted on the API.
func serviceRemoveDeletedFromRemote(execution types.SynchronizationExecution, localServices []discovery.Service, previousServices map[string]bleemeoTypes.Service) {
	newServices := execution.Option().Cache.ServicesByUUID()

	deletedServiceNameInstance := make(map[common.ServiceNameInstance]bool)

	for _, srv := range previousServices {
		if _, ok := newServices[srv.ID]; !ok {
			key := common.ServiceNameInstance{Name: srv.Label, Instance: srv.Instance}
			deletedServiceNameInstance[key] = true
		}
	}

	localServiceToDelete := make([]discovery.Service, 0)

	for _, srv := range localServices {
		key := common.ServiceNameInstance{
			Name:     srv.Name,
			Instance: srv.Instance,
		}
		if _, ok := deletedServiceNameInstance[key]; ok {
			localServiceToDelete = append(localServiceToDelete, srv)
		}
	}

	execution.Option().Discovery.RemoveIfNonRunning(localServiceToDelete)
}

func (s *syncServicesExecution) serviceRegisterAndUpdate(ctx context.Context, execution types.SynchronizationExecution, localServices []discovery.Service) error {
	remoteServicesByKey := execution.Option().Cache.ServiceLookupFromList()
	registeredServices := execution.Option().Cache.ServicesByUUID()
	delayedContainer, _ := execution.GlobalState().DelayedContainers()
	apiClient := execution.BleemeoAPIClient()
	agentID, _ := execution.Option().State.BleemeoCredentials()

	for _, srv := range localServices {
		if _, ok := delayedContainer[srv.ContainerID]; ok {
			logger.V(2).Printf("Skip service %v due to delayedContainer", srv)

			continue
		}

		if !srv.Active {
			// Inactive service will be deleted by serviceDeleteNonLocal
			continue
		}

		key := common.ServiceNameInstance{
			Name:     srv.Name,
			Instance: srv.Instance,
		}

		remoteSrv, remoteFound := remoteServicesByKey[key]

		// Skip updating the remote service if the service is already up to date.
		listenAddresses := getListenAddress(srv.ListenAddresses)
		if skipUpdate(remoteFound, remoteSrv, srv, listenAddresses) {
			continue
		}

		payload := ServicePayloadFromDiscovery(srv, listenAddresses, execution.Option().Cache.AccountID(), agentID, "")

		var (
			result bleemeoTypes.Service
			err    error
		)

		if remoteFound {
			result, err = apiClient.UpdateService(ctx, remoteSrv.ID, payload, serviceWriteFields)
			if err != nil {
				return err
			}

			registeredServices[result.ID] = result
			logger.V(2).Printf("Service %v updated with UUID %s", key, result.ID)
		} else {
			result, err = apiClient.RegisterService(ctx, payload)
			if err != nil {
				return err
			}

			registeredServices[result.ID] = result
			logger.V(2).Printf("Service %v registered with UUID %s", key, result.ID)
		}

		if remoteFound && remoteSrv.Active != result.Active {
			// API will update all associated metrics and update their active status. Apply the same rule on local cache
			var newDeactivatedAt time.Time

			if !result.Active {
				newDeactivatedAt = execution.StartedAt()
			}

			metrics := execution.Option().Cache.Metrics()
			for i, m := range metrics {
				if m.ServiceID == result.ID {
					metrics[i].DeactivatedAt = newDeactivatedAt
				}
			}

			execution.Option().Cache.SetMetrics(metrics)
		}
	}

	finalServices := make([]bleemeoTypes.Service, 0, len(registeredServices))

	for _, srv := range registeredServices {
		finalServices = append(finalServices, srv)
	}

	execution.Option().Cache.SetServices(finalServices)

	return nil
}

// skipUpdate returns true if the service found by the discovery is up to date with the remote service on the API.
func skipUpdate(remoteFound bool, remoteSrv bleemeoTypes.Service, srv discovery.Service, listenAddresses string) bool {
	return remoteFound &&
		remoteSrv.Label == srv.Name &&
		remoteSrv.ListenAddresses == listenAddresses &&
		remoteSrv.ExePath == srv.ExePath &&
		remoteSrv.Active == srv.Active &&
		serviceHadSameTags(remoteSrv.Tags, srv)
}

func getTagsFromLocal(service discovery.Service) []bleemeoTypes.Tag {
	tags := make([]bleemeoTypes.Tag, 0, len(service.Tags)+len(service.Applications))

	for _, t := range service.Tags {
		if len(t) <= bleemeoapi.APITagsLength && t != "" {
			tags = append(tags, bleemeoTypes.Tag{Name: t, TagType: bleemeo.TagType_CreatedByGlouton})
		}
	}

	for _, app := range service.Applications {
		_, appTag := types.AutomaticApplicationName(app)

		if len(appTag) <= bleemeoapi.APITagsLength && appTag != "" {
			tags = append(tags, bleemeoTypes.Tag{Name: appTag, TagType: bleemeo.TagType_AutomaticGlouton})
		}
	}

	return tags
}

// serviceHadSameTags returns true if the two service had the same tags for glouton provided tags.
func serviceHadSameTags(remoteTags []bleemeoTypes.Tag, localService discovery.Service) bool {
	localTags := getTagsFromLocal(localService)
	remoteTagsNoID := make(map[bleemeoTypes.Tag]bool, len(remoteTags))

	for _, tag := range remoteTags {
		if tag.TagType != bleemeo.TagType_AutomaticGlouton && tag.TagType != bleemeo.TagType_CreatedByGlouton {
			continue
		}

		reducedTag := bleemeoTypes.Tag{
			Name:    tag.Name,
			TagType: tag.TagType,
		}
		remoteTagsNoID[reducedTag] = true
	}

	if len(remoteTagsNoID) != len(localTags) {
		return false
	}

	for _, wantedTag := range localTags {
		if !remoteTagsNoID[wantedTag] {
			return false
		}
	}

	return true
}

// ServiceExcludeUnregistrable removes the services that cannot be registered.
func ServiceExcludeUnregistrable(services []discovery.Service, logThrottle func(string)) []discovery.Service {
	i := 0

	for _, service := range services {
		// Remove services with an instance too long.
		if len(service.Instance) > common.APIServiceInstanceLength {
			msg := fmt.Sprintf(
				"Service %s will be ignored because the instance name '%s' is too long (> %d characters)",
				service.Name, service.Instance, common.APIServiceInstanceLength,
			)

			logThrottle(msg)

			continue
		}

		// Copy and increment index.
		services[i] = service
		i++
	}

	return services
}

// serviceDeleteNonLocal deletes the registered services that were not found in the discovery.
func serviceDeleteNonLocal(ctx context.Context, execution types.SynchronizationExecution, localServices []discovery.Service) {
	localServiceExists := make(map[common.ServiceNameInstance]bool, len(localServices))

	for _, srv := range localServices {
		if !srv.Active {
			// Inactive service are considered deleted.
			// I'm not sure why we kept them present in localServices.
			continue
		}

		key := common.ServiceNameInstance{
			Name:     srv.Name,
			Instance: srv.Instance,
		}

		localServiceExists[key] = true
	}

	registeredServices := execution.Option().Cache.Services()
	remoteServicesByKey := common.ServiceLookupFromList(registeredServices)
	finalServices := make([]bleemeoTypes.Service, 0, len(registeredServices))

	for _, remoteSrv := range registeredServices {
		key := common.ServiceNameInstance{Name: remoteSrv.Label, Instance: remoteSrv.Instance}
		if remoteSrv.ID == remoteServicesByKey[key].ID && localServiceExists[key] {
			finalServices = append(finalServices, remoteSrv)

			continue
		}

		logger.V(2).Printf("Deleting the service %v (uuid %s), which isn't present locally", key, remoteSrv.ID)

		err := execution.BleemeoAPIClient().DeleteService(ctx, remoteSrv.ID)
		if err != nil {
			logger.V(1).Printf("Failed to delete service %v on Bleemeo API: %v", key, err)
		}
	}

	execution.Option().Cache.SetServices(finalServices)
}
