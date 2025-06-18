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

package mqtt

import bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"

type notificationPayloadV1 struct {
	MessageType            string `json:"message_type"`
	MetricUUID             string `json:"metric_uuid,omitempty"`
	MonitorUUID            string `json:"monitor_uuid,omitempty"`
	MonitorOperationType   string `json:"monitor_operation_type,omitempty"`
	DiagnosticRequestToken string `json:"request_token,omitempty"`
	AckTimestamp           string `json:"ack_timestamp,omitempty"`
	DataStreamAvailable    bool   `json:"data_stream_available,omitempty"`
	TopInfoStreamAvailable bool   `json:"topinfo_stream_available,omitempty"`
	LogsStreamAvailable    bool   `json:"logs_stream_available,omitempty"`
}

// notificationPayload is the current payload implementation.
// When a new one replaces it, it should be renamed to notificationPayloadV<X>,
// and checked for compatibility with TestNotificationCompatibility.
type notificationPayload struct {
	MessageType                  string                        `json:"message_type"`
	MetricUUID                   string                        `json:"metric_uuid,omitempty"`
	MonitorUUID                  string                        `json:"monitor_uuid,omitempty"`
	MonitorOperationType         string                        `json:"monitor_operation_type,omitempty"`
	DiagnosticRequestToken       string                        `json:"request_token,omitempty"`
	AckTimestamp                 string                        `json:"ack_timestamp,omitempty"`
	DataStreamAvailable          bool                          `json:"data_stream_available,omitempty"`
	TopInfoStreamAvailable       bool                          `json:"topinfo_stream_available,omitempty"`
	LogsStreamAvailabilityStatus bleemeoTypes.LogsAvailability `json:"logs_stream_availability_status,omitempty"`
}
