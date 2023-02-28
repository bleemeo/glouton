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
	"encoding/json"
	"fmt"
	"glouton/bleemeo/client"
	"time"
)

type wrapperClient struct {
	s                *Synchronizer
	client           *client.HTTPClient
	duplicateError   error
	duplicateChecked bool
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
		cl.duplicateError = cl.s.checkDuplicated(ctx)
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
		cl.duplicateError = cl.s.checkDuplicated(ctx)
	}

	if cl.duplicateError != nil {
		return nil, cl.duplicateError
	}

	return cl.client.Iter(ctx, resource, params)
}
