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
	"agentgo/version"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// HTTPClient is a wrapper around Bleemeo API. It mostly perform JWT authentication
type HTTPClient struct {
	baseURL  *url.URL
	username string
	password string
	ctx      context.Context

	cl *http.Client

	l        sync.Mutex
	jwtToken string
}

// APIError are returned when HTTP request got a response but that response is
// an error (400 or 500)
type APIError struct {
	StatusCode   int
	Content      string
	UnmarshalErr error
	IsAuthError  bool
}

// IsAuthError return true if the error is an APIError due to authentication failure
func IsAuthError(err error) bool {
	if apiError, ok := err.(APIError); ok {
		return apiError.IsAuthError
	}
	return false
}

// IsNotFound return true if the error is an APIError due to 404
func IsNotFound(err error) bool {
	if apiError, ok := err.(APIError); ok {
		return apiError.StatusCode == 404
	}
	return false
}

// IsServerError return true if the error is an APIError due to 5xx
func IsServerError(err error) bool {
	if apiError, ok := err.(APIError); ok {
		return apiError.StatusCode >= 500
	}
	return false
}

func (ae APIError) Error() string {
	if ae.Content == "" && ae.UnmarshalErr != nil {
		return fmt.Sprintf("unable to decode JSON: %v", ae.UnmarshalErr)
	}
	return fmt.Sprintf("response code %d: %s", ae.StatusCode, ae.Content)
}

// NewClient return a client to talk with Bleemeo API
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

// Do perform the specified request.
//
// Response is assumed to be JSON and will be decoded into result. If result is nil, response is not decoded
//
// If submittedData is not-nil, it's the body content of the request.
func (c *HTTPClient) Do(method string, path string, params map[string]string, data interface{}, result interface{}) (statusCode int, err error) {
	c.l.Lock()
	defer c.l.Unlock()

	req, err := c.prepareRequest(method, path, params, data)
	if err != nil {
		return 0, err
	}
	return c.do(req, result, true)
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
	req, err := http.NewRequest(method, u.String(), bodyReader)
	if bodyReader != nil {
		req.Header.Add("Content-type", "application/json")
	}
	if err != nil {
		return nil, err
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
	return c.sendRequest(req, result)
}

// Iter read all page for given resource
//
// params may be modified
func (c *HTTPClient) Iter(resource string, params map[string]string) ([]json.RawMessage, error) {
	if params == nil {
		params = make(map[string]string)
	}
	if _, ok := params["page_size"]; !ok {
		params["page_size"] = "100"
	}
	result := make([]json.RawMessage, 0)
	next := fmt.Sprintf("v1/%s/", resource)
	for {
		var page struct {
			Next    string
			Results []json.RawMessage
		}
		_, err := c.Do("GET", next, params, nil, &page)
		if err != nil && IsNotFound(err) {
			break
		}
		if err != nil {
			return result, err
		}

		result = append(result, page.Results...)
		next = page.Next
		params = nil // params are now included in next url.
		if next == "" {
			break
		}
	}
	return result, nil
}

func (c *HTTPClient) do(req *http.Request, result interface{}, firstCall bool) (int, error) {
	if c.jwtToken == "" {
		newToken, err := c.GetJWT()
		if err != nil {
			return 0, err
		}
		c.jwtToken = newToken
	}

	req.Header.Set("Authorization", fmt.Sprintf("JWT %s", c.jwtToken))
	statusCode, err := c.sendRequest(req, result)
	if firstCall && err != nil {
		if apiError, ok := err.(APIError); ok {
			if apiError.StatusCode == 401 {
				c.jwtToken = ""
				return c.do(req, result, false)
			}
		}
	}
	return statusCode, err
}

// GetJWT return a new JWT token for authentication with Bleemeo API
func (c *HTTPClient) GetJWT() (string, error) {
	u, _ := c.baseURL.Parse("v1/jwt-auth/")

	body, _ := json.Marshal(map[string]string{
		"username": c.username,
		"password": c.password,
	})
	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Add("Content-type", "application/json")

	var token struct {
		Token string
	}
	statusCode, err := c.sendRequest(req, &token)
	if err != nil {
		if apiError, ok := err.(APIError); ok {
			if apiError.StatusCode < 500 {
				apiError.IsAuthError = true
				return "", apiError
			}
		}
		return "", err
	}
	if statusCode != 200 {
		if statusCode < 500 {
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

func (c *HTTPClient) sendRequest(req *http.Request, result interface{}) (int, error) {
	req.Header.Add("X-Requested-With", "XMLHttpRequest")
	req.Header.Add("User-Agent", version.UserAgent())

	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := c.cl.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		if resp.Header.Get("Content-Type") != "application/json" {
			partialBody := make([]byte, 250)
			n, _ := resp.Body.Read(partialBody)
			return 0, APIError{
				StatusCode:   resp.StatusCode,
				Content:      string(partialBody[:n]),
				UnmarshalErr: nil,
				IsAuthError:  resp.StatusCode == 401,
			}
		}
		var jsonMessage json.RawMessage
		var jsonError struct {
			Error          string
			Detail         string
			NonFieldErrors []string `json:"non_field_errors"`
		}
		err = json.NewDecoder(resp.Body).Decode(&jsonMessage)
		if err != nil {
			return 0, APIError{
				StatusCode:   resp.StatusCode,
				Content:      "",
				UnmarshalErr: err,
				IsAuthError:  resp.StatusCode == 401,
			}
		}
		err = json.Unmarshal(jsonMessage, &jsonError)
		if err != nil {
			return 0, APIError{
				StatusCode:   resp.StatusCode,
				Content:      "",
				UnmarshalErr: err,
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
			return 0, APIError{
				StatusCode:   resp.StatusCode,
				Content:      errorMessage,
				UnmarshalErr: nil,
				IsAuthError:  resp.StatusCode == 401,
			}
		}
		return 0, APIError{
			StatusCode:   resp.StatusCode,
			Content:      string(jsonMessage),
			UnmarshalErr: nil,
			IsAuthError:  resp.StatusCode == 401,
		}

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
