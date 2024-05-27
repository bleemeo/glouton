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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/bleemeo/bleemeo-go"
	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"
)

const gloutonOAuthClientID = "5c31cbfc-254a-4fb9-822d-e55c681a3d4f"

var (
	errInvalidAgentID      = errors.New("got an invalid agent ID")
	errClientUninitialized = fmt.Errorf("%w: HTTP client", errUninitialized)
)

type wrapperClient struct {
	client           *bleemeo.Client
	checkDuplicateFn func(context.Context, types.Client) error

	duplicateError   error
	duplicateChecked bool
	// TODO: throttling
	throttleDeadline    time.Time
	throttleConsecutive int
}

func (cl *wrapperClient) dupCheck() error {
	if !cl.duplicateChecked {
		cl.duplicateChecked = true
		cl.duplicateError = cl.checkDuplicateFn(context.TODO(), cl)
	}

	return cl.duplicateError
}

func (cl *wrapperClient) ThrottleDeadline() time.Time {
	if cl == nil {
		return time.Time{}
	}

	return cl.throttleDeadline
}

func (cl *wrapperClient) Get(ctx context.Context, resource bleemeo.Resource, id string, fields string, result any) error {
	if cl == nil {
		return errClientUninitialized
	}

	if err := cl.dupCheck(); err != nil {
		return err
	}

	respBody, err := cl.client.Get(ctx, resource, id, strings.Split(fields, ",")...)
	if err != nil {
		return err
	}

	return json.Unmarshal(respBody, result)
}

func (cl *wrapperClient) Count(ctx context.Context, resource bleemeo.Resource, params url.Values) (int, error) {
	if cl == nil {
		return 0, errClientUninitialized
	}

	if err := cl.dupCheck(); err != nil {
		return 0, err
	}

	return cl.client.Count(ctx, resource, params)
}

func (cl *wrapperClient) Iterator(resource bleemeo.Resource, params url.Values) bleemeo.Iterator {
	if cl == nil {
		return errorIterator{errClientUninitialized}
	}

	if err := cl.dupCheck(); err != nil {
		return errorIterator{err}
	}

	return cl.client.Iterator(resource, params)
}

func (cl *wrapperClient) Create(ctx context.Context, resource bleemeo.Resource, body any, fields string, result any) error {
	if cl == nil {
		return errClientUninitialized
	}

	if err := cl.dupCheck(); err != nil {
		return err
	}

	respBody, err := cl.client.Create(ctx, resource, body, strings.Split(fields, ",")...)
	if err != nil {
		return err
	}

	if result != nil {
		return json.Unmarshal(respBody, result)
	}

	return nil
}

func (cl *wrapperClient) Update(ctx context.Context, resource bleemeo.Resource, id string, body any, fields string, result any) error {
	if cl == nil {
		return errClientUninitialized
	}

	if err := cl.dupCheck(); err != nil {
		return err
	}

	respBody, err := cl.client.Update(ctx, resource, id, body, strings.Split(fields, ",")...)
	if err != nil {
		return err
	}

	if result != nil {
		return json.Unmarshal(respBody, result)
	}

	return nil
}

func (cl *wrapperClient) Delete(ctx context.Context, resource bleemeo.Resource, id string) error {
	if cl == nil {
		return errClientUninitialized
	}

	if err := cl.dupCheck(); err != nil {
		return err
	}

	return cl.client.Delete(ctx, resource, id)
}

func (cl *wrapperClient) Do(ctx context.Context, method, reqURI string, params url.Values, authenticated bool, body io.Reader, result any) (statusCode int, err error) {
	statusCode, respBody, err := cl.client.Do(ctx, method, reqURI, params, authenticated, body)
	if err != nil {
		return 0, err
	}

	if result != nil {
		err = json.Unmarshal(respBody, result)
	}

	return statusCode, err
}

func (cl *wrapperClient) DoWithBody(ctx context.Context, reqURI string, contentType string, body io.Reader) (statusCode int, err error) {
	if cl == nil {
		return 0, errClientUninitialized
	}

	if err = cl.dupCheck(); err != nil {
		return 0, err
	}

	if !path.IsAbs(reqURI) {
		reqURI = "/" + reqURI
	}

	req, err := cl.client.ParseRequest(http.MethodPost, reqURI, http.Header{"Content-Type": {contentType}}, nil, body)
	if err != nil {
		return 0, err //nolint:wrapcheck
	}

	resp, err := cl.client.DoRequest(ctx, req, true)
	if err != nil {
		return 0, err //nolint:wrapcheck
	}

	_ = resp.Body.Close()

	return resp.StatusCode, nil
}

// errorIterator implements [bleemeo.Iterator] but only returns an error.
type errorIterator struct {
	err error
}

func (errIter errorIterator) Next(context.Context) bool {
	return false
}

func (errIter errorIterator) At() json.RawMessage {
	return nil
}

func (errIter errorIterator) Err() error {
	return errIter.err
}

// IsAuthError return true if the error is an APIError due to authentication failure.
func IsAuthError(err error) bool {
	apiError := new(bleemeo.AuthError)

	return errors.As(err, &apiError)
}

// IsNotFound return true if the error is an APIError due to 404.
func IsNotFound(err error) bool {
	if apiError := new(bleemeo.APIError); errors.As(err, &apiError) {
		return apiError.StatusCode == 404
	}

	return false
}

// IsBadRequest return true if the error is an APIError due to 400.
func IsBadRequest(err error) bool {
	if apiError := new(bleemeo.APIError); errors.As(err, &apiError) {
		return apiError.StatusCode == 400
	}

	return false
}

// IsServerError return true if the error is an APIError due to 5xx.
func IsServerError(err error) bool {
	if apiError := new(bleemeo.APIError); errors.As(err, &apiError) {
		return apiError.StatusCode >= 500
	}

	return false
}

// IsThrottleError return true if the error is an APIError due to 429 - Too many request.
//
// ThrottleDeadline could be used to get recommended retry deadline.
func IsThrottleError(err error) bool {
	if apiError := new(bleemeo.APIError); errors.As(err, &apiError) {
		return apiError.StatusCode == 429
	}

	return false
}

// APIErrorContent return the API error response, if the error is an APIError.
// Return an empty string if the error isn't an APIError.
func APIErrorContent(err error) string {
	if apiError := new(bleemeo.APIError); errors.As(err, &apiError) {
		return string(apiError.Response)
	}

	return ""
}
