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

//nolint:scopelint
package client

import (
	"bufio"
	"bytes"
	"net/http"
	"reflect"
	"testing"
)

func Test_decodeError(t *testing.T) {
	tests := []struct {
		name     string
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
			want: APIError{
				StatusCode:   400,
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
				Content:      "Unable to log in with provided credentials.",
				IsAuthError:  false,
				UnmarshalErr: nil,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(tt.response)), nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := decodeError(resp); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("decodeError() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
