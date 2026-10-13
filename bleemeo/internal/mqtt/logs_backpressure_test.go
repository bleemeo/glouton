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

package mqtt

import (
	"testing"
	"time"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"

	"github.com/google/go-cmp/cmp"
)

func TestLogsBackPressure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		connected    bool
		sinceLastAck time.Duration
		neverAcked   bool
		pending      int
		apiStatus    bleemeoTypes.LogsAvailability
		wantStatus   bleemeoTypes.LogsAvailability
		wantCauses   []string
	}{
		{
			name:         "ok",
			connected:    true,
			sinceLastAck: 10 * time.Second,
			apiStatus:    bleemeoTypes.LogsAvailabilityOk,
			wantStatus:   bleemeoTypes.LogsAvailabilityOk,
		},
		{
			name:         "API asks to discard",
			connected:    true,
			sinceLastAck: 10 * time.Second,
			apiStatus:    bleemeoTypes.LogsAvailabilityShouldDiscard,
			wantStatus:   bleemeoTypes.LogsAvailabilityShouldDiscard,
			wantCauses:   []string{"the Bleemeo API sets the logs stream availability to discard"},
		},
		{
			name:       "disconnected and never acked buffers, whatever the API said",
			neverAcked: true,
			apiStatus:  bleemeoTypes.LogsAvailabilityOk,
			wantStatus: bleemeoTypes.LogsAvailabilityShouldBuffer,
			wantCauses: []string{"MQTT is not connected", "no MQTT ack received yet"},
		},
		{
			name:         "old ack and too many pending messages",
			connected:    true,
			sinceLastAck: 5 * time.Minute,
			pending:      maxMQTTPendingMessagesToAllowLogsThreshold + 1,
			apiStatus:    bleemeoTypes.LogsAvailabilityShouldDiscard,
			wantStatus:   bleemeoTypes.LogsAvailabilityShouldBuffer,
			wantCauses:   []string{"no MQTT ack for 5m0s (more than 1m10s)", "51 MQTT messages pending (more than 50)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, causes := logsBackPressure(tt.connected, tt.sinceLastAck, tt.neverAcked, tt.pending, tt.apiStatus)
			if status != tt.wantStatus {
				t.Errorf("status = %s, want %s", status, tt.wantStatus)
			}

			if diff := cmp.Diff(tt.wantCauses, causes); diff != "" {
				t.Errorf("causes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
