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

package config

import "fmt"

// flattenOps returns a slice containing all the given operators,
// unpacking slices if needed to end up with a flat list.
// Given values must be either of type OTELOperator or []OTELOperator.
func flattenOps(ops ...any) []OTELOperator {
	var flattened []OTELOperator

	for _, value := range ops {
		switch v := value.(type) {
		case OTELOperator:
			flattened = append(flattened, v)
		case []OTELOperator:
			flattened = append(flattened, v...)
		default:
			panic(fmt.Sprintf("can't flatten type %T: must be OTELOperator or []OTELOperator", value))
		}
	}

	return flattened
}

// renameAttr returns an operator that renames the given attribute field to the desired name.
// Its main purpose is to allow dots (.) in attribute names that come from regexp named groups.
// If the source attribute doesn't exist, the operator is a no-op.
func renameAttr(from, to string) OTELOperator {
	return OTELOperator{
		"type": "move",
		"from": "attributes." + from,
		"to":   "attributes['" + to + "']",
		"if":   `"` + from + `" in attributes`,
	}
}

func removeAttr(name string) OTELOperator { //nolint:unparam
	return OTELOperator{
		"type":  "remove",
		"field": "attributes." + name,
	}
}

func removeAttrWhenUndefined(name string) OTELOperator {
	return OTELOperator{
		"type":  "remove",
		"field": "attributes." + name,
		"if":    `get(attributes, "` + name + `") in [nil, ""]`,
	}
}

func DefaultKnownLogFormats() map[string][]OTELOperator { //nolint:maintidx
	severityFromHTTPStatusCode := map[string]any{
		"parse_from": "attributes.http_response_status_code",
		"mapping": map[string]any{
			"error": "5xx",
			"warn":  "4xx",
			"info":  "3xx", // FIXME: debug level too ?
			"debug": "2xx",
		},
	}

	nginxAccessParser := []OTELOperator{
		{
			"id":       "nginx_access_parser",
			"type":     "regex_parser",
			"regex":    `^(?P<client_address>\S+) \S+ (?P<user_name>\S+) \[(?P<time>[^\]]+)] "(?P<http_request_method>\S+) (?P<http_route>\S+) \S+" (?P<http_response_status_code>\d+) (?P<http_response_size>\S+) "(?P<http_connection_state>[^"]*)" "(?P<user_agent_original>[^"]*)"`,
			"severity": severityFromHTTPStatusCode,
		},
		renameAttr("client_address", "client.address"),
		renameAttr("user_name", "user.name"),
		renameAttr("http_request_method", "http.request.method"),
		renameAttr("http_route", "http.route"),
		renameAttr("http_response_status_code", "http.response.status_code"),
		renameAttr("http_response_size", "http.response.size"),
		renameAttr("http_connection_state", "http.connection.state"),
		renameAttr("user_agent_original", "user_agent.original"),
	}
	nginxErrorParser := []OTELOperator{
		{
			"id":    "nginx_error_parser",
			"type":  "regex_parser",
			"regex": `^(?P<time>\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) \[(?P<severity>\w+)] \d+#\d+: [^,]+(, client: (?P<client_address>[\d.]+), server: (?P<server_address>\S+), request: "(?P<http_request_method>\S+) (?P<http_route>\S+) \S+", host: "(?P<network_local_address>[\w.]+):(?<server_port>\d+)")?`,
			"severity": map[string]any{
				"parse_from": "attributes.severity",
				// Log level reference can be found at https://nginx.org/en/docs/ngx_core_module.html#error_log
				// Mapping is OTEL severity -> Nginx level
				"mapping": map[string]any{
					"fatal":  "emerg",
					"error3": "alert",
					"error2": "crit",
					"error":  "error",
					"warn":   "warn",
					"info2":  "notice",
					"info":   "info",
					"debug":  "debug",
				},
			},
		},
		removeAttrWhenUndefined("client_address"),
		removeAttrWhenUndefined("server_address"),
		removeAttrWhenUndefined("http_request_method"),
		removeAttrWhenUndefined("http_route"),
		removeAttrWhenUndefined("network_local_address"),
		removeAttrWhenUndefined("server_port"),
		renameAttr("client_address", "client.address"),
		renameAttr("server_address", "server.address"),
		renameAttr("http_request_method", "http.request.method"),
		renameAttr("http_route", "http.route"),
		renameAttr("network_local_address", "network.local.address"),
		renameAttr("server_port", "server.port"),
		removeAttr("severity"),
	}

	apacheAccessParser := []OTELOperator{
		{
			"id":       "apache_access_parser",
			"type":     "regex_parser",
			"regex":    `^(?P<client_address>\S+) \S+ (?P<user_name>\S+) \[(?P<time>[^\]]+)] "(((?P<http_request_method>\S+) (?P<http_route>\S+) \S+)|-)" (?P<http_response_status_code>\d+) ((?P<http_response_size>\d+)|-)`,
			"severity": severityFromHTTPStatusCode,
		},
		removeAttrWhenUndefined("http_request_method"),
		removeAttrWhenUndefined("http_route"),
		removeAttrWhenUndefined("http_response_size"),
		renameAttr("client_address", "client.address"),
		renameAttr("user_name", "user.name"),
		renameAttr("http_request_method", "http.request.method"),
		renameAttr("http_route", "http.route"),
		renameAttr("http_response_status_code", "http.response.status_code"),
		renameAttr("http_response_size", "http.response.size"),
	}
	apacheErrorParser := []OTELOperator{
		{
			"id":    "apache_error_parser",
			"type":  "regex_parser",
			"regex": `^\[(?P<time>[\w :/.]+)\] \[\w+:(?P<severity>\w+)]( \[pid (?P<process_pid>\d+):tid (?P<thread_id>\d+)\])?( \[client (?P<client_address>[\d.]+)(:(?<client_port>\d+))?\])? .*`,
			"severity": map[string]any{
				"parse_from": "attributes.severity",
				// Log level reference can be found at https://httpd.apache.org/docs/current/mod/core.html#loglevel
				// Mapping is OTEL severity -> Apache level
				"mapping": map[string]any{
					"fatal":  "emerg",
					"error3": "alert",
					"error2": "crit",
					"error":  "error",
					"warn":   "warn",
					"info2":  "notice",
					"info":   "info",
					"debug":  "debug",
					"trace4": "trace1",
					"trace3": "trace2",
					"trace2": "trace3",
					"trace":  "trace4",
					// ignoring Apache trace levels 5 to 8
				},
			},
		},
		removeAttrWhenUndefined("process_pid"),
		removeAttrWhenUndefined("thread_id"),
		removeAttrWhenUndefined("client_address"),
		removeAttrWhenUndefined("client_port"),
		renameAttr("process_pid", "process.pid"),
		renameAttr("thread_id", "thread.id"),
		renameAttr("client_address", "client.address"),
		renameAttr("client_port", "client.port"),
		removeAttr("severity"),
	}

	kafkaParser := []OTELOperator{
		{
			"id":    "kafka_parser",
			"type":  "regex_parser",
			"regex": `^\[(?P<time>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2},\d{3})] (?P<severity>\w+)( \[[^\]]*(partition=(?P<message_destination_partition_id>[\w-]+))[^\]]*\])? .+ \([\w.]+\)`,
			"severity": map[string]any{
				"parse_from": "attributes.severity",
				// Log level reference can be found at https://logging.apache.org/log4j/2.x/javadoc/log4j-api/org/apache/logging/log4j/Level.html
				// Mapping is OTEL severity -> Log4j level
				"mapping": map[string]any{
					"emerg": "FATAL",
					"error": "ERROR",
					"warn":  "WARN",
					"info":  "INFO",
					"debug": "DEBUG",
					"trace": "TRACE",
				},
			},
		},
		removeAttrWhenUndefined("message_destination_partition_id"),
		renameAttr("message_destination_partition_id", "messaging.destination.partition.id"),
		removeAttr("severity"),
	}

	redisParser := []OTELOperator{
		{
			"id":    "redis_parser",
			"type":  "regex_parser",
			"regex": `^(?P<process_pid>\d+):[A-Z] (?<time>\d{2} \w{3} \d{4} \d{2}:\d{2}:\d{2}\.\d{3}) (?P<severity>[.\-*#]) .+`,
			"severity": map[string]any{
				"parse_from": "attributes.severity",
				// Log level 'reference' can be 'found' at https://github.com/redis/redis/blob/aa8e2d171232218364857ee8528f1af092b9e5b7/src/server.h#L554
				// Mapping is OTEL severity -> Redis level
				"mapping": map[string]any{
					"warn":   "#",
					"info":   "*",
					"debug":  "-",
					"debug2": ".",
				},
			},
		},
		renameAttr("process_pid", "process.pid"),
		removeAttr("severity"),
	}

	postgreSQLParser := []OTELOperator{
		{
			"id":    "postgresql_parser",
			"type":  "regex_parser",
			"regex": `^(?<time>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+ \w+) \[(?<process_pid>\d+)\] (?<severity>[A-Z]+):\s+(.+statement: (?<db_query_text>.+)|.+)`,
			"severity": map[string]any{
				"parse_from": "attributes.severity",
				// Log level reference can be found at https://www.postgresql.org/docs/current/runtime-config-logging.html#RUNTIME-CONFIG-SEVERITY-LEVELS
				// Mapping is OTEL severity -> PostgreSQL level
				"mapping": map[string]any{
					"fatal":  "PANIC",
					"error":  "FATAL",
					"warn":   "WARNING",
					"info2":  "NOTICE",
					"info":   "INFO",
					"debug4": "DEBUG",
					"debug3": "DEBUG2",
					"debug2": "DEBUG3",
					"debug":  "DEBUG4",
				},
			},
		},
		removeAttrWhenUndefined("db_query_text"),
		renameAttr("process_pid", "process.pid"),
		renameAttr("db_query_text", "db.query.text"), // FIXME: sanitize or drop ?
		removeAttr("severity"),
	}

	return map[string][]OTELOperator{
		"json": {
			{
				"type": "json_parser",
			},
		},
		"json_golang_slog": {
			{
				"type": "json_parser",
				"timestamp": map[string]any{
					"parse_from":  "attributes.time",
					"layout":      "%Y-%m-%dT%H:%M:%S.%L%z",
					"layout_type": "strptime",
				},
			},
		},
		"nginx_access": flattenOps(
			OTELOperator{
				"id":    "nginx_access",
				"type":  "add",
				"field": "attributes['log.iostream']",
				"value": "stdout",
			},
			nginxAccessParser,
			OTELOperator{
				"type":        "time_parser",
				"parse_from":  "attributes.time",
				"layout":      "%d/%b/%Y:%H:%M:%S %z",
				"layout_type": "strptime",
			},
		),
		"nginx_error": flattenOps(
			OTELOperator{
				"id":    "nginx_error",
				"type":  "add",
				"field": "attributes['log.iostream']",
				"value": "stderr",
			},
			nginxErrorParser,
			OTELOperator{
				"type":        "time_parser",
				"parse_from":  "attributes.time",
				"layout":      "%Y/%m/%d %H:%M:%S",
				"layout_type": "strptime",
			},
		),
		"nginx_combined": flattenOps(
			OTELOperator{
				"type": "router",
				"routes": []any{
					map[string]any{
						"expr":   `body matches "^(\\d{1,3}\\.){3}\\d{1,3}\\s-\\s(-|[\\w-]+)\\s"`, // <- not regexp, but expr-lang
						"output": "nginx_access",
					},
					map[string]any{
						"expr":   `body matches "^\\d{4}\\/\\d{2}\\/\\d{2}\\s\\d{2}:\\d{2}:\\d{2}\\s\\[error]"`, // <- not regexp, but expr-lang
						"output": "nginx_error",
					},
				},
			},
			// Start: nginx_access
			OTELOperator{
				"id":    "nginx_access",
				"type":  "add",
				"field": "attributes['log.iostream']",
				"value": "stdout",
			},
			nginxAccessParser,
			OTELOperator{
				"type":   "noop",
				"output": "nginx_combined_end",
			},
			// End: nginx_access
			// Start: nginx_error
			OTELOperator{
				"id":    "nginx_error",
				"type":  "add",
				"field": "attributes['log.iostream']",
				"value": "stderr",
			},
			nginxErrorParser,
			OTELOperator{
				"type":   "noop",
				"output": "nginx_combined_end",
			},
			// End: nginx_error
			OTELOperator{
				"id":   "nginx_combined_end",
				"type": "noop",
			},
		),
		"apache_access": flattenOps(
			OTELOperator{
				"id":    "apache_access",
				"type":  "add",
				"field": "attributes['log.iostream']",
				"value": "stdout",
			},
			apacheAccessParser,
			OTELOperator{
				"type":        "time_parser",
				"parse_from":  "attributes.time",
				"layout":      "%d/%b/%Y:%H:%M:%S %z",
				"layout_type": "strptime",
			},
		),
		"apache_error": flattenOps(
			OTELOperator{
				"id":    "apache_error",
				"type":  "add",
				"field": "attributes['log.iostream']",
				"value": "stderr",
			},
			apacheErrorParser,
			OTELOperator{
				"type":        "time_parser",
				"parse_from":  "attributes.time",
				"layout":      "%a %b %d %H:%M:%S %Y",
				"layout_type": "strptime",
			},
		),
		"apache_combined": flattenOps(
			OTELOperator{
				"type": "router",
				"routes": []any{
					map[string]any{
						"expr":   `body matches "^(\\d{1,3}\\.){3}\\d{1,3}\\s\\S+\\s\\S+\\s\\[[^\\]]+\\]\\s"`, // <- not regexp, but expr-lang
						"output": "apache_access",
					},
					map[string]any{
						"expr":   `body matches "^\\[[\\w :.]+\\]\\s(\\[[^\\]]+\\])+\\s"`, // <- not regexp, but expr-lang
						"output": "apache_error",
					},
				},
			},
			// Start: apache_access
			OTELOperator{
				"id":    "apache_access",
				"type":  "add",
				"field": "attributes['log.iostream']",
				"value": "stdout",
			},
			apacheAccessParser,
			OTELOperator{
				"type":   "noop",
				"output": "apache_combined_end",
			},
			// End: apache_access
			// Start: apache_error
			OTELOperator{
				"id":    "apache_error",
				"type":  "add",
				"field": "attributes['log.iostream']",
				"value": "stderr",
			},
			apacheErrorParser,
			OTELOperator{
				"type":   "noop",
				"output": "apache_combined_end",
			},
			// End: apache_error
			OTELOperator{
				"id":   "apache_combined_end",
				"type": "noop",
			},
		),
		"kafka": flattenOps(
			kafkaParser,
			OTELOperator{
				"type":        "time_parser",
				"parse_from":  "attributes.time",
				"layout":      "%Y-%m-%d %H:%M:%S,%f",
				"layout_type": "strptime",
			},
		),
		"kafka_docker": kafkaParser, // we'll rely on the timestamp provided by the runtime
		"redis": flattenOps(
			redisParser,
			OTELOperator{
				"type":        "time_parser",
				"parse_from":  "attributes.time",
				"layout":      "%d %b %Y %H:%M:%S.%L",
				"layout_type": "strptime",
			},
		),
		"redis_docker": redisParser, // we'll rely on the timestamp provided by the runtime
		"haproxy": {
			{
				"id":    "haproxy_parser",
				"type":  "regex_parser",
				"regex": `^\[(?P<severity>[A-Z]+)\]\s+\((?<process_pid>\d+)\)\s*:\s*.+`,
				"severity": map[string]any{
					"parse_from": "attributes.severity",
					// Log level reference can be found at https://docs.haproxy.org/3.0/configuration.html#4.2-log
					// Mapping is OTEL severity -> HAProxy level
					"mapping": map[string]any{
						"fatal":  "EMERG",
						"error3": "ALERT",
						"error2": "CRIT",
						"error":  "ERR",
						"warn":   "WARNING",
						"info2":  "NOTICE",
						"info":   "INFO",
						"debug":  "DEBUG",
					},
				},
			},
			renameAttr("process_pid", "process.pid"),
			removeAttr("severity"),
		},
		"postgresql": postgreSQLParser,
		"postgresql_docker": flattenOps(
			postgreSQLParser,
			OTELOperator{
				"type":        "time_parser",
				"parse_from":  "attributes.time",
				"layout":      "%Y-%m-%d %H:%M:%S.%L %Z",
				"layout_type": "strptime",
			},
		),
	}
}
