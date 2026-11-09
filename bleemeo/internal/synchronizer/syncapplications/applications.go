// Copyright 2015-2026 Bleemeo
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

package syncapplications

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/cache"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/syncservices"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/logger"
)

var ErrApplicationFirst = errors.New("application need to be created before service")

var errApplicationNotAllowed = errors.New("applications aren't allowed in the current plan")

// SyncApplications creates the applications of the discovered services. It keeps no state
// across executions.
type SyncApplications struct{}

func New() *SyncApplications {
	return &SyncApplications{}
}

func (s *SyncApplications) Name() types.EntityName {
	return types.EntityApplication
}

func (s *SyncApplications) EnabledInMaintenance() bool {
	return false
}

func (s *SyncApplications) EnabledInSuspendedMode() bool {
	return false
}

func (s *SyncApplications) PrepareExecution(_ context.Context, execution types.SynchronizationExecution) (types.EntitySynchronizerExecution, error) {
	return &syncApplicationsExecution{execution: execution}, nil
}

type syncApplicationsExecution struct {
	execution types.SynchronizationExecution
}

func (e *syncApplicationsExecution) NeedSynchronization(_ context.Context) (bool, error) {
	// Same synchronization criteria as Services.
	return false, e.execution.RequestLinkedSynchronization(types.EntityApplication, types.EntityService)
}

func (e *syncApplicationsExecution) RefreshCache(ctx context.Context, syncType types.SyncType) error {
	if syncType == types.SyncTypeForceCacheRefresh {
		err := refreshCache(ctx, e.execution.BleemeoAPIClient(), e.execution.Option().Cache)
		if err != nil {
			return errors.Join(err, e.execution.FailOtherEntity(types.EntityService, ErrApplicationFirst))
		}
	}

	return nil
}

func (e *syncApplicationsExecution) SyncRemoteAndLocal(ctx context.Context, _ types.SyncType) error {
	cache := e.execution.Option().Cache
	if cfg, ok := cache.CurrentAccountConfig(); !ok || !cfg.ApplicationIntegration {
		return nil
	}

	localServices, _ := e.execution.Option().Discovery.GetLatestDiscovery()

	err := syncRemoteAndLocal(ctx, localServices, e.execution.BleemeoAPIClient(), cache)
	if errors.Is(err, errApplicationNotAllowed) {
		// Our account config is outdated, refresh it so we stop trying to create applications.
		logger.V(1).Printf("Skip creation of applications: %v", err)
		e.execution.RequestSynchronization(types.EntityConfig, true)

		return nil
	}

	if err != nil {
		return errors.Join(err, e.execution.FailOtherEntity(types.EntityService, ErrApplicationFirst))
	}

	return nil
}

func (e *syncApplicationsExecution) FinishExecution(_ context.Context) {
}

func refreshCache(ctx context.Context, apiClient types.ApplicationClient, cache *cache.Cache) error {
	result, err := apiClient.ListApplications(ctx)
	if err != nil {
		return err
	}

	cache.SetApplications(result)

	return nil
}

func syncRemoteAndLocal(ctx context.Context, localServices []discovery.Service, apiClient types.ApplicationClient, cache *cache.Cache) error {
	// we can discard the logThrottle message, it will be logged by syncservices itself if needed.
	localServices = syncservices.ServiceExcludeUnregistrable(localServices, func(string) {})
	previousServices := cache.Services()
	alreadyExistingApplicationTags := make(map[string]any)
	alreadyExistingApplicationName := make(map[string]any)

	// we only create an new application the first time this tag is seen on active service.
	// we don't create new application is a service already exists with that tag, because that means
	// user deleted the automatically created application.
	for _, srv := range previousServices {
		if !srv.Active {
			continue
		}

		for _, tags := range srv.Tags {
			// we assume that any automatic tags is an application tags
			if tags.TagType != bleemeo.TagType_AutomaticGlouton {
				continue
			}

			alreadyExistingApplicationTags[tags.Name] = nil
		}
	}

	applications := cache.Applications()

	for _, app := range applications {
		alreadyExistingApplicationTags[app.Tag] = nil
		alreadyExistingApplicationName[app.Name] = nil
	}

	// key: application tag, value: application name
	applicationsNeedCreate := make([]discovery.Application, 0)

	for _, srv := range localServices {
		if !srv.Active {
			continue
		}

		for _, localApp := range srv.Applications {
			appName, appTag := types.AutomaticApplicationName(localApp)

			if _, ok := alreadyExistingApplicationTags[appTag]; ok {
				continue
			}

			if _, ok := alreadyExistingApplicationName[appName]; ok {
				continue
			}

			applicationsNeedCreate = append(applicationsNeedCreate, localApp)
			alreadyExistingApplicationName[appName] = nil
			alreadyExistingApplicationTags[appTag] = nil
		}
	}

	for _, localApp := range applicationsNeedCreate {
		appName, appTag := types.AutomaticApplicationName(localApp)

		apiApp, err := apiClient.CreateApplication(ctx, bleemeoTypes.Application{
			Name: appName,
			Tag:  appTag,
		})
		if err != nil {
			// Kept any application that we already had created.
			cache.SetApplications(applications)

			if isApplicationNotAllowed(err) {
				return fmt.Errorf("%w: %w", errApplicationNotAllowed, err)
			}

			return err
		}

		logger.V(2).Printf("Created application %s with UUID %s", appName, apiApp.ID)

		applications = append(applications, apiApp)
	}

	cache.SetApplications(applications)

	return nil
}

// isApplicationNotAllowed returns true if the API refused to create an application
// because applications aren't available in the account's plan.
func isApplicationNotAllowed(err error) bool {
	apiErr := new(bleemeo.APIError)
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return false
	}

	return strings.Contains(string(apiErr.Response), "Don't allow to create an application in this current plan")
}
