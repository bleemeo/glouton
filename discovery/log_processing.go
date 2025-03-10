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

package discovery

import (
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
)

type logProcessingInfo struct {
	Operators        []config.OTELOperator
	FirstOpByLogFile map[string]string
	DockerRouter     config.OTELOperator
}

// TODO: helper functions to bootstrap operators
var servicesLogInfo = map[ServiceName]logProcessingInfo{ //nolint: gochecknoglobals
	NginxService: {
		Operators: []config.OTELOperator{
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
				"output": "add_service_name",
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
				"output": "add_service_name",
			},
			// End: nginx_error
			{
				"id":    "add_service_name",
				"type":  "add",
				"field": "resource['service.name']",
				"value": "nginx",
			},
		},
		FirstOpByLogFile: map[string]string{
			"/var/log/nginx/access.log": "nginx_access",
			"/var/log/nginx/error.log":  "nginx_error",
		},
		DockerRouter: config.OTELOperator{
			"type": "router",
			"routes": []map[string]any{
				{
					"expr":   `body matches "^(\\d{1,3}\\.){3}\\d{1,3}\\s-\\s(-|[\\w-]+)\\s"`, // <- not regexp, but expr-lang
					"output": "nginx_access",
				},
				{
					"expr":   `body matches "^\\d{4}\\/\\d{2}\\/\\d{2}\\s\\d{2}:\\d{2}:\\d{2}\\s\\[error]"`, // <- not regexp, but expr-lang
					"output": "nginx_error",
				},
			},
		},
	},
}

type ServiceLogReceiver struct {
	LogFilePath string // only required if not in a container
	Operators   []config.OTELOperator
}

func inferLogProcessingConfig(service *Service, globalOperators map[string][]config.OTELOperator) {
	if service.Config.LogParser != "" {
		operators, ok := globalOperators[service.Config.LogParser]
		if !ok {
			logger.V(1).Printf("Service %q requires an unknown log parser: %q", service.Name, service.Config.LogParser)

			return
		}

		if service.container == nil && service.Config.LogFile == "" {
			logger.V(1).Printf("Service %q has no log file specified to run the specified parser on", service.Name)

			return
		}

		service.LogProcessing = []ServiceLogReceiver{
			{
				LogFilePath: service.Config.LogFile, // ignored if in a container
				Operators:   operators,
			},
		}

		logger.Printf("Using global parser %q for service %q", service.Config.LogParser, service.Name) // TODO: remove
	} else {
		logProcessInfo, found := servicesLogInfo[service.ServiceType]
		if !found {
			return
		}

		if service.container != nil {
			service.LogProcessing = []ServiceLogReceiver{
				{
					Operators: append(
						[]config.OTELOperator{logProcessInfo.DockerRouter},
						logProcessInfo.Operators...,
					),
				},
			}
		} else {
			service.LogProcessing = make([]ServiceLogReceiver, 0, len(logProcessInfo.FirstOpByLogFile))

			for logFilePath, firstOpName := range logProcessInfo.FirstOpByLogFile {
				firstOp := config.OTELOperator{
					"type":   "noop",
					"output": firstOpName,
				}
				logReceiver := ServiceLogReceiver{
					LogFilePath: logFilePath,
					Operators: append(
						[]config.OTELOperator{firstOp},
						logProcessInfo.Operators...,
					),
				}
				service.LogProcessing = append(service.LogProcessing, logReceiver)
			}
		}

		logger.V(1).Printf("Using default log processing config for service %q", service.Name) // TODO: V(2)
	}
}
