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
	FileFormats   []ServiceLogReceiver
	DefaultFormat string
	DockerFormat  string
}

var servicesLogInfo = map[ServiceName]logProcessingInfo{ //nolint: gochecknoglobals
	ApacheService: {
		FileFormats: []ServiceLogReceiver{
			{
				FilePath: "/var/log/apache2/access.log",
				Format:   "apache_access",
			},
			{
				FilePath: "/var/log/apache2/error.log",
				Format:   "apache_error",
			},
		},
		DockerFormat: "apache_both",
	},
	NginxService: {
		FileFormats: []ServiceLogReceiver{
			{
				FilePath: "/var/log/nginx/access.log",
				Format:   "nginx_access",
			},
			{
				FilePath: "/var/log/nginx/error.log",
				Format:   "nginx_error",
			},
		},
		DockerFormat: "nginx_both",
	},
	KafkaService: {
		FileFormats: []ServiceLogReceiver{
			// TODO
		},
		DefaultFormat: "kafka",
		DockerFormat:  "kafka_docker",
	},
	RedisService: {
		FileFormats: []ServiceLogReceiver{
			{
				FilePath: "/var/log/redis/redis-server.log",
				Format:   "redis",
			},
		},
		DefaultFormat: "redis",
		DockerFormat:  "redis_docker",
	},
	HAProxyService: {
		FileFormats: []ServiceLogReceiver{
			// TODO
		},
		DefaultFormat: "haproxy",
		DockerFormat:  "haproxy_docker",
	},
	PostgreSQLService: {
		FileFormats: []ServiceLogReceiver{
			{
				FilePath: "/var/log/postgresql/postgresql-*-main.log",
				Format:   "postgresql",
			},
		},
		DefaultFormat: "postgresql",
		DockerFormat:  "postgresql_docker",
	},
	MySQLService: {
		FileFormats: []ServiceLogReceiver{
			{
				FilePath: "/var/log/mysql/error.log",
				Format:   "mysql",
			},
		},
		DefaultFormat: "mysql",
		DockerFormat:  "mysql_docker",
	},
	MongoDBService: {
		FileFormats: []ServiceLogReceiver{
			// TODO
		},
		DefaultFormat: "mongodb",
		DockerFormat:  "mongodb_docker",
	},
	RabbitMQService: {
		FileFormats: []ServiceLogReceiver{
			// TODO
		},
		DefaultFormat: "rabbitmq",
		DockerFormat:  "rabbitmq_docker",
	},
}

type ServiceLogReceiver struct {
	FilePath string // ignored if in a container
	Format   string
	Filter   string
}

func inferLogProcessingConfig(service Service, knownLogFormats map[string][]config.OTELOperator, knownLogFilters map[string]config.OTELFilters) Service {
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
			if logFormat == "" && service.Config.LogFormat != "" {
				logFormat = service.Config.LogFormat
			}

			if logFormat != "" {
				_, ok := knownLogFormats[logFormat]
				if !ok {
					logger.V(1).Printf("Service %q requires an unknown log format: %q", service.Name, logFormat)

					return service
				}
			} else { // final fallback
				logFormat = servicesLogInfo[service.ServiceType].DefaultFormat
			}

			logFilter := logFile.LogFilter
			if logFilter == "" && service.Config.LogFilter != "" {
				logFilter = service.Config.LogFilter
			}

			if logFilter != "" {
				_, ok := knownLogFilters[logFilter]
				if !ok {
					logger.V(1).Printf("Service %q requires an unknown log filter: %q", service.Name, logFilter)

					return service
				}
			}

			service.LogProcessing = append(service.LogProcessing, ServiceLogReceiver{
				FilePath: logFile.FilePath,
				Format:   logFormat,
				Filter:   logFilter,
			})
		}
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

		if service.Config.LogFilter != "" {
			_, ok = knownLogFilters[service.Config.LogFilter]
			if !ok {
				logger.V(1).Printf("Service %q requires an unknown log filter: %q", service.Name, service.Config.LogFilter)

				return service
			}
		}

		service.LogProcessing = []ServiceLogReceiver{
			{
				Format: service.Config.LogFormat,
				Filter: service.Config.LogFilter, // maybe empty
			},
		}
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

		logger.V(2).Printf("Using default log processing config for service %q/%q: %v", service.Name, service.Instance, service.LogProcessing)
	}

	return service
}
