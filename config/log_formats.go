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
	"fmt"
)

// Constants for OTEL operator map keys used extensively in log format definitions.
const (
	opType       = "type"
	opField      = "field"
	opValue      = "value"
	opAdd        = "add"
	opRemove     = "remove"
	opMove       = "move"
	opParseFrom  = "parse_from"
	opMapping    = "mapping"
	opSeverity   = "severity"
	opLayout     = "layout"
	opLayoutType = "layout_type"
	opOutput     = "output"
	opNoop       = "noop"
	opRouter     = "router"
	opRoutes     = "routes"
	opExpr       = "expr"
	opIf         = "if"
	opTimestamp  = "timestamp"

	// Parser type constants.
	parserRegex = "regex_parser"
	parserJSON  = "json_parser"
	parserTime  = "time_parser"

	// Regex operator key.
	opRegex = "regex"

	// Common attribute paths.
	attrDBSystemName = "attributes['db.system.name']"
	attrFacility     = "attributes.facility"

	// String values used in severity mappings that differ from severity-level keys.
	mapERROR = "ERROR"
	mapDEBUG = "DEBUG"

	// Severity level constants for OTEL severity mapping.
	sevError  = "error"
	sevError2 = "error2"
	sevError3 = "error3"
	sevWarn   = "warn"
	sevInfo   = "info"
	sevInfo2  = "info2"
	sevDebug  = "debug"
	sevDebug2 = "debug2"
	sevDebug3 = "debug3"
	sevDebug4 = "debug4"
	sevFatal  = "fatal"
	sevEmerg  = "emerg"

	// Common attribute paths.
	attrSeverity  = "attributes.severity"
	attrTime      = "attributes.time"
	attrLogStream = "attributes['log.iostream']"

	// Layout type constants.
	layoutStrptime = "strptime"

	// Stream constants.
	streamStdout = "stdout"
	streamStderr = "stderr"

	// Service name constants used as db.system.name or map keys.
	svcRedis      = "redis"
	svcPostgresql = "postgresql"
	svcMysql      = "mysql"
	svcValkey     = "valkey"

	// Router/noop target ID constants used in composite log formats.
	idHaproxyEnd    = "haproxy_end"
	idNginxAccess   = "nginx_access"
	idNginxError    = "nginx_error"
	idNginxBothEnd  = "nginx_both_end"
	idApacheAccess  = "apache_access"
	idApacheError   = "apache_error"
	idApacheBothEnd = "apache_both_end"
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
		opType: opMove,
		"from": "attributes." + from,
		"to":   "attributes['" + to + "']",
		opIf:   `"` + from + `" in attributes`,
	}

	if len(cond) == 1 {
		op[opIf] = cond[0]
	}

	return op
}

func removeAttr(name string) OTELOperator {
	return OTELOperator{
		opType:  opRemove,
		opField: "attributes." + name,
		opIf:    `"` + name + `" in attributes`,
	}
}

func removeAttrWhenUndefined(name string) OTELOperator {
	return OTELOperator{
		opType:  opRemove,
		opField: "attributes." + name,
		opIf:    `get(attributes, "` + name + `") in [nil, ""]`,
	}
}

func DefaultKnownLogFormats() map[string][]OTELOperator { //nolint:maintidx
	severityFromHTTPStatusCode := map[string]any{
		opParseFrom: "attributes.http_response_status_code",
		opMapping: map[string]any{
			sevError: "5xx",
			sevWarn:  "4xx",
			sevInfo:  "3xx",
			sevDebug: []any{
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
			opType:     parserRegex,
			opRegex:    `^(?P<client_address>\S+) \S+ (?P<user_name>\S+) \[(?P<time>[^\]]+)] "(?P<http_request_method>\S+) \S+ \S+" (?P<http_response_status_code>\d+) (?P<http_response_size>\S+) "[^"]*" "(?P<user_agent_original>[^"]*)"`,
			opSeverity: severityFromHTTPStatusCode,
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
			opType:  parserRegex,
			opRegex: `^(?P<time>\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) \[(?P<severity>\w+)] \d+#\d+:(.*?, client: (?P<client_address>[\d.]+))?(.*?, server: (?P<server_address>[^,]+))?(.*?, request: "(?P<http_request_method>[^,]+) [^,]+ [^,]+")?(.*?, host: "[\w.]+(:(?<server_port>\d+))?")?.*$`,
			opSeverity: map[string]any{
				opParseFrom: attrSeverity,
				// Log level reference can be found at https://nginx.org/en/docs/ngx_core_module.html#error_log
				// Mapping is OTEL severity -> Nginx level
				opMapping: map[string]any{
					sevFatal:  sevEmerg,
					sevError3: "alert",
					sevError2: "crit",
					sevError:  sevError,
					sevWarn:   sevWarn,
					sevInfo2:  "notice",
					sevInfo:   sevInfo,
					sevDebug:  sevDebug,
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
			opType:     parserRegex,
			opRegex:    `^(?P<client_address>\S+) \S+ (?P<user_name>\S+) \[(?P<time>[^\]]+)] "(((?P<http_request_method>\S+) \S+ \S+)|-)" (?P<http_response_status_code>\d+) ((?P<http_response_size>\d+)|-)( "[^"]*" "(?P<user_agent_original>[^"]*)")?`,
			opSeverity: severityFromHTTPStatusCode,
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
			opType:  parserRegex,
			opRegex: `^\[(?P<time>[\w :/.]+)\] \[\w+:(?P<severity>\w+)]( \[pid (?P<process_pid>\d+):tid (?P<thread_id>\d+)\])?( \[client (?P<client_address>[\d.]+)(:(?<client_port>\d+))?\])? .*`,
			opSeverity: map[string]any{
				opParseFrom: attrSeverity,
				// Log level reference can be found at https://httpd.apache.org/docs/current/mod/core.html#loglevel
				// Mapping is OTEL severity -> Apache level
				opMapping: map[string]any{
					sevFatal:  sevEmerg,
					sevError3: "alert",
					sevError2: "crit",
					sevError:  sevError,
					sevWarn:   sevWarn,
					sevInfo2:  "notice",
					sevInfo:   sevInfo,
					sevDebug:  sevDebug,
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
			opType:  opAdd,
			opField: "attributes['messaging.system']",
			opValue: "kafka",
		},
		{
			"id":    "kafka_parser",
			opType:  parserRegex,
			opRegex: `^\[(?P<time>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2},\d{3})] (?P<severity>\w+)( \[[^\]]*(partition=((?P<messaging_destination_partition_id>\d+)|(?P<messaging_destination_name>[\w-]+)))[^\]]*\])? .+ \([\w.]+\)`,
			opSeverity: map[string]any{
				opParseFrom: attrSeverity,
				// Log level reference can be found at https://logging.apache.org/log4j/2.x/javadoc/log4j-api/org/apache/logging/log4j/Level.html
				// Mapping is OTEL severity -> Log4j level
				opMapping: map[string]any{
					sevEmerg: "FATAL",
					sevError: mapERROR,
					sevWarn:  "WARN",
					sevInfo:  DefaultLogLevel,
					sevDebug: mapDEBUG,
					"trace":  "TRACE",
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
			opType:  opAdd,
			opField: attrDBSystemName,
			opValue: svcRedis,
		},
		{
			"id":    "redis_parser",
			opType:  parserRegex,
			opRegex: `^(?P<process_pid>\d+):[A-Z] (?<time>\d{2} \w{3} \d{4} \d{2}:\d{2}:\d{2}\.\d{3}) (?P<severity>[.\-*#]) .+`,
			opSeverity: map[string]any{
				opParseFrom: attrSeverity,
				// Log level 'reference' can be 'found' at https://github.com/redis/redis/blob/aa8e2d171232218364857ee8528f1af092b9e5b7/src/server.h#L554
				// Mapping is OTEL severity -> Redis level
				opMapping: map[string]any{
					sevWarn:   "#",
					sevInfo:   "*",
					sevDebug:  "-",
					sevDebug2: ".",
				},
			},
		},
		removeAttr("process_pid"),
		removeAttr("severity"),
	}

	haproxyParser := []OTELOperator{
		{
			opType: opRouter,
			opRoutes: []any{
				map[string]any{
					opExpr:   `body startsWith "["`,
					opOutput: "haproxy_state_parser",
				},
			},
			"default": "haproxy_access_parser",
		},
		// Start: haproxy_state_parser
		{
			"id":    "haproxy_state_parser",
			opType:  parserRegex,
			opRegex: `^\[(?P<severity>[A-Z]+)\]\s+\((?<process_pid>\d+)\)\s*:\s*.+`,
			opSeverity: map[string]any{
				opParseFrom: attrSeverity,
				// Log level reference can be found at https://docs.haproxy.org/3.0/configuration.html#4.2-log
				// Mapping is OTEL severity -> HAProxy level
				opMapping: map[string]any{
					sevFatal:  "EMERG",
					sevError3: "ALERT",
					sevError2: "CRIT",
					sevError:  "ERR",
					sevWarn:   "WARNING",
					sevInfo2:  "NOTICE",
					sevInfo:   DefaultLogLevel,
					sevDebug:  mapDEBUG,
				},
			},
		},
		removeAttr("process_pid"),
		removeAttr("severity"),
		{
			opType:   opNoop,
			opOutput: idHaproxyEnd,
		},
		// End: haproxy_state_parser
		// Start: haproxy_access_parser
		{
			"id":       "haproxy_access_parser",
			opType:     parserRegex,
			opRegex:    `^(?P<client_address>[a-fA-F0-9.:]+):(?<client_port>\d+) \[(?P<time>[^\]]+)].+ (\d+/){4}\d+ (?P<http_response_status_code>\d+) (?P<http_response_size>\d+) .+ (\d+/){4}\d+ \d+/\d+ "(?P<http_request_method>\S+) \S+ \S+"`,
			opSeverity: severityFromHTTPStatusCode,
		},
		removeAttr("client_address"),
		removeAttr("client_port"),
		renameAttr("http_response_status_code", "http.response.status_code"),
		removeAttr("http_response_size"),
		renameAttr("http_request_method", "http.request.method"),
		{
			opType:   opNoop,
			opOutput: idHaproxyEnd,
		},
		// End: haproxy_access_parser
		{
			"id":   idHaproxyEnd,
			opType: opNoop,
		},
	}

	postgresqlParser := []OTELOperator{
		{
			opType:  opAdd,
			opField: attrDBSystemName,
			opValue: svcPostgresql,
		},
		{
			"id":    "postgresql_parser",
			opType:  parserRegex,
			opRegex: `^(?<time>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+ \w+) \[(?<process_pid>\d+)\] (?<severity>[A-Z]+):\s+(.+statement: (?<db_query_text>.+)|.+)`,
			opSeverity: map[string]any{
				opParseFrom: attrSeverity,
				// Log level reference can be found at https://www.postgresql.org/docs/current/runtime-config-logging.html#RUNTIME-CONFIG-SEVERITY-LEVELS
				// Mapping is OTEL severity -> PostgreSQL level
				opMapping: map[string]any{
					sevFatal:  "PANIC",
					sevError:  "FATAL",
					sevWarn:   "WARNING",
					sevInfo2:  "NOTICE",
					sevInfo:   DefaultLogLevel,
					sevDebug4: mapDEBUG,
					sevDebug3: "DEBUG2",
					sevDebug2: "DEBUG3",
					sevDebug: []any{
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

	mariaDBParser := []OTELOperator{
		{
			opType:  opAdd,
			opField: attrDBSystemName,
			opValue: "mariadb",
		},
		{
			"id":    "mariadb_parser",
			opType:  parserRegex,
			opRegex: `^(?<time>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) (?<thread_id>\d+) \[(?<severity>\w+)\].+`,
			opSeverity: map[string]any{
				opParseFrom: attrSeverity,
				// Log level reference can perhaps be found somewhere ...
				// Mapping is OTEL severity -> MySQL priority
				opMapping: map[string]any{
					sevError: "Error",
					sevWarn:  "Warning",
					sevDebug: "Note",
				},
			},
		},
		removeAttr("thread_id"),
		removeAttr("severity"),
	}

	mysqlParser := []OTELOperator{
		{
			opType:  opAdd,
			opField: attrDBSystemName,
			opValue: svcMysql,
		},
		{
			"id":    "mysql_parser",
			opType:  parserRegex,
			opRegex: `^(?<time>\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+\S+) (?<thread_id>\d+) \[(?<severity>\w+)\] \[(?<db_response_status_code>MY-\d+)\] \[[^\]]+\] .+`,
			opSeverity: map[string]any{
				opParseFrom: attrSeverity,
				// Log level reference can perhaps be found somewhere ...
				// Mapping is OTEL severity -> MySQL priority
				opMapping: map[string]any{
					sevError: "Error",
					sevWarn:  "Warning",
					sevInfo:  "System",
					sevDebug: "Note",
				},
			},
		},
		removeAttr("thread_id"),
		renameAttr("db_response_status_code", "db.response.status_code"),
		removeAttr("severity"),
	}

	mongodbParser := []OTELOperator{
		{
			opType:  opAdd,
			opField: attrDBSystemName,
			opValue: "mongodb",
		},
		{
			"id":   "mongodb_parser",
			opType: parserJSON,
			opSeverity: map[string]any{
				opParseFrom: "attributes.s",
				// Log level reference can be found at https://www.mongodb.com/docs/manual/reference/log-messages/#std-label-log-severity-levels
				// Mapping is OTEL severity -> MongoDB severity
				opMapping: map[string]any{
					sevFatal:  "F",
					sevError:  "E",
					sevWarn:   "W",
					sevInfo:   "I",
					sevDebug4: "D1",
					sevDebug3: "D2",
					sevDebug2: "D3",
					sevDebug: []any{
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
			opType:  opAdd,
			opField: "attributes['messaging.system']",
			opValue: "rabbitmq",
		},
		{
			"id":    "rabbitmq_parser",
			opType:  parserRegex,
			opRegex: `^(?P<time>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+\+\d{2}:\d{2}) \[(?P<severity>\w+)\]\s+<\d+\.\d+\.\d+> .+`,
			opSeverity: map[string]any{
				opParseFrom: attrSeverity,
				// Log level reference can be found at https://www.rabbitmq.com/docs/logging#log-levels
				// Mapping is OTEL severity -> RabbitMQ level
				opMapping: map[string]any{
					sevFatal: "critical",
					sevError: sevError,
					sevWarn:  "warning",
					sevInfo:  sevInfo,
					sevDebug: sevDebug,
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
			opType:       parserTime,
			opParseFrom:  "body._SOURCE_REALTIME_TIMESTAMP",
			opLayout:     "us",
			opLayoutType: "epoch",
		},
		{
			opType:      "severity_parser",
			opParseFrom: "body.PRIORITY",
			// Priority is syslog's level: 0 ("emerg") -> 7 ("debug")
			// Mapping is OTEL severity -> slog level
			opMapping: map[string]any{
				sevFatal:  "0", // Emergency
				"error4":  "1", // Alert
				sevError3: "2", // Critical
				sevError:  "3", // Error
				sevWarn:   "4", // Warning
				sevInfo2:  "5", // Notice
				sevInfo:   "6", // Informational
				sevDebug:  "7", // Debug
			},
		},
		{
			opType:  opAdd,
			opField: attrFacility,
			opValue: exprSyslogToFacility,
		},
		{
			opType:  opRemove,
			opField: attrFacility,
			opIf:    "attributes.facility == 'unknown'",
		},
		{
			opType:  opAdd,
			opField: "attributes.source_program",
			opValue: `EXPR(body["SYSLOG_IDENTIFIER"] ?? body["_COMM"] ?? "")`,
		},
		{
			opType:  opRemove,
			opField: "attributes.source_program",
			opIf:    "attributes.source_program == ''",
		},
		{
			opType:     opAdd,
			opField:    "body",
			opValue:    exprJournaldToSyslogBody,
			"on_error": "send",
		},
		// fallback if above expresion failed
		{
			opType: opMove,
			"from": "body.MESSAGE",
			"to":   "body",
			opIf:   `type(body) == "map" && "MESSAGE" in body`,
		},
	}

	syslogParser := []OTELOperator{
		{
			// Parse syslog when time use "2026-03-10T14:23:09.079180+00:00" format (e.g. Ubuntu 24.04 and later)
			opType:  parserRegex,
			opRegex: `^(?P<time>[^ ]+) (?P<hostname>[^ ]+) (?P<source_program>[^ \[]+)(\[\d+\])?: .*$`,
			opTimestamp: map[string]any{
				opParseFrom:  attrTime,
				opLayout:     "%Y-%m-%dT%H:%M:%S.%L%z",
				opLayoutType: layoutStrptime,
			},
			opIf: `body matches '^\\d{4}-'`,
		},
		{
			// Parse syslog when time use "Mar 10 14:39:12" format (e.g. Ubuntu 22.04)
			opType:  parserRegex,
			opRegex: `^(?P<time>[A-Za-z]{3} [0-9: ]+) (?P<hostname>[^ ]+) (?P<source_program>[^ \[]+)(\[\d+\])?: .*$`,
			opTimestamp: map[string]any{
				opParseFrom:  attrTime,
				opLayout:     "%b %d %H:%M:%S",
				opLayoutType: layoutStrptime,
			},
			opIf: `body matches '^[A-Za-z]{3} '`,
		},
		removeAttr("time"),
		removeAttr("hostname"),
	}

	auditdParser := []OTELOperator{
		{
			opType:  parserRegex,
			opRegex: `^type=(?P<type>[^ ]+) msg=audit\((?P<timestamp>[0-9.]+):[0-9]+\).*$`,
			opTimestamp: map[string]any{
				opParseFrom:  "attributes.timestamp",
				opLayout:     "s.ms",
				opLayoutType: "epoch",
			},
		},
		removeAttr("timestamp"),
	}

	return map[string][]OTELOperator{
		"json": {
			{
				opType: parserJSON,
			},
		},
		"json_golang_slog": {
			{
				opType: parserJSON,
				opTimestamp: map[string]any{
					opParseFrom:  attrTime,
					opLayout:     "2006-01-02T15:04:05.999999999Z07:00",
					opLayoutType: "gotime", // '07:00' as no strptime equivalent
				},
			},
			{
				opType:      "severity_parser",
				opParseFrom: "attributes.level",
				// Log level reference can be 'found' at https://pkg.go.dev/log/slog#Level
				// Mapping is OTEL severity -> slog level
				opMapping: map[string]any{
					"error4":  "ERROR+3",
					sevError3: "ERROR+2",
					sevError2: "ERROR+1",
					sevError:  "ERROR",
					"warn4":   "WARN+3",
					"warn3":   "WARN+2",
					"warn2":   "WARN+1",
					sevWarn:   "WARN",
					"info4":   "INFO+3",
					"info3":   "INFO+2",
					sevInfo2:  "INFO+1",
					sevInfo:   DefaultLogLevel,
					sevDebug4: "DEBUG+3",
					sevDebug3: "DEBUG+2",
					sevDebug2: "DEBUG+1",
					sevDebug:  mapDEBUG,
				},
			},
			removeAttr("time"),
			removeAttr("level"),
			removeAttr("msg"),
		},
		idNginxAccess: flattenOps(
			OTELOperator{
				"id":    idNginxAccess,
				opType:  opAdd,
				opField: attrLogStream,
				opValue: streamStdout,
			},
			nginxAccessParser,
			OTELOperator{
				opType:       parserTime,
				opParseFrom:  attrTime,
				opLayout:     "%d/%b/%Y:%H:%M:%S %z",
				opLayoutType: layoutStrptime,
			},
			removeAttr("time"),
		),
		idNginxError: flattenOps(
			OTELOperator{
				"id":    idNginxError,
				opType:  opAdd,
				opField: attrLogStream,
				opValue: streamStderr,
			},
			nginxErrorParser,
			OTELOperator{
				opType:       parserTime,
				opParseFrom:  attrTime,
				opLayout:     "%Y/%m/%d %H:%M:%S",
				opLayoutType: layoutStrptime,
			},
			removeAttr("time"),
		),
		"nginx_both": flattenOps(
			OTELOperator{
				opType: opRouter,
				opRoutes: []any{
					map[string]any{
						opExpr:   `body matches "^(\\d{1,3}\\.){3}\\d{1,3}\\s-\\s(-|[\\w-]+)\\s"`, // <- not regexp, but expr-lang
						opOutput: idNginxAccess,
					},
					map[string]any{
						opExpr:   `body matches "^\\d{4}\\/\\d{2}\\/\\d{2}\\s\\d{2}:\\d{2}:\\d{2}\\s\\[error]"`, // <- not regexp, but expr-lang
						opOutput: idNginxError,
					},
				},
			},
			// Start: nginx_access
			OTELOperator{
				"id":    idNginxAccess,
				opType:  opAdd,
				opField: attrLogStream,
				opValue: streamStdout,
			},
			nginxAccessParser,
			OTELOperator{
				opType:   opNoop,
				opOutput: idNginxBothEnd,
			},
			// End: nginx_access
			// Start: nginx_error
			OTELOperator{
				"id":    idNginxError,
				opType:  opAdd,
				opField: attrLogStream,
				opValue: streamStderr,
			},
			nginxErrorParser,
			OTELOperator{
				opType:   opNoop,
				opOutput: idNginxBothEnd,
			},
			// End: nginx_error
			OTELOperator{
				"id":   idNginxBothEnd,
				opType: opNoop,
			},
			removeAttr("time"),
		),
		idApacheAccess: flattenOps(
			OTELOperator{
				"id":    idApacheAccess,
				opType:  opAdd,
				opField: attrLogStream,
				opValue: streamStdout,
			},
			apacheAccessParser,
			OTELOperator{
				opType:       parserTime,
				opParseFrom:  attrTime,
				opLayout:     "%d/%b/%Y:%H:%M:%S %z",
				opLayoutType: layoutStrptime,
			},
			removeAttr("time"),
		),
		idApacheError: flattenOps(
			OTELOperator{
				"id":    idApacheError,
				opType:  opAdd,
				opField: attrLogStream,
				opValue: streamStderr,
			},
			apacheErrorParser,
			OTELOperator{
				opType:       parserTime,
				opParseFrom:  attrTime,
				opLayout:     "%a %b %d %H:%M:%S %Y",
				opLayoutType: layoutStrptime,
			},
			removeAttr("time"),
		),
		"apache_both": flattenOps(
			OTELOperator{
				opType: opRouter,
				opRoutes: []any{
					map[string]any{
						opExpr:   `body matches "^(\\d{1,3}\\.){3}\\d{1,3}\\s\\S+\\s\\S+\\s\\[[^\\]]+\\]\\s"`, // <- not regexp, but expr-lang
						opOutput: idApacheAccess,
					},
					map[string]any{
						opExpr:   `body matches "^\\[[\\w :.]+\\]\\s(\\[[^\\]]+\\])+\\s"`, // <- not regexp, but expr-lang
						opOutput: idApacheError,
					},
				},
			},
			// Start: apache_access
			OTELOperator{
				"id":    idApacheAccess,
				opType:  opAdd,
				opField: attrLogStream,
				opValue: streamStdout,
			},
			apacheAccessParser,
			OTELOperator{
				opType:   opNoop,
				opOutput: idApacheBothEnd,
			},
			// End: apache_access
			// Start: apache_error
			OTELOperator{
				"id":    idApacheError,
				opType:  opAdd,
				opField: attrLogStream,
				opValue: streamStderr,
			},
			apacheErrorParser,
			OTELOperator{
				opType:   opNoop,
				opOutput: idApacheBothEnd,
			},
			// End: apache_error
			OTELOperator{
				"id":   idApacheBothEnd,
				opType: opNoop,
			},
			removeAttr("time"),
		),
		"kafka": flattenOps(
			kafkaParser,
			OTELOperator{
				opType:       parserTime,
				opParseFrom:  attrTime,
				opLayout:     "%Y-%m-%d %H:%M:%S,%f",
				opLayoutType: layoutStrptime,
			},
			removeAttr("time"),
		),
		"kafka_docker": flattenOps(
			kafkaParser, // we'll rely on the timestamp provided by the runtime
			removeAttr("time"),
		),
		svcRedis: flattenOps(
			redisParser,
			OTELOperator{
				opType:       parserTime,
				opParseFrom:  attrTime,
				opLayout:     "%d %b %Y %H:%M:%S.%L",
				opLayoutType: layoutStrptime,
			},
			removeAttr("time"),
		),
		"redis_docker": flattenOps(
			redisParser, // we'll rely on the timestamp provided by the runtime
			removeAttr("time"),
		),
		svcValkey: flattenOps(
			redisParser,
			OTELOperator{
				opType:  opAdd,
				opField: attrDBSystemName,
				opValue: svcValkey,
			},
			OTELOperator{
				opType:       parserTime,
				opParseFrom:  attrTime,
				opLayout:     "%d %b %Y %H:%M:%S.%L",
				opLayoutType: layoutStrptime,
			},
		),
		"valkey_docker": flattenOps(
			redisParser, // we'll rely on the timestamp provided by the runtime
			OTELOperator{
				opType:  opAdd,
				opField: attrDBSystemName,
				opValue: svcValkey,
			},
			removeAttr("time"),
		),
		"haproxy": flattenOps(
			haproxyParser,
			OTELOperator{
				opType:       parserTime,
				opParseFrom:  attrTime,
				opLayout:     "%d/%b/%Y:%H:%M:%S.%L",
				opLayoutType: layoutStrptime,
				opIf:         `"time" in attributes`, // time is not provided by the haproxy_state_parser
			},
			removeAttr("time"),
		),
		"haproxy_docker": flattenOps(
			haproxyParser, // we'll rely on the timestamp provided by the runtime
			removeAttr("time"),
		),
		svcPostgresql: flattenOps(
			postgresqlParser,
			OTELOperator{
				opType:       parserTime,
				opParseFrom:  attrTime,
				opLayout:     "%Y-%m-%d %H:%M:%S.%L %Z",
				opLayoutType: layoutStrptime,
			},
			removeAttr("time"),
		),
		"postgresql_docker": flattenOps(
			postgresqlParser, // we'll rely on the timestamp provided by the runtime
			removeAttr("time"),
		),
		"mariadb": flattenOps(
			mariaDBParser,
			OTELOperator{
				opType:       parserTime,
				opParseFrom:  attrTime,
				opLayout:     "%Y-%m-%d %H:%M:%S",
				opLayoutType: layoutStrptime,
			},
			removeAttr("time"),
		),
		"mariadb_docker": flattenOps(
			mariaDBParser, // we'll rely on the timestamp provided by the runtime
			removeAttr("time"),
		),
		svcMysql: flattenOps(
			mysqlParser,
			// FIXME: logs from the [Entrypoint] component have a different timestamp format ...
			OTELOperator{
				opType:       parserTime,
				opParseFrom:  attrTime,
				opLayout:     "%Y-%m-%dT%H:%M:%S.%f%z",
				opLayoutType: layoutStrptime,
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
				opType:       parserTime,
				opParseFrom:  "attributes.t.$date",
				opLayout:     "%Y-%m-%dT%H:%M:%S.%L%j",
				opLayoutType: layoutStrptime,
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
				opType:       parserTime,
				opParseFrom:  attrTime,
				opLayout:     "%Y-%m-%d %H:%M:%S.%f%j",
				opLayoutType: layoutStrptime,
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
				opType:  opAdd,
				opField: attrFacility,
				opValue: "security/authorization",
			},
		),
	}
}
