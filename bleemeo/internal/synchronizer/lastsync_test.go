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
	"net/http"
	"testing"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/bleemeoapi"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	"github.com/bleemeo/glouton/discovery"
)

// TestServicesRetriedAfterFailure checks a discovery is synchronized again while the services
// fail to synchronize it, and no longer once they succeeded.
func TestServicesRetriedAfterFailure(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.initSynchronizer(t)

	if err := helper.runOnce(t); err != nil {
		t.Fatal(err)
	}

	helper.wrapperClientMock.resources.services.createHook = func(*bleemeoapi.ServicePayload) error {
		return clientError{body: "unavailable", statusCode: http.StatusInternalServerError}
	}

	helper.AddTime(time.Minute)
	helper.discovery.SetResult([]discovery.Service{
		{Name: testServiceRedis, ServiceType: discovery.RedisService, Active: true},
	}, nil)
	helper.discovery.UpdatedAt = helper.Now()

	for _, step := range []struct {
		name        string
		failing     bool
		wantService bool
	}{
		{name: "discovery, the services failing", failing: true, wantService: true},
		{name: "still failing", failing: true, wantService: true},
		{name: "succeeding", failing: false, wantService: true},
		{name: "nothing new", failing: false, wantService: false},
	} {
		if !step.failing {
			helper.wrapperClientMock.resources.services.createHook = nil
		}

		helper.AddTime(time.Minute)

		result := helper.runOnceWithResult(t)
		if (result.err != nil) != step.failing {
			t.Errorf("%s: runOnce error = %v, want error %v", step.name, result.err, step.failing)
		}

		if _, got := result.syncPerEntity[types.EntityService]; got != step.wantService {
			t.Errorf("%s: services synchronized = %v, want %v", step.name, got, step.wantService)
		}
	}

	if services := helper.ServicesFromAPI(); len(services) != 1 {
		t.Errorf("%d services on the API, want 1", len(services))
	}
}
