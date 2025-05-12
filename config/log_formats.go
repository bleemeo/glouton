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
func renameAttr(from, to string, cond ...string) OTELOperator {
	op := OTELOperator{
		"type": "move",
		"from": "attributes." + from,
		"to":   "attributes['" + to + "']",
		"if":   `"` + from + `" in attributes`,
	}

	if len(cond) == 1 {
		op["if"] = cond[0]
	}

	return op
}

func removeAttr(name string) OTELOperator {
	return OTELOperator{
		"type":  "remove",
		"field": "attributes." + name,
		"if":    `"` + name + `" in attributes`,
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
			"info":  "3xx",
			"debug": []any{
				"2xx",
				// no range alias is defined for 1xx ...
				/*map[string]any{
					"min": 100,
					"max": 199,
				},*/
			},
		},
	}

	nginxAccessParser := []OTELOperator{
		{
			"id":       "nginx_access_parser",
			"type":     "regex_parser",
			"regex":    `^(?P<client_address>\S+) \S+ (?P<user_name>\S+) \[(?P<time>[^\]]+)] "(?P<http_request_method>\S+) \S+ \S+" (?P<http_response_status_code>\d+) (?P<http_response_size>\S+) "(?P<http_connection_state>[^"]*)" "(?P<user_agent_original>[^"]*)"`,
			"severity": severityFromHTTPStatusCode,
		},
		renameAttr("client_address", "client.address"),
		renameAttr("user_name", "user.name"),
		renameAttr("http_request_method", "http.request.method"),
		renameAttr("http_response_status_code", "http.response.status_code"),
		renameAttr("http_response_size", "http.response.size"),
		renameAttr("http_connection_state", "http.connection.state"),
		renameAttr("user_agent_original", "user_agent.original"),
	}
	nginxErrorParser := []OTELOperator{
		{
			"id":    "nginx_error_parser",
			"type":  "regex_parser",
			"regex": `^(?P<time>\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) \[(?P<severity>\w+)] \d+#\d+: [^,]+(, client: (?P<client_address>[\d.]+), server: (?P<server_address>\S+), request: "(?P<http_request_method>\S+) \S+ \S+", host: "(?P<network_local_address>[\w.]+):(?<server_port>\d+)")?`,
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
		removeAttrWhenUndefined("network_local_address"),
		removeAttrWhenUndefined("server_port"),
		renameAttr("client_address", "client.address"),
		renameAttr("server_address", "server.address"),
		renameAttr("http_request_method", "http.request.method"),
		renameAttr("network_local_address", "network.local.address"),
		renameAttr("server_port", "server.port"),
		removeAttr("severity"),
	}

	apacheAccessParser := []OTELOperator{
		{
			"id":       "apache_access_parser",
			"type":     "regex_parser",
			"regex":    `^(?P<client_address>\S+) \S+ (?P<user_name>\S+) \[(?P<time>[^\]]+)] "(((?P<http_request_method>\S+) \S+ \S+)|-)" (?P<http_response_status_code>\d+) ((?P<http_response_size>\d+)|-)`,
			"severity": severityFromHTTPStatusCode,
		},
		removeAttrWhenUndefined("http_request_method"),
		removeAttrWhenUndefined("http_response_size"),
		renameAttr("client_address", "client.address"),
		renameAttr("user_name", "user.name"),
		renameAttr("http_request_method", "http.request.method"),
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
					"trace4": []any{
						"trace1",
						"trace2",
					},
					"trace3": []any{
						"trace3",
						"trace4",
					},
					"trace2": []any{
						"trace5",
						"trace6",
					},
					"trace": []any{
						"trace7",
						"trace8",
					},
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
			"type":  "add",
			"field": "attributes['messaging.system']",
			"value": "kafka",
		},
		{
			"id":    "kafka_parser",
			"type":  "regex_parser",
			"regex": `^\[(?P<time>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2},\d{3})] (?P<severity>\w+)( \[[^\]]*(partition=((?P<messaging_destination_partition_id>\d+)|(?P<messaging_destination_name>[\w-]+)))[^\]]*\])? .+ \([\w.]+\)`,
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
		removeAttrWhenUndefined("messaging_destination_partition_id"),
		removeAttrWhenUndefined("messaging_destination_name"),
		renameAttr("messaging_destination_partition_id", "messaging.destination.partition.id"),
		renameAttr("messaging_destination_name", "messaging.destination.name"),
		removeAttr("severity"),
	}

	redisParser := []OTELOperator{
		{
			"type":  "add",
			"field": "attributes['db.system.name']",
			"value": "redis",
		},
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

	postgresqlParser := []OTELOperator{
		{
			"type":  "add",
			"field": "attributes['db.system.name']",
			"value": "postgresql",
		},
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
					"debug": []any{
						"DEBUG4",
						"DEBUG5",
					},
				},
			},
		},
		removeAttrWhenUndefined("db_query_text"),
		renameAttr("process_pid", "process.pid"),
		renameAttr("db_query_text", "db.query.text"), // FIXME: sanitize or drop
		removeAttr("severity"),
	}

	mysqlParser := []OTELOperator{
		{
			"type":  "add",
			"field": "attributes['db.system.name']",
			"value": "mysql",
		},
		{
			"id":    "mysql_parser",
			"type":  "regex_parser",
			"regex": `^(?<time>\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+\S+) (?<thread_id>\d+) \[(?<severity>\w+)\] \[(?<db_response_status_code>MY-\d+)\] \[[^\]]+\] .+`,
			"severity": map[string]any{
				"parse_from": "attributes.severity",
				// Log level reference can perhaps be found somewhere ...
				// Mapping is OTEL severity -> MySQL priority
				"mapping": map[string]any{
					"error": "Error",
					"warn":  "Warning",
					"info":  "System",
					"debug": "Note",
				},
			},
		},
		renameAttr("thread_id", "thread.id"),
		renameAttr("db_response_status_code", "db.response.status_code"),
		removeAttr("severity"),
	}

	mongodbParser := []OTELOperator{
		{
			"type":  "add",
			"field": "attributes['db.system.name']",
			"value": "mongodb",
		},
		{
			"id":   "mongodb_parser",
			"type": "json_parser",
			"severity": map[string]any{
				"parse_from": "attributes.s",
				// Log level reference can be found at https://www.mongodb.com/docs/manual/reference/log-messages/#std-label-log-severity-levels
				// Mapping is OTEL severity -> MongoDB severity
				"mapping": map[string]any{
					"fatal":  "F",
					"error":  "E",
					"warn":   "W",
					"info":   "I",
					"debug4": "D1",
					"debug3": "D2",
					"debug2": "D3",
					"debug": []any{
						"D4",
						"D5",
						"D", // previous versions
					},
				},
			},
		},
		renameAttr("attr.namespace", "db.collection.name", `"attr" in attributes && "namespace" in attributes["attr"]`),
		removeAttr("s"),
		removeAttr("c"),
		removeAttr("id"),
		removeAttr("ctx"),
		removeAttr("svc"),
		removeAttr("msg"),
		removeAttr("attr"),
		removeAttr("tags"),
		removeAttr("truncated"),
		removeAttr("size"),
	}

	rabbitMQParser := []OTELOperator{
		{
			"type":  "add",
			"field": "attributes['messaging.system']",
			"value": "rabbitmq",
		},
		{
			"id":    "rabbitmq_parser",
			"type":  "regex_parser",
			"regex": `^(?P<time>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+\+\d{2}:\d{2}) \[(?P<severity>\w+)\]\s+<\d+\.\d+\.\d+> .+`,
			"severity": map[string]any{
				"parse_from": "attributes.severity",
				// Log level reference can be found at https://www.rabbitmq.com/docs/logging#log-levels
				// Mapping is OTEL severity -> RabbitMQ level
				"mapping": map[string]any{
					"fatal": "critical",
					"error": "error",
					"warn":  "warning",
					"info":  "info",
					"debug": "debug",
				},
			},
		},
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
					"layout":      "2006-01-02T15:04:05.999999999Z07:00",
					"layout_type": "gotime", // '07:00' as no strptime equivalent
				},
			},
			{
				"type":       "severity_parser",
				"parse_from": "attributes.level",
				// Log level reference can be 'found' at https://pkg.go.dev/log/slog#Level
				// Mapping is OTEL severity -> slog level
				"mapping": map[string]any{
					"error4": "ERROR+3",
					"error3": "ERROR+2",
					"error2": "ERROR+1",
					"error":  "ERROR",
					"warn4":  "WARN+3",
					"warn3":  "WARN+2",
					"warn2":  "WARN+1",
					"warn":   "WARN",
					"info4":  "INFO+3",
					"info3":  "INFO+2",
					"info2":  "INFO+1",
					"info":   "INFO",
					"debug4": "DEBUG+3",
					"debug3": "DEBUG+2",
					"debug2": "DEBUG+1",
					"debug":  "DEBUG",
				},
			},
			removeAttr("time"),
			removeAttr("level"),
			removeAttr("msg"),
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
			removeAttr("time"),
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
			removeAttr("time"),
		),
		"nginx_both": flattenOps(
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
				"output": "nginx_both_end",
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
				"output": "nginx_both_end",
			},
			// End: nginx_error
			OTELOperator{
				"id":   "nginx_both_end",
				"type": "noop",
			},
			removeAttr("time"),
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
			removeAttr("time"),
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
			removeAttr("time"),
		),
		"apache_both": flattenOps(
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
				"output": "apache_both_end",
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
				"output": "apache_both_end",
			},
			// End: apache_error
			OTELOperator{
				"id":   "apache_both_end",
				"type": "noop",
			},
			removeAttr("time"),
		),
		"kafka": flattenOps(
			kafkaParser,
			OTELOperator{
				"type":        "time_parser",
				"parse_from":  "attributes.time",
				"layout":      "%Y-%m-%d %H:%M:%S,%f",
				"layout_type": "strptime",
			},
			removeAttr("time"),
		),
		"kafka_docker": flattenOps(
			kafkaParser, // we'll rely on the timestamp provided by the runtime
			removeAttr("time"),
		),
		"redis": flattenOps(
			redisParser,
			OTELOperator{
				"type":        "time_parser",
				"parse_from":  "attributes.time",
				"layout":      "%d %b %Y %H:%M:%S.%L",
				"layout_type": "strptime",
			},
			removeAttr("time"),
		),
		"redis_docker": flattenOps(
			redisParser, // we'll rely on the timestamp provided by the runtime
			removeAttr("time"),
		),
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
			removeAttr("time"),
			removeAttr("severity"),
		},
		"postgresql": flattenOps(
			postgresqlParser,
			OTELOperator{
				"type":        "time_parser",
				"parse_from":  "attributes.time",
				"layout":      "%Y-%m-%d %H:%M:%S.%L %Z",
				"layout_type": "strptime",
			},
			removeAttr("time"),
		),
		"postgresql_docker": flattenOps(
			postgresqlParser, // we'll rely on the timestamp provided by the runtime
			removeAttr("time"),
		),
		"mysql": flattenOps(
			mysqlParser,
			// FIXME: logs from the [Entrypoint] component have a different timestamp format ...
			OTELOperator{
				"type":        "time_parser",
				"parse_from":  "attributes.time",
				"layout":      "%Y-%m-%dT%H:%M:%S.%f%z",
				"layout_type": "strptime",
			},
			removeAttr("time"),
		),
		"mysql_docker": flattenOps(
			mysqlParser, // we'll rely on the timestamp provided by the runtime
			removeAttr("time"),
		),
		"mongodb": flattenOps(
			mongodbParser,
			OTELOperator{
				"type":        "time_parser",
				"parse_from":  "attributes.t.$date",
				"layout":      "%Y-%m-%dT%H:%M:%S.%L%j",
				"layout_type": "strptime",
			},
			removeAttr("t"),
		),
		"mongodb_docker": flattenOps(
			mongodbParser, // we'll rely on the timestamp provided by the runtime
			removeAttr("t"),
		),
		"rabbitmq": flattenOps(
			rabbitMQParser,
			OTELOperator{
				"type":        "time_parser",
				"parse_from":  "attributes.time",
				"layout":      "%Y-%m-%d %H:%M:%S.%f%j",
				"layout_type": "strptime",
			},
			removeAttr("time"),
		),
		"rabbitmq_docker": flattenOps(
			rabbitMQParser,
			removeAttr("time"),
		),
	}
}
