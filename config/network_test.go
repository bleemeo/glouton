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
	"reflect"
	"testing"
)

func TestResolveNetworkReceivers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		enable    bool
		receivers []string
		want      []string
	}{
		{name: "disabled, no receivers", enable: false, receivers: nil, want: nil},
		{name: "simple enable", enable: true, receivers: nil, want: []string{DefaultNetworkReceiverName}},
		{name: "explicit receivers win over enable", enable: true, receivers: []string{"custom"}, want: []string{"custom"}},
		{name: "explicit receivers without enable", enable: false, receivers: []string{"custom"}, want: []string{"custom"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := ResolveNetworkReceivers(tc.enable, tc.receivers)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestEffectiveNetworkReceivers(t *testing.T) {
	t.Parallel()

	t.Run("explicit receivers are never auto-provisioned over", func(t *testing.T) {
		t.Parallel()

		explicit := map[string]NetworkReceiver{
			"custom": {Protocols: NetworkProtocols{GRPC: &NetworkEndpoint{Endpoint: "localhost:9999"}}},
		}

		got := EffectiveNetworkReceivers(explicit, true, true)
		if !reflect.DeepEqual(got, explicit) {
			t.Errorf("Expected explicit receivers untouched, got %v", got)
		}
	})

	t.Run("no simple want leaves an empty map empty", func(t *testing.T) {
		t.Parallel()

		got := EffectiveNetworkReceivers(nil, false, false)
		if len(got) != 0 {
			t.Errorf("Expected no auto-provisioned receiver, got %v", got)
		}
	})

	t.Run("a simple want auto-provisions the default receiver", func(t *testing.T) {
		t.Parallel()

		got := EffectiveNetworkReceivers(nil, false, true)

		recv, ok := got[DefaultNetworkReceiverName]
		if !ok {
			t.Fatalf("Expected %q to be auto-provisioned, got %v", DefaultNetworkReceiverName, got)
		}

		if recv.Protocols.GRPC == nil || recv.Protocols.HTTP == nil {
			t.Errorf("Expected both GRPC and HTTP configured on the auto-provisioned receiver, got %+v", recv.Protocols)
		}
	})
}
