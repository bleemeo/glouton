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

	"github.com/go-viper/mapstructure/v2"
)

var errReceiverNoSelector = errors.New("log.opentelemetry receiver has no source selector (include, container_name, container_selectors, or network)")

// receiverSelectors is the subset of a raw LogReceiver's keys that decide
// what it watches, narrow-decoded out of the rest of the receiver's
// (otherwise real vendored fileconsumer/filelogreceiver) fields.
type receiverSelectors struct {
	Include            []string
	ContainerName      string            `mapstructure:"container_name"`
	ContainerSelectors map[string]string `mapstructure:"container_selectors"`
	Network            OTLPNetworkParticipation
}

// LogReceiverSelectors narrow-decodes just the selector-related keys out of
// a raw LogReceiver, ignoring every other (real vendored fileconsumer/
// filelogreceiver) field it may carry. Used both by validateLogReceivers and
// by the runtime layer (e.g. to check whether a container already matches a
// configured receiver before falling back to container-label detection).
func LogReceiverSelectors(raw LogReceiver) (include []string, containerName string, containerSelectors map[string]string, network OTLPNetworkParticipation, err error) {
	var probe receiverSelectors

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{Result: &probe})
	if err != nil {
		return nil, "", nil, OTLPNetworkParticipation{}, fmt.Errorf("creating decoder: %w", err)
	}

	if err := decoder.Decode(raw); err != nil {
		return nil, "", nil, OTLPNetworkParticipation{}, err
	}

	return probe.Include, probe.ContainerName, probe.ContainerSelectors, probe.Network, nil
}

// validateLogReceivers rejects any log.opentelemetry.receivers entry with no
// source selector at all (include, container_name, container_selectors, or
// network) -- almost certainly a typo/mistake, caught at load time instead
// of silently doing nothing.
func validateLogReceivers(cfg Config) error {
	var errs []error

	for name, raw := range cfg.Log.OpenTelemetry.Receivers {
		include, containerName, containerSelectors, network, err := LogReceiverSelectors(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("log.opentelemetry.receivers.%s: %w", name, err))

			continue
		}

		hasNetwork := len(ResolveNetworkReceivers(network.Enable, network.Receivers)) > 0

		if len(include) == 0 && containerName == "" && len(containerSelectors) == 0 && !hasNetwork {
			errs = append(errs, fmt.Errorf("%w: %q", errReceiverNoSelector, name))
		}
	}

	return errors.Join(errs...)
}
