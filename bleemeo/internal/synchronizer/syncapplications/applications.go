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

package syncapplications

import (
	"context"
	"errors"
	"fmt"
	"glouton/bleemeo/client"
	"glouton/bleemeo/internal/cache"
	"glouton/bleemeo/internal/synchronizer/syncservices"
	"glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/discovery"
	"glouton/logger"
	"mime"
	"strings"
	"time"
)

var ErrApplicationFirst = errors.New("application need to be created before service")

type SyncApplications struct {
	currentExecution types.SynchronizationExecution
}

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
	s.currentExecution = execution

	return s, nil
}

func (s *SyncApplications) NeedSynchronization(_ context.Context) (bool, error) {
	if s.currentExecution == nil {
		return false, fmt.Errorf("%w: currentExecution is nil", types.ErrUnexpectedWorkflow)
	}

	if !s.currentExecution.GlobalState().APIHasFeature(types.APIFeatureApplication) {
		return false, nil
	}

	// Same synchronization criteria as Services.
	return false, s.currentExecution.RequestLinkedSynchronization(types.EntityApplication, types.EntityService)
}

func (s *SyncApplications) RefreshCache(ctx context.Context, syncType types.SyncType) error {
	if s.currentExecution == nil {
		return fmt.Errorf("%w: currentExecution is nil", types.ErrUnexpectedWorkflow)
	}

	if syncType == types.SyncTypeForceCacheRefresh {
		err := refreshCache(ctx, s.currentExecution.BleemeoAPIClient(), s.currentExecution.Option().Cache)

		if apiErr := (client.APIError{}); errors.As(err, &apiErr) {
			mediatype, _, err := mime.ParseMediaType(apiErr.ContentType)
			if err == nil && mediatype == "text/html" && strings.Contains(apiErr.FinalURL, "login") {
				// If we receive an error with an HTTP 200, it's very likely means that API don't yet support
				// application. Disable this feature
				s.currentExecution.GlobalState().SetAPIHasFeature(types.APIFeatureApplication, false)

				// and ignore the error
				return nil
			}
		}

		if err != nil {
			s.currentExecution.FailOtherEntity(types.EntityService, ErrApplicationFirst)

			return err
		}

		// no error -> application feature is available
		s.currentExecution.GlobalState().SetAPIHasFeature(types.APIFeatureApplication, true)
	}

	return nil
}

func (s *SyncApplications) SyncRemoteAndLocal(ctx context.Context, syncType types.SyncType) error {
	_ = syncType

	if s.currentExecution == nil {
		return fmt.Errorf("%w: currentExecution is nil", types.ErrUnexpectedWorkflow)
	}

	if !s.currentExecution.GlobalState().APIHasFeature(types.APIFeatureApplication) {
		return nil
	}

	localServices, err := s.currentExecution.Option().Discovery.Discovery(ctx, 24*time.Hour)
	if err != nil {
		s.currentExecution.FailOtherEntity(types.EntityService, ErrApplicationFirst)

		return err
	}

	err = syncRemoteAndLocal(ctx, localServices, s.currentExecution.BleemeoAPIClient(), s.currentExecution.Option().Cache)
	if err != nil {
		s.currentExecution.FailOtherEntity(types.EntityService, ErrApplicationFirst)

		return err
	}

	return nil
}

func (s *SyncApplications) FinishExecution(_ context.Context) {
	s.currentExecution = nil
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
	alreadyExistingApplicationTags := make(map[string]interface{})
	alreadyExistingApplicationName := make(map[string]interface{})

	// we only create an new application the first time this tag is seen on active service.
	// we don't create new application is a service already exists with that tag, because that means
	// user deleted the automatically created application.
	for _, srv := range previousServices {
		if !srv.Active {
			continue
		}

		for _, tags := range srv.Tags {
			// we assume that any automatic tags is an application tags
			if tags.TagType != bleemeoTypes.TagTypeIsAutomaticByGlouton {
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

			return err
		}

		logger.V(2).Printf("created application %s with UUID %s", appName, apiApp.ID)

		applications = append(applications, apiApp)
	}

	cache.SetApplications(applications)

	return nil
}
