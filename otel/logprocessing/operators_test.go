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

package logprocessing

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/fileconsumer/attrs"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	noopM "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
)

//nolint: gosmopolitan,gofmt,gofumpt,goimports
func TestKnownLogFormats(t *testing.T) { //nolint: maintidx
	t.Parallel()

	utcPlus1 := time.FixedZone("UTC", int(time.Hour.Seconds()))
	utcPlus2 := time.FixedZone("UTC", 2*int(time.Hour.Seconds()))

	noTS := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

	testCases := []struct {
		name            string // must be a valid file name (no spaces, special chars, ...)
		logFormat       string
		inputLogs       []string
		expectedRecords []logRecord // dynamic attributes such as file name/path will be added automatically
	}{
		{
			name:      "raw",
			logFormat: "",
			inputLogs: []string{
				"This is a raw log line",
			},
			expectedRecords: []logRecord{
				{
					Timestamp: noTS,
					Body:      "This is a raw log line",
				},
			},
		},
		{
			name:      "json-golang-slog",
			logFormat: "json_golang_slog",
			inputLogs: []string{
				`{"time":"2025-01-20T15:22:11.377443+01:00","level":"INFO","msg":"starting","go_version":"go1.23.5"}`,
			},
			expectedRecords: []logRecord{
				{
					Timestamp: time.Date(2025, 1, 20, 15, 22, 11, 377443000, utcPlus1),
					Body:      `{"time":"2025-01-20T15:22:11.377443+01:00","level":"INFO","msg":"starting","go_version":"go1.23.5"}`,
					Attributes: map[string]any{
						"go_version": "go1.23.5",
					},
					Severity: 9,
				},
			},
		},
		{
			name:      "nginx-access",
			logFormat: "nginx_access",
			inputLogs: []string{
				`127.0.0.1 - - [04/Apr/2025:11:14:50 +0200] "GET / HTTP/1.1" 200 396 "-" "Glouton 0.1"`,
				`127.0.0.1 - user [04/Apr/2025:11:15:18 +0200] "GET /favicon.ico HTTP/1.1" 404 134 "http://localhost" "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:137.0) Gecko/20100101 Firefox/137.0"`,
			},
			expectedRecords: []logRecord{
				{
					Timestamp: time.Date(2025, 4, 4, 11, 14, 50, 0, utcPlus2),
					Body:      `127.0.0.1 - - [04/Apr/2025:11:14:50 +0200] "GET / HTTP/1.1" 200 396 "-" "Glouton 0.1"`,
					Attributes: map[string]any{
						"client.address":            "127.0.0.1",
						"http.connection.state":     "-",
						"http.request.method":       "GET",
						"http.response.size":        "396",
						"http.response.status_code": "200",
						"log.iostream":              "stdout",
						"user.name":                 "-",
						"user_agent.original":       "Glouton 0.1",
					},
					Severity: 5,
				},
				{
					Timestamp: time.Date(2025, 4, 4, 11, 15, 18, 0, utcPlus2),
					Body:      `127.0.0.1 - user [04/Apr/2025:11:15:18 +0200] "GET /favicon.ico HTTP/1.1" 404 134 "http://localhost" "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:137.0) Gecko/20100101 Firefox/137.0"`,
					Attributes: map[string]any{
						"client.address":            "127.0.0.1",
						"http.connection.state":     "http://localhost",
						"http.request.method":       "GET",
						"http.response.size":        "134",
						"http.response.status_code": "404",
						"log.iostream":              "stdout",
						"user.name":                 "user",
						"user_agent.original":       "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:137.0) Gecko/20100101 Firefox/137.0",
					},
					Severity: 13,
				},
			},
		},
		{
			name:      "nginx-error",
			logFormat: "nginx_error",
			inputLogs: []string{
				`2025/04/04 11:48:45 [error] 29#29: *1 open() "/usr/share/nginx/html/favicon.ico" failed (2: No such file or directory), client: 172.17.0.1, server: localhost, request: "GET /favicon.ico HTTP/1.1", host: "localhost:8080", referrer: "http://localhost:8080/50x.html"`,
			},
			expectedRecords: []logRecord{
				{
					Timestamp: time.Date(2025, 4, 4, 11, 48, 45, 0, time.Local),
					Body:      `2025/04/04 11:48:45 [error] 29#29: *1 open() "/usr/share/nginx/html/favicon.ico" failed (2: No such file or directory), client: 172.17.0.1, server: localhost, request: "GET /favicon.ico HTTP/1.1", host: "localhost:8080", referrer: "http://localhost:8080/50x.html"`,
					Attributes: map[string]any{
						"client.address":        "172.17.0.1",
						"http.request.method":   "GET",
						"log.iostream":          "stderr",
						"network.local.address": "localhost",
						"server.address":        "localhost",
						"server.port":           "8080",
					},
					Severity: 17,
				},
			},
		},
		{
			name:      "apache-access",
			logFormat: "apache_access",
			inputLogs: []string{
				`172.17.0.1 - - [04/Apr/2025:09:53:14 +0000] "GET / HTTP/1.1" 200 45`,
				`172.17.0.1 - - [04/Apr/2025:09:53:21 +0000] "GET /nginx_status HTTP/1.1" 404 196`,
				`172.17.0.1 - - [04/Apr/2025:09:53:22 +0000] "-" 408 -`,
			},
			expectedRecords: []logRecord{
				{
					Timestamp: time.Date(2025, 4, 4, 9, 53, 14, 0, time.UTC),
					Body:      `172.17.0.1 - - [04/Apr/2025:09:53:14 +0000] "GET / HTTP/1.1" 200 45`,
					Attributes: map[string]any{
						"client.address":            "172.17.0.1",
						"http.request.method":       "GET",
						"http.response.size":        "45",
						"http.response.status_code": "200",
						"log.iostream":              "stdout",
						"user.name":                 "-",
					},
					Severity: 5,
				},
				{
					Timestamp: time.Date(2025, 4, 4, 9, 53, 21, 0, time.UTC),
					Body:      `172.17.0.1 - - [04/Apr/2025:09:53:21 +0000] "GET /nginx_status HTTP/1.1" 404 196`,
					Attributes: map[string]any{
						"client.address":            "172.17.0.1",
						"http.request.method":       "GET",
						"http.response.size":        "196",
						"http.response.status_code": "404",
						"log.iostream":              "stdout",
						"user.name":                 "-",
					},
					Severity: 13,
				},
				{
					Timestamp: time.Date(2025, 4, 4, 9, 53, 22, 0, time.UTC),
					Body:      "172.17.0.1 - - [04/Apr/2025:09:53:22 +0000] \"-\" 408 -",
					Attributes: map[string]any{
						"client.address":            "172.17.0.1",
						"http.response.status_code": "408",
						"log.iostream":              "stdout",
						"user.name":                 "-",
					},
					Severity: 13,
				},
			},
		},
		{
			name:      "apache-error",
			logFormat: "apache_error",
			inputLogs: []string{
				`[Fri Apr 04 11:53:04.199554 2025] [mpm_event:notice] [pid 1:tid 1] AH00489: Apache/2.4.63 (Unix) configured -- resuming normal operations`,
				`[Fri Apr 04 11:54:29.902022 2025] [core:error] [pid 35708:tid 4328636416] [client 72.15.99.187] File does not exist: /usr/local/apache2/htdocs/favicon.ico`,
			},
			expectedRecords: []logRecord{
				{
					Timestamp: time.Date(2025, 4, 4, 11, 53, 4, 199554000, time.Local),
					Body:      "[Fri Apr 04 11:53:04.199554 2025] [mpm_event:notice] [pid 1:tid 1] AH00489: Apache/2.4.63 (Unix) configured -- resuming normal operations",
					Attributes: map[string]any{
						"log.iostream": "stderr",
						"process.pid":  "1",
						"thread.id":    "1",
					},
					Severity: 10,
				},
				{
					Timestamp: time.Date(2025, 4, 4, 11, 54, 29, 902022000, time.Local),
					Body:      "[Fri Apr 04 11:54:29.902022 2025] [core:error] [pid 35708:tid 4328636416] [client 72.15.99.187] File does not exist: /usr/local/apache2/htdocs/favicon.ico",
					Attributes: map[string]any{
						"client.address": "72.15.99.187",
						"log.iostream":   "stderr",
						"process.pid":    "35708",
						"thread.id":      "4328636416",
					},
					Severity: 17,
				},
			},
		},
		{
			name:      "kafka",
			logFormat: "kafka",
			inputLogs: []string{
				`[2025-04-04 12:09:35,863] INFO [ReplicaFetcherManager on broker 1] Removed fetcher for partitions Set(test-topic-0) (kafka.server.ReplicaFetcherManager)`,
				`[2025-04-04 12:09:35,883] INFO [LogLoader partition=test-topic-0, dir=/tmp/kraft-combined-logs] Loading producer state till offset 0 (org.apache.kafka.storage.internals.log.UnifiedLog)`,
				`[2025-04-04 13:33:13,847] WARN [GroupCoordinator id=1 topic=__consumer_offsets partition=22] Group console-consumer-54101 with generation 2 is now empty. (org.apache.kafka.coordinator.group.GroupMetadataManager)`,
			},
			expectedRecords: []logRecord{
				{
					Timestamp: time.Date(2025, 4, 4, 12, 9, 35, 863000000, time.Local),
					Body:      `[2025-04-04 12:09:35,863] INFO [ReplicaFetcherManager on broker 1] Removed fetcher for partitions Set(test-topic-0) (kafka.server.ReplicaFetcherManager)`,
					Attributes: map[string]any{
						"messaging.system": "kafka",
					},
					Severity: 9,
				},
				{
					Timestamp: time.Date(2025, 4, 4, 12, 9, 35, 883000000, time.Local),
					Body:      `[2025-04-04 12:09:35,883] INFO [LogLoader partition=test-topic-0, dir=/tmp/kraft-combined-logs] Loading producer state till offset 0 (org.apache.kafka.storage.internals.log.UnifiedLog)`,
					Attributes: map[string]any{
						"messaging.destination.name": "test-topic-0",
						"messaging.system":           "kafka",
					},
					Severity: 9,
				},
				{
					Timestamp: time.Date(2025, 4, 4, 13, 33, 13, 847000000, time.Local),
					Body:      `[2025-04-04 13:33:13,847] WARN [GroupCoordinator id=1 topic=__consumer_offsets partition=22] Group console-consumer-54101 with generation 2 is now empty. (org.apache.kafka.coordinator.group.GroupMetadataManager)`,
					Attributes: map[string]any{
						"messaging.destination.partition.id": "22",
						"messaging.system":                   "kafka",
					},
					Severity: 13,
				},
			},
		},
		{
			name:      "redis",
			logFormat: "redis",
			inputLogs: []string{
				`1:M 04 Apr 2025 13:52:29.632 * Background saving started by pid 104`,
				`104:C 04 Apr 2025 13:52:29.650 * DB saved on disk`,
			},
			expectedRecords: []logRecord{
				{
					Timestamp: time.Date(2025, 4, 4, 13, 52, 29, 632000000, time.Local),
					Body:      `1:M 04 Apr 2025 13:52:29.632 * Background saving started by pid 104`,
					Attributes: map[string]any{
						"db.system.name": "redis",
						"process.pid":    "1",
					},
					Severity: 9,
				},
				{
					Timestamp: time.Date(2025, 4, 4, 13, 52, 29, 650000000, time.Local),
					Body:      `104:C 04 Apr 2025 13:52:29.650 * DB saved on disk`,
					Attributes: map[string]any{
						"db.system.name": "redis",
						"process.pid":    "104",
					},
					Severity: 9,
				},
			},
		},
		{
			name:      "haproxy",
			logFormat: "haproxy",
			inputLogs: []string{
				`[NOTICE]   (1) : Loading success.`,
				`[WARNING]  (8) : Server http_back/api is DOWN, reason: Layer4 connection problem, info: "Connection refused", check duration: 0ms. 0 active and 0 backup servers left. 0 sessions active, 0 requeued, 0 remaining in queue.`,
				`[ALERT]    (8) : backend 'http_back' has no server available!`,
				`::ffff:241.1.2.3:1685 [05/May/2025:15:24:34.006] http-in~ be_ingest/10.75.1.2 0/0/1/88/89 200 188 - - ---- 8/4/0/0/0 0/0 "POST /cloud/api/v2/iot/someid/telemetry HTTP/1.1"`,
				`127.0.0.1:38074 [12/May/2025:09:52:07.313] web_front web_back/web1 0/0/0/2/2 404 466 - - ---- 1/1/0/1/0 0/0 "GET /some-route HTTP/1.1"`,
			},
			expectedRecords: []logRecord{
				{
					Timestamp: noTS,
					Body:      `[NOTICE]   (1) : Loading success.`,
					Attributes: map[string]any{
						"process.pid": "1",
					},
					Severity: 10,
				},
				{
					Timestamp: noTS,
					Body:      `[WARNING]  (8) : Server http_back/api is DOWN, reason: Layer4 connection problem, info: "Connection refused", check duration: 0ms. 0 active and 0 backup servers left. 0 sessions active, 0 requeued, 0 remaining in queue.`,
					Attributes: map[string]any{
						"process.pid": "8",
					},
					Severity: 13,
				},
				{
					Timestamp: noTS,
					Body:      `[ALERT]    (8) : backend 'http_back' has no server available!`,
					Attributes: map[string]any{
						"process.pid": "8",
					},
					Severity: 19,
				},
				{
					Timestamp: time.Date(2025, 5, 5, 15, 24, 34, 6000000, time.Local),
					Body:      `::ffff:241.1.2.3:1685 [05/May/2025:15:24:34.006] http-in~ be_ingest/10.75.1.2 0/0/1/88/89 200 188 - - ---- 8/4/0/0/0 0/0 "POST /cloud/api/v2/iot/someid/telemetry HTTP/1.1"`,
					Attributes: map[string]any{
						"client.address":            "::ffff:241.1.2.3",
						"client.port":               "1685",
						"http.response.status_code": "200",
						"http.response.size":        "188",
						"http.request.method":       "POST",
					},
					Severity: 5,
				},
				{
					Timestamp: time.Date(2025, 5, 12, 9, 52, 7, 313000000, time.Local),
					Body:      `127.0.0.1:38074 [12/May/2025:09:52:07.313] web_front web_back/web1 0/0/0/2/2 404 466 - - ---- 1/1/0/1/0 0/0 "GET /some-route HTTP/1.1"`,
					Attributes: map[string]any{
						"client.address":            "127.0.0.1",
						"client.port":               "38074",
						"http.response.status_code": "404",
						"http.response.size":        "466",
						"http.request.method":       "GET",
					},
					Severity: 13,
				},
			},
		},
		{
			name:      "postgres",
			logFormat: "postgresql",
			inputLogs: []string{
				`2025-04-04 14:39:09.167 UTC [27] LOG:  checkpoint starting: time`,
				`2025-04-04 14:41:59.076 UTC [2768] LOG:  duration: 42.292 ms  statement: DELETE FROM "silk_response" WHERE "silk_response"."request_id" IN ('63b0e9b6-5ba2-432d-a299-e983d089aa0b', '6edc9f33-191f-4057-9630-e5e4355abc2c')`,
			},
			expectedRecords: []logRecord{
				{
					Timestamp: time.Date(2025, 4, 4, 14, 39, 9, 167000000, time.UTC),
					Body:      `2025-04-04 14:39:09.167 UTC [27] LOG:  checkpoint starting: time`,
					Attributes: map[string]any{
						"db.system.name": "postgresql",
						"process.pid":    "27",
					},
				},
				{
					Timestamp: time.Date(2025, 4, 4, 14, 41, 59, 76000000, time.UTC),
					Body:      `2025-04-04 14:41:59.076 UTC [2768] LOG:  duration: 42.292 ms  statement: DELETE FROM "silk_response" WHERE "silk_response"."request_id" IN ('63b0e9b6-5ba2-432d-a299-e983d089aa0b', '6edc9f33-191f-4057-9630-e5e4355abc2c')`,
					Attributes: map[string]any{
						"db.query.text":  `DELETE FROM "silk_response" WHERE "silk_response"."request_id" IN ('63b0e9b6-5ba2-432d-a299-e983d089aa0b', '6edc9f33-191f-4057-9630-e5e4355abc2c')`,
						"db.system.name": "postgresql",
						"process.pid":    "2768",
					},
				},
			},
		},
		{
			name:      "mysql",
			logFormat: "mysql",
			inputLogs: []string{
				// `2025-04-04 14:50:02+00:00 [Note] [Entrypoint]: Entrypoint script for MySQL Server 9.2.0-1.el9 started.`,
				`2025-04-04T14:50:03.474893Z 0 [System] [MY-015017] [Server] MySQL Server Initialization - start.`,
				`2025-04-04T14:50:05.193871Z 6 [Warning] [MY-010453] [Server] root@localhost is created with an empty password ! Please consider switching off the --initialize-insecure option.`,
			},
			expectedRecords: []logRecord{
				/*{
					Timestamp: time.Date(2025, 4, 4, 14, 50, 2, 0, time.UTC),
					Body:      `2025-04-04 14:50:02+00:00 [Note] [Entrypoint]: Entrypoint script for MySQL Server 9.2.0-1.el9 started.`,
					Attributes: map[string]any{
						"db.system.name": "mysql",
					},
					Severity: 0,
				},*/
				{
					Timestamp: time.Date(2025, 4, 4, 14, 50, 3, 474893000, time.UTC),
					Body:      `2025-04-04T14:50:03.474893Z 0 [System] [MY-015017] [Server] MySQL Server Initialization - start.`,
					Attributes: map[string]any{
						"db.response.status_code": "MY-015017",
						"db.system.name":          "mysql",
						"thread.id":               "0",
					},
					Severity: 9,
				},
				{
					Timestamp: time.Date(2025, 4, 4, 14, 50, 5, 193871000, time.UTC),
					Body:      `2025-04-04T14:50:05.193871Z 6 [Warning] [MY-010453] [Server] root@localhost is created with an empty password ! Please consider switching off the --initialize-insecure option.`,
					Attributes: map[string]any{
						"db.response.status_code": "MY-010453",
						"db.system.name":          "mysql",
						"thread.id":               "6",
					},
					Severity: 13,
				},
			},
		},
		{
			name:      "mongodb",
			logFormat: "mongodb",
			inputLogs: []string{
				`{"t":{"$date":"2025-04-04T14:59:59.934+00:00"},"s":"I",  "c":"INDEX",    "id":20345,   "ctx":"conn7","msg":"Index build: done building","attr":{"buildUUID":null,"collectionUUID":{"uuid":{"$uuid":"e8be8d1e-3c09-4de4-8f4e-2fd1b2ddad91"}},"namespace":"my-db.my-collection","index":"_id_","ident":"index-8-8921814859363955208","collectionIdent":"collection-7-8921814859363955208","commitTimestamp":null}}`,
				`{"t":{"$date":"2025-04-04T15:00:00.016+00:00"},"s":"W",  "c":"CONTROL",  "id":636300,  "ctx":"ftdc","msg":"Use of deprecated server parameter name","attr":{"deprecatedName":"internalQueryCacheSize","canonicalName":"internalQueryCacheMaxEntriesPerCollection"}}`,
			},
			expectedRecords: []logRecord{
				{
					Timestamp: time.Date(2025, 4, 4, 14, 59, 59, 934000000, time.UTC),
					Body:      `{"t":{"$date":"2025-04-04T14:59:59.934+00:00"},"s":"I",  "c":"INDEX",    "id":20345,   "ctx":"conn7","msg":"Index build: done building","attr":{"buildUUID":null,"collectionUUID":{"uuid":{"$uuid":"e8be8d1e-3c09-4de4-8f4e-2fd1b2ddad91"}},"namespace":"my-db.my-collection","index":"_id_","ident":"index-8-8921814859363955208","collectionIdent":"collection-7-8921814859363955208","commitTimestamp":null}}`,
					Attributes: map[string]any{
						"db.collection.name": "my-db.my-collection",
						"db.system.name":     "mongodb",
					},
					Severity: 9,
				},
				{
					Timestamp: time.Date(2025, 4, 4, 15, 0, 0, 16000000, time.UTC),
					Body:      `{"t":{"$date":"2025-04-04T15:00:00.016+00:00"},"s":"W",  "c":"CONTROL",  "id":636300,  "ctx":"ftdc","msg":"Use of deprecated server parameter name","attr":{"deprecatedName":"internalQueryCacheSize","canonicalName":"internalQueryCacheMaxEntriesPerCollection"}}`,
					Attributes: map[string]any{
						"db.system.name": "mongodb",
					},
					Severity: 13,
				},
			},
		},
		{
			name:      "rabbitmq",
			logFormat: "rabbitmq",
			inputLogs: []string{
				`2025-04-04 15:17:07.803117+00:00 [info] <0.5147.0> accepting AMQP connection <0.5147.0> (127.0.0.1:37542 -> 127.0.0.1:5672)`,
				`2025-04-04 15:17:57.802102+00:00 [error] <0.5171.0> {bad_header,<<"PINGAMQP">>}`,
			},
			expectedRecords: []logRecord{
				{
					Timestamp: time.Date(2025, 4, 4, 15, 17, 7, 803117000, time.UTC),
					Body:      `2025-04-04 15:17:07.803117+00:00 [info] <0.5147.0> accepting AMQP connection <0.5147.0> (127.0.0.1:37542 -> 127.0.0.1:5672)`,
					Attributes: map[string]any{
						"messaging.system": "rabbitmq",
					},
					Severity: 9,
				},
				{
					Timestamp: time.Date(2025, 4, 4, 15, 17, 57, 802102000, time.UTC),
					Body:      `2025-04-04 15:17:57.802102+00:00 [error] <0.5171.0> {bad_header,<<"PINGAMQP">>}`,
					Attributes: map[string]any{
						"messaging.system": "rabbitmq",
					},
					Severity: 17,
				},
			},
		},
	}

	logger, err := zap.NewDevelopment(zap.IncreaseLevel(zap.InfoLevel))
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	pipeline := pipelineContext{
		hostroot:      string(os.PathSeparator),
		lastFileSizes: make(map[string]int64),
		telemetry: component.TelemetrySettings{
			Logger:         logger,
			TracerProvider: noop.NewTracerProvider(),
			MeterProvider:  noopM.NewMeterProvider(),
			Resource:       pcommon.NewResource(),
		},
		startedComponents: []component.Component{},
		persister:         mustNewPersistHost(t),
	}

	defer func() {
		shutdownAll(pipeline.startedComponents)
	}()

	tmpDir := t.TempDir()
	knownLogFormats := config.DefaultKnownLogFormats()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logFile, err := os.Create(filepath.Join(tmpDir, tc.name+".log"))
			if err != nil {
				t.Fatal("Failed to create log file:", err)
			}

			defer logFile.Close()

			cfg := config.OTLPReceiver{
				Include:   []string{logFile.Name()},
				LogFormat: tc.logFormat,
			}

			logBuf := logBuffer{
				buf: make([]plog.Logs, 0, len(tc.inputLogs)),
			}

			recv, warn, err := newLogReceiver("filelog/"+tc.name, cfg, false, makeBufferConsumer(t, &logBuf), knownLogFormats)
			if err != nil {
				t.Fatal("Failed to initialize log receiver:", err)
			}

			if warn != nil {
				t.Fatal("Got warning during log receiver initialization:", warn)
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			pipeline.l.Lock()
			err = recv.update(ctx, &pipeline, addWarningsFn(t))
			pipeline.l.Unlock()

			if err != nil {
				t.Fatal("Failed to update pipeline:", err)
			}

			time.Sleep(time.Second)

			for _, logLine := range tc.inputLogs {
				_, err = logFile.WriteString(logLine + "\n")
				if err != nil {
					t.Fatal("Failed to write to log file:", err)
				}
			}

			time.Sleep(time.Second)

			// Inject attributes with dynamic values into the expected result
			for i, rec := range tc.expectedRecords {
				if rec.Attributes == nil {
					rec.Attributes = make(map[string]any, 2)
				}

				rec.Attributes[attrs.LogFileName] = filepath.Base(logFile.Name())
				rec.Attributes[attrs.LogFilePath] = logFile.Name()

				tc.expectedRecords[i] = rec
			}

			if diff := cmp.Diff(tc.expectedRecords, logBuf.getAllRecords(), cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("Unexpected log records (-want, +got):\n%s", diff)
			}
		})
	}
}
