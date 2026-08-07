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
	"fmt"
)

var errNetworkListenerNoProtocol = errors.New("opentelemetry.listeners entry has no protocol enabled (grpc or http)")

// validateNetworkListeners rejects any opentelemetry.listeners entry with neither grpc nor http
// configured -- almost certainly a typo/mistake (e.g. an empty "protocols:" block). Without this, such
// an entry silently listens on nothing: otlpreceiver.Config.Validate() does reject it, but only once a
// receiver references it and the agent tries to start it at runtime, and even then the failure only hit
// the log file instead of the same load-time errors every other misconfigured receiver produces.
func validateNetworkListeners(cfg Config) error {
	var errs []error

	for name, listener := range cfg.OpenTelemetry.NetworkListeners {
		if listener.Protocols.GRPC == nil && listener.Protocols.HTTP == nil {
			errs = append(errs, fmt.Errorf("%w: %q", errNetworkListenerNoProtocol, name))
		}
	}

	return errors.Join(errs...)
}
