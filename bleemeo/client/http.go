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

package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/logger"
	gloutonTypes "github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/version"

	"golang.org/x/oauth2"
)

const (
	minimalThrottle = 15 * time.Second
	maximalThrottle = 10 * time.Minute
	// If the throttle delay is less than this, automatically retry the requests.
	maxAutoRetryDelay = time.Minute

	gloutonOAuthClientID = "5c31cbfc-254a-4fb9-822d-e55c681a3d4f"
)

var (
	errInvalidAgentID       = errors.New("got an invalid agent ID")
	errUnexpectedStatusCode = errors.New("unexpected status code")
)

// HTTPClient is a wrapper around Bleemeo API.
// It is useful for authenticating seamlessly.
type HTTPClient struct {
	baseURL  *url.URL
	username string
	password string

	cl          *http.Client
	reloadState types.BleemeoReloadState
	oauthConfig oauth2.Config

	l                   sync.Mutex
	token               *oauth2.Token
	throttleDeadline    time.Time
	throttleConsecutive int
	requestsCount       int
}

// APIError are returned when HTTP request got a response but that response is
// an error (400 or 500).
type APIError struct {
	StatusCode   int
	Content      string
	ContentType  string
	FinalURL     string
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
// It does the authentication (using OAuth currently) and may do rate-limiting/throtteling, so
// most functions may return a ThrottleError.
func NewClient(baseURL string, username string, password string, insecureTLS bool, reloadState types.BleemeoReloadState) (*HTTPClient, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: insecureTLS, //nolint:gosec
	}

	cl := &http.Client{
		Transport: gloutonTypes.NewHTTPTransport(tlsConfig),
	}

	tokenURL, err := url.JoinPath(baseURL, "/o/token/")
	if err != nil {
		return nil, fmt.Errorf("can't build token URL: %w", err)
	}

	var token *oauth2.Token
	if reloadState != nil {
		token = reloadState.Token()
	}

	return &HTTPClient{
		baseURL:     u,
		username:    username,
		password:    password,
		cl:          cl,
		token:       token,
		reloadState: reloadState,
		oauthConfig: oauth2.Config{
			ClientID: gloutonOAuthClientID,
			Endpoint: oauth2.Endpoint{
				TokenURL:  tokenURL,
				AuthStyle: oauth2.AuthStyleInParams,
			},
		},
	}, nil
}

// ThrottleDeadline return the time request should be retried.
func (c *HTTPClient) ThrottleDeadline() time.Time {
	c.l.Lock()
	defer c.l.Unlock()

	return c.throttleDeadline
}

func (c *HTTPClient) RequestsCount() int {
	c.l.Lock()
	defer c.l.Unlock()

	return c.requestsCount
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

// DoUnauthenticated perform the specified request, but without the OAuth token used in `Do`. It is otherwise exactly similar to `Do`.
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

// DoWithBody sends a POST request to the given path with the given body under the given content-type.
// It returns the status code of the response or any error that occurred.
func (c *HTTPClient) DoWithBody(ctx context.Context, path string, contentType string, body io.Reader) (int, error) {
	c.l.Lock()
	defer c.l.Unlock()

	u, err := c.baseURL.Parse(path)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), body)
	if err != nil {
		return 0, err
	}

	req.Header.Add("Content-Type", contentType)

	return c.do(ctx, req, nil, true, true, false)
}

func (c *HTTPClient) prepareRequest(method string, path string, params map[string]string, data interface{}) (*http.Request, error) {
	u, err := c.baseURL.Parse(path)
	if err != nil {
		return nil, err
	}

	var bodyReader io.Reader

	if data != nil {
		body, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}

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
func (c *HTTPClient) PostAuth(ctx context.Context, path string, data interface{}, username string, password string, result interface{}) (statusCode int, err error) {
	c.l.Lock()
	defer c.l.Unlock()

	req, err := c.prepareRequest("POST", path, nil, data)
	if err != nil {
		return 0, err
	}

	req.SetBasicAuth(username, password)

	statusCode, err = c.sendRequest(ctx, req, result, false)

	return statusCode, err
}

// Iter read all page for given resource.
//
// params may be modified.
func (c *HTTPClient) Iter(ctx context.Context, resource string, params map[string]string) ([]json.RawMessage, error) {
	if params == nil {
		params = make(map[string]string)
	}

	if _, ok := params["page_size"]; !ok {
		params["page_size"] = "10000"
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
		if !c.token.Valid() {
			newToken, err := c.GetToken(ctx)
			if err != nil {
				return 0, err
			}

			c.token = newToken
			if c.reloadState != nil {
				c.reloadState.SetToken(c.token)
			}
		}

		req.Header.Set("Authorization", "Bearer "+c.token.AccessToken)
	}

	for {
		statusCode, err := c.sendRequest(ctx, req, result, forceInsecure)

		// reset the token if the call wasn't authorized, the token may have expired
		if withAuth && firstCall && err != nil {
			if apiError, ok := err.(APIError); ok {
				if apiError.StatusCode == http.StatusUnauthorized {
					c.token = nil

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

// GetToken for authentication with the Bleemeo API.
// The access token will be renewed using the refresh token or with
// the username and password if the refresh token has expired.
func (c *HTTPClient) GetToken(ctx context.Context) (*oauth2.Token, error) {
	if c.token != nil && c.token.RefreshToken != "" {
		token, err := c.refreshToken(ctx, c.token)
		if err == nil {
			return token, nil
		}

		// A 401 response is received if the refresh token has expired, we only want
		// to try username/password authentication in this case.
		var apiError APIError
		if !(errors.As(err, &apiError) && (apiError.StatusCode == 401 || apiError.Content == "invalid_grant")) {
			return nil, err
		}
	}

	return c.getToken(ctx)
}

func (c *HTTPClient) getToken(ctx context.Context) (*oauth2.Token, error) {
	accessPayload := url.Values{
		"grant_type": {"password"},
		"username":   {c.username},
		"password":   {c.password},
		"client_id":  {c.oauthConfig.ClientID},
	}

	req, err := http.NewRequest(http.MethodPost, c.oauthConfig.Endpoint.TokenURL, strings.NewReader(accessPayload.Encode())) //nolint: noctx
	if err != nil {
		return nil, fmt.Errorf("can't create token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var jsonToken jsonToken

	statusCode, err := c.sendRequest(ctx, req, &jsonToken, false)
	if err != nil {
		if apiError, ok := err.(APIError); ok {
			if apiError.StatusCode < 500 && apiError.StatusCode != 429 {
				apiError.IsAuthError = true

				return nil, apiError
			}
		}

		return nil, err
	}

	if statusCode != 200 {
		if statusCode < 500 && statusCode != 429 {
			return nil, APIError{
				StatusCode:  statusCode,
				Content:     "Unable to authenticate",
				IsAuthError: true,
			}
		}

		return nil, APIError{
			StatusCode: statusCode,
			Content:    fmt.Sprintf("oauth request returned status code == %d, want 200", statusCode),
		}
	}

	return jsonToken.toToken(), nil
}

func (c *HTTPClient) refreshToken(ctx context.Context, token *oauth2.Token) (*oauth2.Token, error) {
	refreshPayload := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {token.RefreshToken},
		"client_id":     {c.oauthConfig.ClientID},
	}

	req, err := http.NewRequest(http.MethodPost, c.oauthConfig.Endpoint.TokenURL, strings.NewReader(refreshPayload.Encode())) //nolint: noctx
	if err != nil {
		return nil, fmt.Errorf("can't create token refresh request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var jsonToken jsonToken

	statusCode, err := c.sendRequest(ctx, req, &jsonToken, false)
	if err != nil {
		return nil, fmt.Errorf("can't send token refresh request: %w", err)
	}

	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("%w when refreshing token: %d (error code: %q)", errUnexpectedStatusCode, statusCode, jsonToken.ErrorCode)
	}

	return jsonToken.toToken(), nil
}

// VerifyAndGetToken is used to get a valid token.
// It differs from GetToken because the token is only renewed if necessary.
func (c *HTTPClient) VerifyAndGetToken(ctx context.Context, agentID string) (string, error) {
	var res struct {
		ID string
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Low cost API endpoint, used to test our token.
	path := fmt.Sprintf("v1/agent/%s/?fields=id", agentID)

	// We rely on the client to renew the token if it has expired.
	_, err := c.Do(ctx, "GET", path, nil, nil, &res)
	if err != nil {
		return "", err
	}

	if res.ID != agentID {
		return "", errInvalidAgentID
	}

	return c.token.AccessToken, nil
}

func (c *HTTPClient) sendRequest(ctx context.Context, req *http.Request, result interface{}, forceInsecure bool) (statusCode int, err error) {
	if time.Until(c.throttleDeadline) > 0 {
		return 0, APIError{
			StatusCode: 429,
			Content:    "Request was throttled",
		}
	}

	req.Header.Add("X-Requested-With", "XMLHttpRequest")
	req.Header.Add("User-Agent", version.UserAgent())

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req = req.WithContext(ctx)
	client := c.cl

	if forceInsecure {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec
		}

		client = &http.Client{
			Transport: gloutonTypes.NewHTTPTransport(tlsConfig),
		}
	}

	c.requestsCount++

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}

	defer func() {
		// Ensure we read the whole response to avoid "Connection reset by peer" on server
		// and ensure HTTP connection can be reused
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < http.StatusInternalServerError {
		c.throttleConsecutive = 0
	}

	if resp.StatusCode >= http.StatusBadRequest {
		err := fieldsFromResponse(resp, decodeError(resp))

		if resp.StatusCode == http.StatusTooManyRequests {
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

		return resp.StatusCode, err
	}

	if result != nil {
		err = json.NewDecoder(resp.Body).Decode(result)
		if err != nil {
			return resp.StatusCode, fieldsFromResponse(resp, APIError{
				StatusCode:   resp.StatusCode,
				Content:      "",
				UnmarshalErr: err,
			})
		}
	}

	return resp.StatusCode, nil
}

func fieldsFromResponse(resp *http.Response, err APIError) APIError {
	if resp == nil {
		return err
	}

	if resp.Request != nil && resp.Request.URL != nil {
		err.FinalURL = resp.Request.URL.String()
	}

	if resp.Header != nil {
		err.ContentType = resp.Header.Get("content-type")
	}

	err.StatusCode = resp.StatusCode

	return err
}

func decodeError(resp *http.Response) APIError {
	if resp.Header.Get("Content-Type") != "application/json" {
		partialBody := make([]byte, 250)
		n, _ := resp.Body.Read(partialBody)

		return APIError{
			Content:      string(partialBody[:n]),
			UnmarshalErr: nil,
			IsAuthError:  resp.StatusCode == 401,
		}
	}

	var (
		jsonMessage json.RawMessage
		jsonError   struct {
			Error          string   `json:"error"`
			Detail         string   `json:"detail"`
			NonFieldErrors []string `json:"non_field_errors"`
		}
		errorList        []string
		validationErrors []map[string][]string
	)

	err := json.NewDecoder(resp.Body).Decode(&jsonMessage)
	if err != nil {
		return APIError{
			Content:      "",
			UnmarshalErr: err,
			IsAuthError:  resp.StatusCode == 401,
		}
	}

	err = json.Unmarshal(jsonMessage, &jsonError)
	if err != nil {
		err = json.Unmarshal(jsonMessage, &errorList)
		if err != nil {
			err = json.Unmarshal(jsonMessage, &validationErrors)
		}
	}

	if err != nil {
		return APIError{
			Content:      "",
			UnmarshalErr: err,
			IsAuthError:  resp.StatusCode == 401,
		}
	}

	if len(errorList) > 0 && errorList[0] != "" {
		return APIError{
			Content:      strings.Join(errorList, ", "),
			UnmarshalErr: nil,
			IsAuthError:  resp.StatusCode == 401,
		}
	}

	if len(validationErrors) > 0 && len(validationErrors[0]) > 0 {
		var errs []string

		for _, m := range validationErrors {
			for field, validErrs := range m {
				errs = append(errs, fmt.Sprintf("invalid field %q: %s", field, strings.Join(validErrs, ", ")))
			}
		}

		return APIError{
			Content:      strings.Join(errs, ", "),
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
			Content:      errorMessage,
			UnmarshalErr: nil,
			IsAuthError:  resp.StatusCode == 401,
		}
	}

	return APIError{
		Content:      string(jsonMessage),
		UnmarshalErr: nil,
		IsAuthError:  resp.StatusCode == 401,
	}
}

type jsonToken struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int32  `json:"expires_in"`
	ErrorCode        string `json:"error"`
	ErrorDescription string `json:"error_description"`
	ErrorURI         string `json:"error_uri"`
}

func (jTk jsonToken) toToken() *oauth2.Token {
	return &oauth2.Token{
		AccessToken:  jTk.AccessToken,
		TokenType:    jTk.TokenType,
		RefreshToken: jTk.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(jTk.ExpiresIn) * time.Second),
	}
}
