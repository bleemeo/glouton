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
	"time"

	"github.com/bleemeo/glouton/bleemeo/client"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
)

type wrapperClient struct {
	client           *client.HTTPClient
	duplicateError   error
	duplicateChecked bool
	checkDuplicated  func(context.Context, types.RawClient) error
}

func (s *Synchronizer) newClient() *wrapperClient {
	return &wrapperClient{
		checkDuplicated: s.checkDuplicated,
		client:          s.realClient,
	}
}

func (cl *wrapperClient) ThrottleDeadline() time.Time {
	if cl == nil {
		return time.Time{}
	}

	return cl.client.ThrottleDeadline()
}

func (cl *wrapperClient) Do(ctx context.Context, method string, path string, params map[string]string, data interface{}, result interface{}) (statusCode int, err error) {
	if cl == nil {
		return 0, fmt.Errorf("%w: HTTP client", errUninitialized)
	}

	if !cl.duplicateChecked {
		cl.duplicateChecked = true
		cl.duplicateError = cl.checkDuplicated(ctx, cl)
	}

	if cl.duplicateError != nil {
		return 0, cl.duplicateError
	}

	return cl.client.Do(ctx, method, path, params, data, result)
}

func (cl *wrapperClient) Iter(ctx context.Context, resource string, params map[string]string) ([]json.RawMessage, error) {
	if cl == nil {
		return nil, fmt.Errorf("%w: HTTP client", errUninitialized)
	}

	if !cl.duplicateChecked {
		cl.duplicateChecked = true
		cl.duplicateError = cl.checkDuplicated(ctx, cl)
	}

	if cl.duplicateError != nil {
		return nil, cl.duplicateError
	}

	return cl.client.Iter(ctx, resource, params)
}

func (cl *wrapperClient) DoWithBody(ctx context.Context, path string, contentType string, body io.Reader) (statusCode int, err error) {
	return cl.client.DoWithBody(ctx, path, contentType, body)
}

func (cl *wrapperClient) ListApplications(ctx context.Context) ([]bleemeoTypes.Application, error) {
	result, err := cl.Iter(ctx, "application", map[string]string{})
	if err != nil {
		return nil, err
	}

	applications := make([]bleemeoTypes.Application, 0, len(result))

	for _, jsonMessage := range result {
		var application bleemeoTypes.Application

		if err := json.Unmarshal(jsonMessage, &application); err != nil {
			continue
		}

		applications = append(applications, application)
	}

	return applications, nil
}

func (cl *wrapperClient) CreateApplication(ctx context.Context, app bleemeoTypes.Application) (bleemeoTypes.Application, error) {
	var result bleemeoTypes.Application

	app.ID = "" // ID isn't allowed in creation

	_, err := cl.Do(ctx, "POST", "v1/application/", map[string]string{}, app, &result)
	if err != nil {
		return bleemeoTypes.Application{}, err
	}

	return result, nil
}
