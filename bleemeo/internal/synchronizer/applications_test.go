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

package synchronizer

import (
	"testing"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/discovery"
)

// TestApplicationIntegration ensures applications are only created when the plan allows them,
// and that services are registered in both cases.
func TestApplicationIntegration(t *testing.T) {
	t.Parallel()

	composeApp := discovery.Application{Name: "my-compose", Type: discovery.ApplicationDockerCompose}
	appName, appTag := types.AutomaticApplicationName(composeApp)

	// assertApplication checks whether the application exists in the API
	// and whether the service has the application tag.
	assertApplication := func(t *testing.T, helper *syncTestHelper, wantApplication bool) {
		t.Helper()

		applications := helper.wrapperClientMock.resources.applications.clone()

		var gotApplication bool

		for _, app := range applications {
			if app.Name == appName && app.Tag == appTag {
				gotApplication = true
			}
		}

		if gotApplication != wantApplication || len(applications) > 1 {
			t.Errorf("applications = %v, want application %q = %v", applications, appName, wantApplication)
		}

		services := helper.ServicesFromAPI()
		if len(services) != 1 {
			t.Fatalf("got %d services, want 1", len(services))
		}

		var gotTag bool

		for _, tag := range services[0].Tags {
			if tag.Name == appTag && tag.TagType == bleemeo.TagType_AutomaticGlouton {
				gotTag = true
			}
		}

		if gotTag != wantApplication {
			t.Errorf("service tags = %v, want tag %q = %v", services[0].Tags, appTag, wantApplication)
		}
	}

	runSync := func(t *testing.T, helper *syncTestHelper) {
		t.Helper()

		// two run, because very first is "onlyEssential"
		for range 2 {
			if err := helper.runOnceWithResult(t).Check(); err != nil {
				t.Error(err)
			}
		}
	}

	setup := func(t *testing.T, configs []bleemeoTypes.Config) *syncTestHelper {
		t.Helper()

		helper := newHelper(t)
		t.Cleanup(helper.Close)

		helper.SetAPIConfigs(configs)
		helper.initSynchronizer(t)
		helper.discovery.SetResult([]discovery.Service{
			{
				Name:         testServiceRedis,
				ServiceType:  discovery.RedisService,
				Active:       true,
				Applications: []discovery.Application{composeApp},
			},
		}, nil)

		return helper
	}

	t.Run("enabled", func(t *testing.T) {
		t.Parallel()

		helper := setup(t, defaultAccountConfigs)
		runSync(t, helper)
		assertApplication(t, helper, true)
	})

	t.Run("disabled-then-enabled", func(t *testing.T) {
		t.Parallel()

		helper := setup(t, makeTestPlanConfigs(accountID, agentTypeAgent, agentTypeSNMP, agentTypeMonitor, true))
		runSync(t, helper)
		assertApplication(t, helper, false)

		helper.SetAPIConfigs(defaultAccountConfigs)
		helper.SetTimeToNextFullSync()
		runSync(t, helper)
		helper.AddTime(2 * time.Minute)
		runSync(t, helper)
		assertApplication(t, helper, true)
	})
}
