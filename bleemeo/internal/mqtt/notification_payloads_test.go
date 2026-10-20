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
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/go-cmp/cmp"
)

// TestNotificationCompatibility ensures that newer payload from Bleemeo could be decoded into older struct.
// This is used to ensure non-updated Glouton continues to work even if Bleemeo side is updated.
func TestNotificationCompatibility(t *testing.T) {
	t.Parallel()

	// Every time a new payload format is designed on the Bleemeo side,
	// it should be added to this list to ensure its compatibility against older Glouton versions.
	payloadVersions := [][]byte{
		[]byte(`{"message_type": "ack", "ack_timestamp": "2025-01-09T08:57:40+00:00", "data_stream_available": true, "topinfo_stream_available": true, "logs_stream_available": false}`),
		[]byte(`{"message_type": "ack", "ack_timestamp": "2025-01-09T08:57:40+00:00", "data_stream_available": true, "topinfo_stream_available": true, "logs_stream_availability_status": 1}`),
	}

	typeVersions := []any{ // any -> notificationPayloadV1, notificationPayload, ...
		notificationPayloadV1{
			MessageType:            messageTypeAck,
			AckTimestamp:           "2025-01-09T08:57:40+00:00",
			DataStreamAvailable:    true,
			TopInfoStreamAvailable: true,
			LogsStreamAvailable:    false, // Logs weren't actually used at this moment, so this field should always resolve to false.
		},
		notificationPayload{
			MessageType:                  messageTypeAck,
			AckTimestamp:                 "2025-01-09T08:57:40+00:00",
			DataStreamAvailable:          true,
			TopInfoStreamAvailable:       true,
			LogsStreamAvailabilityStatus: bleemeoTypes.LogsAvailabilityOk,
		},
	}

	for payloadVersion, payload := range payloadVersions {
		for typeVersion, expectedPayload := range typeVersions {
			if payloadVersion < typeVersion {
				// A payload is always at least as recent as the type.
				continue
			}

			t.Run(fmt.Sprintf("payload v%d to type v%d", payloadVersion+1, typeVersion+1), func(t *testing.T) {
				t.Parallel()

				payloadObjPtr := reflect.New(reflect.TypeOf(expectedPayload)).Interface()

				err := json.Unmarshal(payload, payloadObjPtr)
				if err != nil {
					t.Fatalf("Failed to unmarshal payload: %v -> Did the payload change its format ?", err)
				}

				payloadObj := reflect.Indirect(reflect.ValueOf(payloadObjPtr)).Interface()
				if diff := cmp.Diff(expectedPayload, payloadObj); diff != "" {
					t.Fatalf("Unexpected payload (-want +got):\n%s\n-> Did the payload change its format ?", diff)
				}
			})
		}
	}
}

// payloadMessage is a paho.Message which only has a payload.
type payloadMessage struct {
	paho.Message

	payload []byte
}

func (m payloadMessage) Payload() []byte {
	return m.payload
}

// TestMonitorUpdateNotification ensures a monitor-update notification requests the update
// of the monitor identified by its Monitor ID (monitor_id), not by its Service ID (monitor_uuid).
func TestMonitorUpdateNotification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		wantOp  string
		wantID  string
	}{
		{
			name: "monitor_id",
			payload: `{"message_type": "monitor-update", "monitor_operation_type": "change",` +
				` "monitor_uuid": "fdd9d999-e2ff-45d3-af2b-6519cf8e3e70", "monitor_id": "3b2e1f3a-8c0d-4f6e-9a57-1d2c3b4a5e6f"}`,
			wantOp: "change",
			wantID: "3b2e1f3a-8c0d-4f6e-9a57-1d2c3b4a5e6f",
		},
		{
			name: "delete",
			payload: `{"message_type": "monitor-update", "monitor_operation_type": "delete",` +
				` "monitor_uuid": "fdd9d999-e2ff-45d3-af2b-6519cf8e3e70", "monitor_id": "3b2e1f3a-8c0d-4f6e-9a57-1d2c3b4a5e6f"}`,
			wantOp: "delete",
			wantID: "3b2e1f3a-8c0d-4f6e-9a57-1d2c3b4a5e6f",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calls [][2]string

			c := &Client{
				opts: Option{
					UpdateMonitor: func(op string, uuid string) {
						calls = append(calls, [2]string{op, uuid})
					},
				},
			}

			c.onNotification(t.Context(), payloadMessage{payload: []byte(tt.payload)})

			want := [][2]string{{tt.wantOp, tt.wantID}}
			if diff := cmp.Diff(want, calls); diff != "" {
				t.Errorf("UpdateMonitor calls mismatch (-want +got)\n%s", diff)
			}
		})
	}
}
