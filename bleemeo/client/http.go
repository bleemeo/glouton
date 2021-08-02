// Copyright 2015-2019 Bleemeo
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

package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"glouton/logger"
	"glouton/version"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	minimalThrottle = 15 * time.Second
	maximalThrottle = 10 * time.Minute
	// If the throttle delay is less than this, automatically retry the requests.
	maxAutoRetryDelay = time.Minute
)

// HTTPClient is a wrapper around Bleemeo API. It mostly perform JWT authentication.
type HTTPClient struct {
	baseURL  *url.URL
	username string
	password string
	ctx      context.Context

	cl *http.Client

	l                   sync.Mutex
	jwtToken            string
	throttleDeadline    time.Time
	throttleConsecutive int
}

// APIError are returned when HTTP request got a response but that response is
// an error (400 or 500).
type APIError struct {
	StatusCode   int
	Content      string
	UnmarshalErr error
	IsAuthError  bool
}

// IsAuthError return true if the error is an APIError due to authentication failure.
func IsAuthError(err error) bool {
	if apiError, ok := err.(APIError); ok {
		return apiError.IsAuthError
	}

	return false
}

// IsNotFound return true if the error is an APIError due to 404.
func IsNotFound(err error) bool {
	if apiError, ok := err.(APIError); ok {
		return apiError.StatusCode == 404
	}

	return false
}

// IsBadRequest return true if the error is an APIError due to 400.
func IsBadRequest(err error) bool {
	if apiError, ok := err.(APIError); ok {
		return apiError.StatusCode == 400
	}

	return false
}

// IsServerError return true if the error is an APIError due to 5xx.
func IsServerError(err error) bool {
	if apiError, ok := err.(APIError); ok {
		return apiError.StatusCode >= 500
	}

	return false
}

// IsThrottleError return true if the error is an APIError due to 429 - Too many request.
//
// ThrottleDeadline could be used to get recommended retry deadline.
func IsThrottleError(err error) bool {
	if apiError, ok := err.(APIError); ok {
		return apiError.StatusCode == 429
	}

	return false
}

// APIErrorContent return the API error response, if the error is an APIError.
// Retrun the empty string if the error isn't an APIError.
func APIErrorContent(err error) string {
	if apiError, ok := err.(APIError); ok {
		return apiError.Content
	}

	return ""
}

func (ae APIError) Error() string {
	if ae.Content == "" && ae.UnmarshalErr != nil {
		return fmt.Sprintf("unable to decode JSON: %v", ae.UnmarshalErr)
	}

	return fmt.Sprintf("response code %d: %s", ae.StatusCode, ae.Content)
}

// NewClient return a client to talk with Bleemeo API.
//
// It does the authentication (using JWT currently) and may do rate-limiting/throtteling, so
// most function may return a ThrottleError.
func NewClient(ctx context.Context, baseURL string, username string, password string, insecureTLS bool) (*HTTPClient, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	cl := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: insecureTLS, //nolint: gosec
			},
		},
	}

	return &HTTPClient{
		ctx:      ctx,
		baseURL:  u,
		username: username,
		password: password,
		cl:       cl,
	}, nil
}

// ThrottleDeadline return the time request should be retryed.
func (c *HTTPClient) ThrottleDeadline() time.Time {
	c.l.Lock()
	defer c.l.Unlock()

	return c.throttleDeadline
}

// Do perform the specified request.
//
// Response is assumed to be JSON and will be decoded into result. If result is nil, response is not decoded
//
// If submittedData is not-nil, it's the body content of the request.
func (c *HTTPClient) Do(ctx context.Context, method string, path string, params map[string]string, data interface{}, result interface{}) (statusCode int, err error) {
	c.l.Lock()
	defer c.l.Unlock()

	req, err := c.prepareRequest(method, path, params, data)
	if err != nil {
		return 0, err
	}

	return c.do(ctx, req, result, true, true, false)
}

// DoUnauthenticated perform the specified request, but without the JWT token used in `Do`. It is otherwise exactly similar to `Do`.
func (c *HTTPClient) DoUnauthenticated(ctx context.Context, method string, path string, params map[string]string, data interface{}, result interface{}) (statusCode int, err error) {
	c.l.Lock()
	defer c.l.Unlock()

	req, err := c.prepareRequest(method, path, params, data)
	if err != nil {
		return 0, err
	}

	return c.do(ctx, req, result, true, false, false)
}

// DoTLSInsecure perform the specified request, but without TLS verification. It is otherwise exactly similar to `DoUnauthenticated`.
// This method will NOT use authentication (since credentials should not be sent insecurely).
func (c *HTTPClient) DoTLSInsecure(ctx context.Context, method string, path string, params map[string]string, data interface{}, result interface{}) (statusCode int, err error) {
	c.l.Lock()
	defer c.l.Unlock()

	req, err := c.prepareRequest(method, path, params, data)
	if err != nil {
		return 0, err
	}

	return c.do(ctx, req, result, true, false, true)
}

func (c *HTTPClient) prepareRequest(method string, path string, params map[string]string, data interface{}) (*http.Request, error) {
	u, err := c.baseURL.Parse(path)
	if err != nil {
		return nil, err
	}

	var bodyReader io.Reader

	if data != nil {
		body, _ := json.Marshal(data)
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, u.String(), bodyReader) //nolint:noctx
	if err != nil {
		return nil, err
	}

	if bodyReader != nil {
		req.Header.Add("Content-type", "application/json")
	}

	if len(params) > 0 {
		q := req.URL.Query()

		for k, v := range params {
			q.Set(k, v)
		}

		req.URL.RawQuery = q.Encode()
	}

	return req, nil
}

// PostAuth perform the post on specified path. baseURL will be always be added.
func (c *HTTPClient) PostAuth(path string, data interface{}, username string, password string, result interface{}) (statusCode int, err error) {
	c.l.Lock()
	defer c.l.Unlock()

	req, err := c.prepareRequest("POST", path, nil, data)
	if err != nil {
		return 0, err
	}

	req.SetBasicAuth(username, password)

	return c.sendRequest(req, result, false)
}

// Iter read all page for given resource.
//
// params may be modified.
func (c *HTTPClient) Iter(ctx context.Context, resource string, params map[string]string) ([]json.RawMessage, error) {
	if params == nil {
		params = make(map[string]string)
	}

	if _, ok := params["page_size"]; !ok {
		params["page_size"] = "100"
	}

	result := make([]json.RawMessage, 0)
	next := fmt.Sprintf("v1/%s/", resource)

	for ctx.Err() == nil {
		var page struct {
			Next    string
			Results []json.RawMessage
		}

		_, err := c.Do(ctx, "GET", next, params, nil, &page)
		if err != nil && IsNotFound(err) {
			break
		}

		if err != nil {
			return result, err
		}

		result = append(result, page.Results...)

		if next == page.Next {
			logger.V(1).Printf("next page is the same as current page: %s", page.Next)

			break
		}

		next = page.Next
		params = nil // params are now included in next url.

		if next == "" {
			break
		}
	}

	return result, ctx.Err()
}

func (c *HTTPClient) do(ctx context.Context, req *http.Request, result interface{}, firstCall bool, withAuth bool, forceInsecure bool) (int, error) {
	if forceInsecure {
		withAuth = false
	}

	if withAuth {
		if c.jwtToken == "" {
			newToken, err := c.GetJWT()
			if err != nil {
				return 0, err
			}

			c.jwtToken = newToken
		}

		req.Header.Set("Authorization", fmt.Sprintf("JWT %s", c.jwtToken))
	}

	for {
		statusCode, err := c.sendRequest(req, result, forceInsecure)

		// reset the JWT token if the call wasn't authorized, the JWT token may have expired
		if withAuth && firstCall && err != nil {
			if apiError, ok := err.(APIError); ok {
				if apiError.StatusCode == 401 {
					c.jwtToken = ""

					return c.do(ctx, req, result, false, withAuth, forceInsecure)
				}
			}
		}

		if IsThrottleError(err) {
			delay := time.Until(c.throttleDeadline)
			if delay > maxAutoRetryDelay {
				return statusCode, err
			}

			logger.V(2).Printf("During HTTPClient.do() got too many requests. Had to wait %v", delay)

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return 0, ctx.Err()
			}

			continue
		}

		return statusCode, err
	}
}

// GetJWT return a new JWT token for authentication with Bleemeo API.
func (c *HTTPClient) GetJWT() (string, error) {
	u, _ := c.baseURL.Parse("v1/jwt-auth/")

	body, _ := json.Marshal(map[string]string{
		"username": c.username,
		"password": c.password,
	})

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		return "", err
	}

	req.Header.Add("Content-type", "application/json")

	var token struct {
		Token string
	}

	statusCode, err := c.sendRequest(req, &token, false)
	if err != nil {
		if apiError, ok := err.(APIError); ok {
			if apiError.StatusCode < 500 && apiError.StatusCode != 429 {
				apiError.IsAuthError = true

				return "", apiError
			}
		}

		return "", err
	}

	if statusCode != 200 {
		if statusCode < 500 && statusCode != 429 {
			return "", APIError{
				StatusCode:  statusCode,
				Content:     "Unable to authenticate",
				IsAuthError: true,
			}
		}

		return "", APIError{
			StatusCode: statusCode,
			Content:    fmt.Sprintf("jwt-auth returned status code == %v, want 200", statusCode),
		}
	}

	return token.Token, nil
}

func (c *HTTPClient) sendRequest(req *http.Request, result interface{}, forceInsecure bool) (int, error) {
	if time.Until(c.throttleDeadline) > 0 {
		return 0, APIError{
			StatusCode: 429,
			Content:    "Request was throttled",
		}
	}

	req.Header.Add("X-Requested-With", "XMLHttpRequest")
	req.Header.Add("User-Agent", version.UserAgent())

	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()

	req = req.WithContext(ctx)
	client := c.cl

	if forceInsecure {
		client = &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, //nolint: gosec
				},
			},
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}

	defer func() {
		// Ensure we read the whole response to avoid "Connection reset by peer" on server
		// and ensure HTTP connection can be resused
		_, _ = io.Copy(ioutil.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != 429 && resp.StatusCode < 500 {
		c.throttleConsecutive = 0
	}

	if resp.StatusCode >= 400 {
		err := decodeError(resp)

		if resp.StatusCode == 429 {
			c.throttleConsecutive++

			delay := minimalThrottle + time.Duration(10*c.throttleConsecutive)*time.Second

			delaySecond, err := strconv.ParseInt(resp.Header.Get("Retry-After"), 10, 64)
			if err == nil {
				delay = time.Duration(delaySecond) * time.Second
			}

			if delay > maximalThrottle {
				delay = maximalThrottle
			}

			if delay < minimalThrottle {
				delay = minimalThrottle
			}

			c.throttleDeadline = time.Now().Add(delay)
		}

		return 0, err
	}

	if result != nil {
		err = json.NewDecoder(resp.Body).Decode(result)
		if err != nil {
			return 0, APIError{
				StatusCode:   resp.StatusCode,
				Content:      "",
				UnmarshalErr: err,
			}
		}
	}

	return resp.StatusCode, nil
}

func decodeError(resp *http.Response) APIError {
	if resp.Header.Get("Content-Type") != "application/json" {
		partialBody := make([]byte, 250)
		n, _ := resp.Body.Read(partialBody)

		return APIError{
			StatusCode:   resp.StatusCode,
			Content:      string(partialBody[:n]),
			UnmarshalErr: nil,
			IsAuthError:  resp.StatusCode == 401,
		}
	}

	var (
		jsonMessage json.RawMessage
		jsonError   struct {
			Error          string
			Detail         string
			NonFieldErrors []string `json:"non_field_errors"`
		}
		errorList []string
	)

	err := json.NewDecoder(resp.Body).Decode(&jsonMessage)
	if err != nil {
		return APIError{
			StatusCode:   resp.StatusCode,
			Content:      "",
			UnmarshalErr: err,
			IsAuthError:  resp.StatusCode == 401,
		}
	}

	err = json.Unmarshal(jsonMessage, &jsonError)
	if err != nil {
		err = json.Unmarshal(jsonMessage, &errorList)
	}

	if err != nil {
		return APIError{
			StatusCode:   resp.StatusCode,
			Content:      "",
			UnmarshalErr: err,
			IsAuthError:  resp.StatusCode == 401,
		}
	}

	if errorList != nil {
		return APIError{
			StatusCode:   resp.StatusCode,
			Content:      strings.Join(errorList, ", "),
			UnmarshalErr: nil,
			IsAuthError:  resp.StatusCode == 401,
		}
	}

	if jsonError.Error != "" || jsonError.Detail != "" || len(jsonError.NonFieldErrors) > 0 {
		errorMessage := jsonError.Error
		if errorMessage == "" {
			errorMessage = jsonError.Detail
		}

		if errorMessage == "" && len(jsonError.NonFieldErrors) > 0 {
			errorMessage = strings.Join(jsonError.NonFieldErrors, ", ")
		}

		return APIError{
			StatusCode:   resp.StatusCode,
			Content:      errorMessage,
			UnmarshalErr: nil,
			IsAuthError:  resp.StatusCode == 401,
		}
	}

	return APIError{
		StatusCode:   resp.StatusCode,
		Content:      string(jsonMessage),
		UnmarshalErr: nil,
		IsAuthError:  resp.StatusCode == 401,
	}
}
