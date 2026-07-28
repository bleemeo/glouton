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

// DefaultNetworkReceiverName is the log.network.receivers entry a feature's
// simple "enable: true" participation (see OTLPNetworkParticipation,
// LogMetricsNetworkReceiver) resolves to when it names no explicit receivers
// of its own, and the entry EffectiveNetworkReceivers auto-provisions when
// log.network.receivers is left completely empty.
const DefaultNetworkReceiverName = "otlp"

// ResolveNetworkReceivers returns the log.network.receivers entries a
// participant pulls from: its own explicit receivers if any are named
// (advanced mode -- enable is then irrelevant), or DefaultNetworkReceiverName
// if it opted into the simple enable-only shortcut instead, or none.
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
// has any entry -- naming even one receiver yourself takes full manual
// control, real OTel has no auto-provisioning either. Otherwise, if any
// simpleWant is true (a participant opted into the simple enable-only
// shortcut with no receivers of its own), it returns a single
// DefaultNetworkReceiverName entry with both GRPC and HTTP on the standard
// OTLP ports -- so the common single-listener case needs no receiver defined
// anywhere. Returns networkReceivers unchanged (possibly empty) if no
// participant wants the shortcut.
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
