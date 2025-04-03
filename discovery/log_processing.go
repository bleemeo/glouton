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
	FileFormats  []ServiceLogReceiver
	DockerFormat string
}

var servicesLogInfo = map[ServiceName]logProcessingInfo{ //nolint: gochecknoglobals
	ApacheService: {
		FileFormats: []ServiceLogReceiver{
			{"/var/log/apache2/access.log", "apache_access"},
			{"/var/log/apache2/error.log", "apache_error"},
		},
		DockerFormat: "apache_both",
	},
	NginxService: {
		FileFormats: []ServiceLogReceiver{
			{"/var/log/nginx/access.log", "nginx_access"},
			{"/var/log/nginx/error.log", "nginx_error"},
		},
		DockerFormat: "nginx_both",
	},
	KafkaService: {
		FileFormats: []ServiceLogReceiver{
			// TODO
		},
		DockerFormat: "kafka_docker",
	},
	RedisService: {
		FileFormats: []ServiceLogReceiver{
			// TODO
		},
		DockerFormat: "redis_docker",
	},
	HAProxyService: {
		FileFormats: []ServiceLogReceiver{
			// TODO
		},
		DockerFormat: "haproxy", // no need for a container-specific behavior
	},
	/*PostgreSQLService: {
		FileFormats: []ServiceLogReceiver{
			// TODO
		},
		DockerFormat: "postgresql_docker",
	},*/
	MySQLService: {
		FileFormats: []ServiceLogReceiver{
			// TODO
		},
		DockerFormat: "mysql_docker",
	},
	MongoDBService: {
		FileFormats: []ServiceLogReceiver{
			// TODO
		},
		DockerFormat: "mongodb_docker",
	},
}

type ServiceLogReceiver struct {
	FilePath string // ignored if in a container
	Format   string
}

func inferLogProcessingConfig(service Service, knownLogFormats map[string][]config.OTELOperator) Service {
	switch {
	case len(service.Config.LogFiles) > 0:
		if service.container != nil {
			logger.V(1).Printf("Can't watch specific log files on service %q: it runs in a container", service.Name)

			return service
		}

		service.LogProcessing = make([]ServiceLogReceiver, 0, len(service.Config.LogFiles))

		for i, logFile := range service.Config.LogFiles {
			if logFile.FilePath == "" {
				logger.V(1).Printf("No path provided for log file n°%d on service %q", i+1, service.Name)

				continue
			}

			logFormat := logFile.LogFormat
			if logFormat == "" {
				if service.Config.LogFormat != "" {
					logFormat = service.Config.LogFormat
				} else {
					logger.V(1).Printf("No log format specified for log file %q on service %q", logFile.FilePath, service.Name)

					continue
				}
			}

			_, ok := knownLogFormats[logFormat]
			if !ok {
				logger.V(1).Printf("Service %q requires an unknown log format: %q", service.Name, logFormat)

				return service
			}

			service.LogProcessing = append(service.LogProcessing, ServiceLogReceiver{
				FilePath: logFile.FilePath,
				Format:   logFormat,
			})
		}

		logger.Printf("Using user-defined formats for service %q/%q: %v", service.Name, service.Instance, service.LogProcessing) // TODO: remove
	case service.Config.LogFormat != "":
		if service.container == nil {
			logger.V(1).Printf("Service %q only provides a log format but doesn't run in a container", service.Name)

			return service
		}

		_, ok := knownLogFormats[service.Config.LogFormat]
		if !ok {
			logger.V(1).Printf("Service %q requires an unknown log format: %q", service.Name, service.Config.LogFormat)

			return service
		}

		service.LogProcessing = []ServiceLogReceiver{
			{
				Format: service.Config.LogFormat,
			},
		}

		logger.Printf("Using known log format %q for service %q/%q", service.Config.LogFormat, service.Name, service.Instance) // TODO: remove
	default:
		logProcessInfo, found := servicesLogInfo[service.ServiceType]
		if !found {
			return service
		}

		if service.container != nil {
			service.LogProcessing = []ServiceLogReceiver{
				{
					Format: logProcessInfo.DockerFormat,
				},
			}
		} else {
			service.LogProcessing = make([]ServiceLogReceiver, len(logProcessInfo.FileFormats))

			copy(service.LogProcessing, logProcessInfo.FileFormats)
		}

		logger.V(1).Printf("Using default log processing config for service %q/%q: %v", service.Name, service.Instance, service.LogProcessing)
	}

	return service
}
