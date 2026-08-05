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

// DefaultNetworkReceiverName is the opentelemetry.network.receivers entry auto-provisioned
// by EffectiveNetworkReceivers for the simple "enable: true" shortcut.
const DefaultNetworkReceiverName = "otlp"

// ResolveNetworkReceivers returns the receivers a participant pulls from: its
// own explicit list if set, else DefaultNetworkReceiverName if it just set
// enable, else none.
func ResolveNetworkReceivers(enable bool, receivers []string) []string {
	if len(receivers) > 0 {
		return receivers
	}

	if enable {
		return []string{DefaultNetworkReceiverName}
	}

	return nil
}

// EffectiveNetworkReceivers returns networkReceivers unchanged if it already
// has an entry (naming one yourself disables auto-provisioning). Otherwise, if
// any simpleWant is true, it auto-provisions a single DefaultNetworkReceiverName
// entry on the standard OTLP ports.
func EffectiveNetworkReceivers(networkReceivers map[string]NetworkReceiver, simpleWants ...bool) map[string]NetworkReceiver {
	if len(networkReceivers) > 0 {
		return networkReceivers
	}

	for _, want := range simpleWants {
		if want {
			return map[string]NetworkReceiver{
				DefaultNetworkReceiverName: {
					Protocols: NetworkProtocols{
						GRPC: &NetworkEndpoint{Endpoint: "localhost:4317"},
						HTTP: &NetworkEndpoint{Endpoint: "localhost:4318"},
					},
				},
			}
		}
	}

	return networkReceivers
}
