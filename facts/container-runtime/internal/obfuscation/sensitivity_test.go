// Copyright 2015-2025 Bleemeo
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

package obfuscation

import (
	"testing"
)

func TestIsSensitive(t *testing.T) {
	cases := []struct {
		key               string
		expectedSensitive bool
	}{
		{
			key:               "minio_pubkey",
			expectedSensitive: false,
		},
		{
			key:               "minio_public_key",
			expectedSensitive: false,
		},
		{
			key:               "public_minio_key",
			expectedSensitive: true,
		},
		{
			key:               "minio_key_file",
			expectedSensitive: false,
		},
		{
			key:               "minio_cert_file_password",
			expectedSensitive: true,
		},
		{
			key:               "PASSWORD_PUBLIC_KEY",
			expectedSensitive: true,
		},
		{
			key:               "PUBKEY_PASSWORD",
			expectedSensitive: true,
		},
		{
			key:               "KEY-FOR-PUBLIC-KEY",
			expectedSensitive: true,
		},
		{
			key:               "pubkey_with_key_and_key_file",
			expectedSensitive: true,
		},
		{
			key:               "keyfile_public_key",
			expectedSensitive: false,
		},
		{
			key:               "BLEEMEO_OAUTH_INITIAL_REFRESH_TOKEN",
			expectedSensitive: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			sensitive := isSensitive(tc.key)
			if sensitive != tc.expectedSensitive {
				if tc.expectedSensitive {
					t.Fatalf("Expected %q to be sensitive", tc.key)
				} else {
					t.Fatalf("Expected %q not to be sensitive", tc.key)
				}
			}
		})
	}
}
