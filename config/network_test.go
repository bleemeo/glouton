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

package config

import (
	"errors"
	"testing"
)

func TestValidateNetworkListeners(t *testing.T) {
	t.Parallel()

	t.Run("no protocol at all is rejected", func(t *testing.T) {
		t.Parallel()

		cfg := Config{
			OpenTelemetry: OpenTelemetryConfig{
				NetworkListeners: map[string]NetworkListener{
					"otlp/my_custom": {},
				},
			},
		}

		err := validateNetworkListeners(cfg)
		if !errors.Is(err, errNetworkListenerNoProtocol) {
			t.Fatalf("Expected errNetworkListenerNoProtocol, got: %v", err)
		}
	})

	t.Run("grpc only is accepted", func(t *testing.T) {
		t.Parallel()

		cfg := Config{
			OpenTelemetry: OpenTelemetryConfig{
				NetworkListeners: map[string]NetworkListener{
					"otlp": {Protocols: NetworkProtocols{GRPC: &NetworkEndpoint{}}},
				},
			},
		}

		if err := validateNetworkListeners(cfg); err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
	})

	t.Run("http only is accepted", func(t *testing.T) {
		t.Parallel()

		cfg := Config{
			OpenTelemetry: OpenTelemetryConfig{
				NetworkListeners: map[string]NetworkListener{
					"otlp": {Protocols: NetworkProtocols{HTTP: &NetworkEndpoint{}}},
				},
			},
		}

		if err := validateNetworkListeners(cfg); err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
	})

	t.Run("no listeners at all is accepted", func(t *testing.T) {
		t.Parallel()

		if err := validateNetworkListeners(Config{}); err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
	})
}
