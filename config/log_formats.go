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

func DefaultKnownLogFormats() map[string][]OTELOperator {
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
		"nginx_access": {
			{
				"id":    "nginx_access",
				"type":  "add",
				"field": "attributes['log.iostream']",
				"value": "stdout",
			},
			{
				"id":    "nginx_access_parser",
				"type":  "regex_parser",
				"regex": `^(?<host>(\d{1,3}\.){3}\d{1,3})\s-\s(-|[\w-]+)\s\[(?<time>\d{1,2}\/\w{1,15}\/\d{4}(:\d{2}){3}\s\+\d{4})\]\s(?<request>.+)\n*$`,
			},
		},
		"nginx_error": {
			{
				"id":    "nginx_error",
				"type":  "add",
				"field": "attributes['log.iostream']",
				"value": "stderr",
			},
			{
				"id":    "nginx_error_parser",
				"type":  "regex_parser",
				"regex": `(?<time>^\d{4}\/\d{2}\/\d{2}\s\d{2}:\d{2}:\d{2})\s\[\w+]\s\d+#\d+:\s.*\n*$`,
			},
		},
		"nginx_combined": {
			{
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
			{
				"id":    "nginx_access",
				"type":  "add",
				"field": "attributes['log.iostream']",
				"value": "stdout",
			},
			{
				"id":     "nginx_access_parser",
				"type":   "regex_parser",
				"regex":  `^(?<host>(\d{1,3}\.){3}\d{1,3})\s-\s(-|[\w-]+)\s\[(?<time>\d{1,2}\/\w{1,15}\/\d{4}(:\d{2}){3}\s\+\d{4})\]\s(?<request>.+)\n*$`,
				"output": "nginx_combined_end",
			},
			// End: nginx_access
			// Start: nginx_error
			{
				"id":    "nginx_error",
				"type":  "add",
				"field": "attributes['log.iostream']",
				"value": "stderr",
			},
			{
				"id":     "nginx_error_parser",
				"type":   "regex_parser",
				"regex":  `(?<time>^\d{4}\/\d{2}\/\d{2}\s\d{2}:\d{2}:\d{2})\s\[\w+]\s\d+#\d+:\s.*\n*$`,
				"output": "nginx_combined_end",
			},
			// End: nginx_error
			{
				"id":   "nginx_combined_end",
				"type": "noop",
			},
		},
	}
}
