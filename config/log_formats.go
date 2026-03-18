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

import (
	"fmt"
)

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
			"regex":    `^(?P<client_address>\S+) \S+ (?P<user_name>\S+) \[(?P<time>[^\]]+)] "(?P<http_request_method>\S+) \S+ \S+" (?P<http_response_status_code>\d+) (?P<http_response_size>\S+) "[^"]*" "(?P<user_agent_original>[^"]*)"`,
			"severity": severityFromHTTPStatusCode,
		},
		removeAttr("client_address"),
		removeAttr("user_name"),
		renameAttr("http_request_method", "http.request.method"),
		renameAttr("http_response_status_code", "http.response.status_code"),
		removeAttr("http_response_size"),
		removeAttr("user_agent_original"),
	}
	nginxErrorParser := []OTELOperator{
		{
			"id":    "nginx_error_parser",
			"type":  "regex_parser",
			"regex": `^(?P<time>\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) \[(?P<severity>\w+)] \d+#\d+:(.*?, client: (?P<client_address>[\d.]+))?(.*?, server: (?P<server_address>[^,]+))?(.*?, request: "(?P<http_request_method>[^,]+) [^,]+ [^,]+")?(.*?, host: "[\w.]+(:(?<server_port>\d+))?")?.*$`,
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
		removeAttr("client_address"),
		removeAttrWhenUndefined("server_address"),
		removeAttrWhenUndefined("http_request_method"),
		removeAttrWhenUndefined("server_port"),
		renameAttr("server_address", "server.address"),
		renameAttr("http_request_method", "http.request.method"),
		renameAttr("server_port", "server.port"),
		removeAttr("severity"),
	}

	apacheAccessParser := []OTELOperator{
		{
			"id":       "apache_access_parser",
			"type":     "regex_parser",
			"regex":    `^(?P<client_address>\S+) \S+ (?P<user_name>\S+) \[(?P<time>[^\]]+)] "(((?P<http_request_method>\S+) \S+ \S+)|-)" (?P<http_response_status_code>\d+) ((?P<http_response_size>\d+)|-)( "[^"]*" "(?P<user_agent_original>[^"]*)")?`,
			"severity": severityFromHTTPStatusCode,
		},
		removeAttr("user_agent_original"),
		removeAttrWhenUndefined("http_request_method"),
		removeAttr("http_response_size"),
		removeAttr("client_address"),
		removeAttr("user_name"),
		renameAttr("http_request_method", "http.request.method"),
		renameAttr("http_response_status_code", "http.response.status_code"),
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
		removeAttr("process_pid"),
		removeAttr("thread_id"),
		removeAttr("client_address"),
		removeAttr("client_port"),
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
		removeAttr("process_pid"),
		removeAttr("severity"),
	}

	haproxyParser := []OTELOperator{
		{
			"type": "router",
			"routes": []any{
				map[string]any{
					"expr":   `body startsWith "["`,
					"output": "haproxy_state_parser",
				},
			},
			"default": "haproxy_access_parser",
		},
		// Start: haproxy_state_parser
		{
			"id":    "haproxy_state_parser",
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
		removeAttr("process_pid"),
		removeAttr("severity"),
		{
			"type":   "noop",
			"output": "haproxy_end",
		},
		// End: haproxy_state_parser
		// Start: haproxy_access_parser
		{
			"id":       "haproxy_access_parser",
			"type":     "regex_parser",
			"regex":    `^(?P<client_address>[a-fA-F0-9.:]+):(?<client_port>\d+) \[(?P<time>[^\]]+)].+ (\d+/){4}\d+ (?P<http_response_status_code>\d+) (?P<http_response_size>\d+) .+ (\d+/){4}\d+ \d+/\d+ "(?P<http_request_method>\S+) \S+ \S+"`,
			"severity": severityFromHTTPStatusCode,
		},
		removeAttr("client_address"),
		removeAttr("client_port"),
		renameAttr("http_response_status_code", "http.response.status_code"),
		removeAttr("http_response_size"),
		renameAttr("http_request_method", "http.request.method"),
		{
			"type":   "noop",
			"output": "haproxy_end",
		},
		// End: haproxy_access_parser
		{
			"id":   "haproxy_end",
			"type": "noop",
		},
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
		removeAttr("process_pid"),
		removeAttr("db_query_text"),
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
		removeAttr("thread_id"),
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

	exprSyslogToFacility := `EXPR(
      body["SYSLOG_FACILITY"] == "0" ? "kernel" :
      body["SYSLOG_FACILITY"] == "1" ? "user-level" :
      body["SYSLOG_FACILITY"] == "2" ? "mail" :
      body["SYSLOG_FACILITY"] == "3" ? "system daemons" :
      body["SYSLOG_FACILITY"] == "4" ? "security/authorization" :
      body["SYSLOG_FACILITY"] == "5" ? "syslogd" :
      body["SYSLOG_FACILITY"] == "6" ? "line printer" :
      body["SYSLOG_FACILITY"] == "7" ? "network news" :
      body["SYSLOG_FACILITY"] == "8" ? "uucp" :
      body["SYSLOG_FACILITY"] == "9" ? "clock" :
      body["SYSLOG_FACILITY"] == "10" ? "security/authorization" :
      body["SYSLOG_FACILITY"] == "11" ? "ftp" :
      body["SYSLOG_FACILITY"] == "12" ? "ntp" :
      body["SYSLOG_FACILITY"] == "13" ? "log audit" :
      body["SYSLOG_FACILITY"] == "14" ? "log alert" :
      body["SYSLOG_FACILITY"] == "15" ? "clock" :
      body["SYSLOG_FACILITY"] == "16" ? "local" :
      body["SYSLOG_FACILITY"] == "17" ? "local" :
      body["SYSLOG_FACILITY"] == "18" ? "local" :
      body["SYSLOG_FACILITY"] == "19" ? "local" :
      body["SYSLOG_FACILITY"] == "20" ? "local" :
      body["SYSLOG_FACILITY"] == "21" ? "local" :
      body["SYSLOG_FACILITY"] == "22" ? "local" :
      body["SYSLOG_FACILITY"] == "23" ? "local" :
      "unknown"
    )`

	// Rebuild a "syslog" message from journalctl JSON (it's also what journcalclt show on CLI)
	exprJournaldToSyslogBody := `EXPR(
		timestamp.Format("Jan 02 15:04:05") + " " +
		(body["_HOSTNAME"] ?? "") + " " +
		(attributes["source_program"] ?? "") +
		(body["_PID"] != nil && attributes["source_program"] != nil ? "[" + body["_PID"] + "]" : "") +
		": " + (body["MESSAGE"] ?? "")
	)`

	journaldParser := []OTELOperator{
		{
			"type":        "time_parser",
			"parse_from":  "body._SOURCE_REALTIME_TIMESTAMP",
			"layout":      "us",
			"layout_type": "epoch",
		},
		{
			"type":       "severity_parser",
			"parse_from": "body.PRIORITY",
			// Priority is syslog's level: 0 ("emerg") -> 7 ("debug")
			// Mapping is OTEL severity -> slog level
			"mapping": map[string]any{
				"fatal":  "0", // Emergency
				"error4": "1", // Alert
				"error3": "2", // Critical
				"error":  "3", // Error
				"warn":   "4", // Warning
				"info2":  "5", // Notice
				"info":   "6", // Informational
				"debug":  "7", // Debug
			},
		},
		{
			"type":  "add",
			"field": "attributes.facility",
			"value": exprSyslogToFacility,
		},
		{
			"type":  "remove",
			"field": "attributes.facility",
			"if":    "attributes.facility == 'unknown'",
		},
		{
			"type":  "add",
			"field": "attributes.source_program",
			"value": `EXPR(body["SYSLOG_IDENTIFIER"] ?? body["_COMM"] ?? "")`,
		},
		{
			"type":  "remove",
			"field": "attributes.source_program",
			"if":    "attributes.source_program == ''",
		},
		{
			"type":     "add",
			"field":    "body",
			"value":    exprJournaldToSyslogBody,
			"on_error": "send",
		},
		// fallback if above expresion failed
		{
			"type": "move",
			"from": "body.MESSAGE",
			"to":   "body",
			"if":   `type(body) == "map" && "MESSAGE" in body`,
		},
	}

	syslogParser := []OTELOperator{
		{
			// Parse syslog when time use "2026-03-10T14:23:09.079180+00:00" format (e.g. Ubuntu 24.04 and later)
			"type":  "regex_parser",
			"regex": `^(?P<time>[^ ]+) (?P<hostname>[^ ]+) (?P<source_program>[^ \[]+)(\[\d+\])?: .*$`,
			"timestamp": map[string]any{
				"parse_from":  "attributes.time",
				"layout":      "%Y-%m-%dT%H:%M:%S.%L%z",
				"layout_type": "strptime",
			},
			"if": `body matches '^\\d{4}-'`,
		},
		{
			// Parse syslog when time use "Mar 10 14:39:12" format (e.g. Ubuntu 22.04)
			"type":  "regex_parser",
			"regex": `^(?P<time>[A-Za-z]{3} [0-9: ]+) (?P<hostname>[^ ]+) (?P<source_program>[^ \[]+)(\[\d+\])?: .*$`,
			"timestamp": map[string]any{
				"parse_from":  "attributes.time",
				"layout":      "%b %d %H:%M:%S",
				"layout_type": "strptime",
			},
			"if": `body matches '^[A-Za-z]{3} '`,
		},
		removeAttr("time"),
		removeAttr("hostname"),
	}

	auditdParser := []OTELOperator{
		{
			"type":  "regex_parser",
			"regex": `^type=(?P<type>[^ ]+) msg=audit\((?P<timestamp>[0-9.]+):[0-9]+\).*$`,
			"timestamp": map[string]any{
				"parse_from":  "attributes.timestamp",
				"layout":      "s.ms",
				"layout_type": "epoch",
			},
		},
		removeAttr("timestamp"),
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
		"valkey": flattenOps(
			redisParser,
			OTELOperator{
				"type":  "add",
				"field": "attributes['db.system.name']",
				"value": "valkey",
			},
			OTELOperator{
				"type":        "time_parser",
				"parse_from":  "attributes.time",
				"layout":      "%d %b %Y %H:%M:%S.%L",
				"layout_type": "strptime",
			},
		),
		"valkey_docker": flattenOps(
			redisParser, // we'll rely on the timestamp provided by the runtime
			OTELOperator{
				"type":  "add",
				"field": "attributes['db.system.name']",
				"value": "valkey",
			},
			removeAttr("time"),
		),
		"haproxy": flattenOps(
			haproxyParser,
			OTELOperator{
				"type":        "time_parser",
				"parse_from":  "attributes.time",
				"layout":      "%d/%b/%Y:%H:%M:%S.%L",
				"layout_type": "strptime",
				"if":          `"time" in attributes`, // time is not provided by the haproxy_state_parser
			},
			removeAttr("time"),
		),
		"haproxy_docker": flattenOps(
			haproxyParser, // we'll rely on the timestamp provided by the runtime
			removeAttr("time"),
		),
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
		"journald": journaldParser,
		"auditd":   auditdParser,
		"syslog":   syslogParser,
		"syslogAuth": flattenOps(
			syslogParser,
			OTELOperator{
				"type":  "add",
				"field": "attributes.facility",
				"value": "security/authorization",
			},
		),
	}
}
