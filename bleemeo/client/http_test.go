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

//nolint:scopelint
package client

import (
	"bufio"
	"bytes"
	"context"
	"net/http"
	"testing"
)

func Test_decodeError(t *testing.T) {
	tests := []struct {
		name     string
		reqURL   string
		response []byte
		want     APIError
	}{
		{
			name: "too-many-connected-agent",
			response: []byte(`HTTP/1.1 400 Bad Request
Date: Mon, 01 Jun 2020 07:54:37 GMT
Server: WSGIServer/0.2 CPython/3.6.9
Content-Type: application/json
Vary: Accept
Allow: GET, POST, HEAD, OPTIONS
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Content-Length: 52

["Usage exceeded. You can connect up to 8 servers."]`),
			reqURL: "http://localhost:8000/v1/agent/",
			want: APIError{
				StatusCode:   400,
				ContentType:  "application/json",
				FinalURL:     "http://localhost:8000/v1/agent/",
				Content:      "Usage exceeded. You can connect up to 8 servers.",
				IsAuthError:  false,
				UnmarshalErr: nil,
			},
		},
		{
			name: "bad-account-registration-key",
			response: []byte(`HTTP/1.1 401 Unauthorized
Date: Mon, 01 Jun 2020 08:10:42 GMT
Server: WSGIServer/0.2 CPython/3.6.9
Content-Type: application/json
WWW-Authenticate: JWT realm="api"
Vary: Accept
Allow: GET, POST, HEAD, OPTIONS
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Content-Length: 39

{"detail":"Invalid username/password."}`),
			want: APIError{
				StatusCode:   401,
				ContentType:  "application/json",
				Content:      "Invalid username/password.",
				IsAuthError:  true,
				UnmarshalErr: nil,
			},
		},
		{
			name: "bad-agent-password",
			response: []byte(`HTTP/1.1 400 Bad Request
Content-Type: application/json
Vary: Accept
Allow: POST, OPTIONS
Content-Length: 68

{"non_field_errors":["Unable to log in with provided credentials."]}`),
			want: APIError{
				StatusCode:   400,
				ContentType:  "application/json",
				Content:      "Unable to log in with provided credentials.",
				IsAuthError:  false,
				UnmarshalErr: nil,
			},
		},
		{
			name: "field-failing-validation",
			response: []byte(`HTTP/1.1 400 Bad Request
Content-Type: application/json
Vary: Accept
Allow: GET, POST, HEAD, OPTIONS
Content-Length: 43

[{"value":["This field may not be null."]}]`),
			want: APIError{
				StatusCode:   400,
				ContentType:  "application/json",
				Content:      "invalid field \"value\": This field may not be null.",
				IsAuthError:  false,
				UnmarshalErr: nil,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, tt.reqURL, nil)
			if err != nil {
				t.Fatal(err)
			}

			resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(tt.response)), req)
			if err != nil {
				t.Fatal(err)
			}

			defer resp.Body.Close()

			got := fieldsFromResponse(resp, decodeError(resp))

			if got.Content != tt.want.Content {
				t.Errorf("decodeError().Content = %#v, want %#v", APIErrorContent(got), APIErrorContent(tt.want))
			}

			if got.StatusCode != tt.want.StatusCode {
				t.Errorf("decodeError().StatusCode = %#v, want %#v", got.StatusCode, tt.want.StatusCode)
			}

			if got.ContentType != tt.want.ContentType {
				t.Errorf("decodeError().ContentType = %#v, want %#v", got.ContentType, tt.want.ContentType)
			}

			if got.FinalURL != tt.want.FinalURL {
				t.Errorf("decodeError().FinalURL = %#v, want %#v", got.FinalURL, tt.want.FinalURL)
			}

			isFuns := []struct {
				name string
				fun  func(err error) bool
			}{
				{name: "IsAuthError", fun: IsAuthError},
				{name: "IsNotFound", fun: IsNotFound},
				{name: "IsBadRequest", fun: IsBadRequest},
				{name: "IsServerError", fun: IsServerError},
				{name: "IsThrottleError", fun: IsThrottleError},
			}

			for _, f := range isFuns {
				if f.fun(got) != f.fun(tt.want) {
					t.Errorf("%s() = %v, want %v", f.name, f.fun(got), f.fun(tt.want))
				}
			}
		})
	}
}
