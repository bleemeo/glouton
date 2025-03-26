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
// Its main utility is to allow dots (.) in attribute names that come from regexp named groups.
func renameAttr(from, to string) OTELOperator {
	return OTELOperator{
		"type": "move",
		"from": "attributes." + from,
		"to":   "attributes['" + to + "']",
	}
}

func DefaultKnownLogFormats() map[string][]OTELOperator {
	nginxAccessParser := []OTELOperator{
		{
			"id":    "nginx_access_parser",
			"type":  "regex_parser",
			"regex": `^(?P<client_address>\S+) \S+ (?P<user_name>\S+) \[(?P<time>[^\]]+)] "(?P<http_request_method>\S+) (?P<http_route>\S+) \S+" (?P<http_response_status_code>\d+) (?P<http_response_size>\S+) "(?P<http_connection_state>[^"]*)" "(?P<user_agent_original>[^"]*)"`,
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
			"regex": `(?P<time>\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) \[\w+] \d+#\d+: \*\d+ .*?, client: (?P<client_address>[\d\.]+), server: (?P<server_address>\S+), request: "(?P<http_request_method>\S+) (?P<http_route>\S+) \S+", host: "(?P<network_local_address>(\d{1,3}\.){3}\d{1,3}):(?<server_port>\d+)"`,
		},
		renameAttr("client_address", "client.address"),
		renameAttr("server_address", "server.address"),
		renameAttr("http_request_method", "http.request.method"),
		renameAttr("http_route", "http.route"),
		renameAttr("network_local_address", "network.local.address"),
		renameAttr("server_port", "server.port"),
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
		),
		"nginx_error": flattenOps(
			OTELOperator{
				"id":    "nginx_error",
				"type":  "add",
				"field": "attributes['log.iostream']",
				"value": "stderr",
			},
			nginxErrorParser,
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
	}
}
