// Copyright 2015-2019 Bleemeo
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
	"errors"
	"fmt"
	"glouton/collector"
	"glouton/inputs"
	"glouton/inputs/apache"
	"glouton/inputs/cpu"
	"glouton/inputs/disk"
	"glouton/inputs/diskio"
	"glouton/inputs/elasticsearch"
	"glouton/inputs/haproxy"
	"glouton/inputs/mem"
	"glouton/inputs/memcached"
	"glouton/inputs/modify"
	"glouton/inputs/mongodb"
	"glouton/inputs/mysql"
	netInput "glouton/inputs/net"
	"glouton/inputs/nginx"
	"glouton/inputs/phpfpm"
	"glouton/inputs/postgresql"
	"glouton/inputs/rabbitmq"
	"glouton/inputs/redis"
	"glouton/inputs/swap"
	"glouton/inputs/system"
	"glouton/inputs/winperfcounters"
	"glouton/inputs/zookeeper"
	"glouton/logger"
	"glouton/types"
	"runtime"
	"strconv"

	"github.com/influxdata/telegraf"
)

var (
	errNotSupported = errors.New("service not supported by Prometheus collector")
)

// AddDefaultInputs adds system inputs to a collector.
func AddDefaultInputs(coll *collector.Collector, inputsConfig inputs.CollectorConfig) error {
	input, err := system.New()
	if err != nil {
		return err
	}

	if _, err = coll.AddInput(input, "system"); err != nil {
		return err
	}

	input, err = cpu.New()
	if err != nil {
		return err
	}

	if _, err = coll.AddInput(input, "cpu"); err != nil {
		return err
	}

	input, err = netInput.New(inputsConfig.NetIfBlacklist)
	if err != nil {
		return err
	}

	if _, err = coll.AddInput(input, "net"); err != nil {
		return err
	}

	if inputsConfig.DFRootPath != "" {
		input, err = disk.New(inputsConfig.DFRootPath, inputsConfig.DFPathBlacklist)
		if err != nil {
			return err
		}

		if _, err = coll.AddInput(input, "disk"); err != nil {
			return err
		}
	}

	input, err = diskio.New(inputsConfig.IODiskWhitelist, inputsConfig.IODiskBlacklist)
	if err != nil {
		return err
	}

	if _, err = coll.AddInput(input, "diskio"); err != nil {
		return err
	}

	return addDefaultFromOS(inputsConfig, coll)
}

func addDefaultFromOS(inputsConfig inputs.CollectorConfig, coll *collector.Collector) error {
	var input telegraf.Input

	var err error

	switch runtime.GOOS {
	case "windows":
		input, err = winperfcounters.New(inputsConfig)
		if err != nil {
			return err
		}

		_, err = coll.AddInput(input, "win_perf_counters")
		if err != nil {
			return err
		}
	default:
		// on windows, win_perf_counters provides the metrics for the memory
		input, err = mem.New()
		if err != nil {
			return err
		}

		if _, err = coll.AddInput(input, "mem"); err != nil {
			return err
		}

		input, err = swap.New()
		if err != nil {
			return err
		}

		if _, err = coll.AddInput(input, "swap"); err != nil {
			return err
		}
	}

	return nil
}

func (d *Discovery) configureMetricInputs(oldServices, services map[NameContainer]Service) (err error) {
	for key := range oldServices {
		if _, ok := services[key]; !ok {
			d.removeInput(key)
		}
	}

	for key, service := range services {
		oldService, ok := oldServices[key]
		if !ok || serviceNeedUpdate(oldService, service) {
			d.removeInput(key)

			err = d.createInput(service)
			if err != nil {
				return
			}
		}
	}

	return nil
}

func serviceNeedUpdate(oldService, service Service) bool {
	switch {
	case oldService.Name != service.Name,
		oldService.ServiceType != service.ServiceType,
		oldService.ContainerID != service.ContainerID,
		oldService.ContainerName != service.ContainerName,
		oldService.IPAddress != service.IPAddress,
		oldService.ExePath != service.ExePath,
		oldService.Stack != service.Stack,
		oldService.Active != service.Active,
		oldService.CheckIgnored != service.CheckIgnored,
		oldService.MetricsIgnored != service.MetricsIgnored:
		return true
	case len(oldService.ListenAddresses) != len(service.ListenAddresses):
		return true
	case len(oldService.ExtraAttributes) != len(service.ExtraAttributes):
		return true
	}

	// We assume order of ListenAddresses is mostly stable. serviceEqual may return
	// some false positive.
	for i, old := range oldService.ListenAddresses {
		new := service.ListenAddresses[i]
		if old.Network() != new.Network() || old.String() != new.String() {
			return true
		}
	}

	for k, v := range oldService.ExtraAttributes {
		if v != service.ExtraAttributes[k] {
			return true
		}
	}

	return false
}

func (d *Discovery) removeInput(key NameContainer) {
	if d.coll == nil {
		return
	}

	if collector, ok := d.activeCollector[key]; ok {
		logger.V(2).Printf("Remove input for service %v on container %s", key.Name, key.ContainerName)
		delete(d.activeCollector, key)

		if collector.gathererID == 0 {
			d.coll.RemoveInput(collector.inputID)
		} else if !d.metricRegistry.UnregisterGatherer(collector.gathererID) {
			logger.V(2).Printf("The gatherer wasn't present")
		}
	}
}

// createPrometheusCollector create a Prometheus collector for given service
// Return errNotSupported if no Prometheus collector exists for this service.
func (d *Discovery) createPrometheusCollector(service Service) error {
	if service.ServiceType == MemcachedService {
		return d.createPrometheusMemcached(service)
	}

	return errNotSupported
}

//nolint: gocyclo
func (d *Discovery) createInput(service Service) error {
	if !service.Active {
		return nil
	}

	if service.MetricsIgnored {
		logger.V(2).Printf("The input associated to the service '%s' on container '%s' is ignored by the configuration", service.Name, service.ContainerID)
		return nil
	}

	if d.metricFormat == types.MetricFormatPrometheus {
		err := d.createPrometheusCollector(service)
		if !errors.Is(err, errNotSupported) {
			logger.V(2).Printf("Add collector for service %v on container %s", service.Name, service.ContainerID)
			return err
		}
	}

	var (
		err   error
		input telegraf.Input
	)

	switch service.ServiceType {
	case ApacheService:
		if ip, port := service.AddressPort(); ip != "" {
			statusURL := fmt.Sprintf("http://%s:%d/server-status?auto", ip, port)

			if port == 80 {
				statusURL = fmt.Sprintf("http://%s/server-status?auto", ip)
			}

			input, err = apache.New(statusURL)
		}
	case ElasticSearchService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = elasticsearch.New(fmt.Sprintf("http://%s:%d", ip, port))
		}
	case HAProxyService:
		if service.ExtraAttributes["stats_url"] != "" {
			input, err = haproxy.New(service.ExtraAttributes["stats_url"])
		}
	case MemcachedService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = memcached.New(fmt.Sprintf("%s:%d", ip, port))
		}
	case MongoDBService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = mongodb.New(fmt.Sprintf("mongodb://%s:%d", ip, port))
		}
	case MySQLService:
		if ip, port := service.AddressPort(); ip != "" && service.ExtraAttributes["password"] != "" {
			username := service.ExtraAttributes["username"]
			if username == "" {
				username = "root"
			}

			input, err = mysql.New(fmt.Sprintf("%s:%s@tcp(%s:%d)/", username, service.ExtraAttributes["password"], ip, port))
		}
	case NginxService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = nginx.New(fmt.Sprintf("http://%s:%d/nginx_status", ip, port))
		}
	case PHPFPMService:
		statsURL := urlForPHPFPM(service)
		if statsURL != "" {
			input, err = phpfpm.New(statsURL)
		}
	case PostgreSQLService:
		if ip, port := service.AddressPort(); ip != "" && service.ExtraAttributes["password"] != "" {
			username := service.ExtraAttributes["username"]
			if username == "" {
				username = "postgres"
			}

			input, err = postgresql.New(fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=postgres sslmode=disable", ip, port, username, service.ExtraAttributes["password"]))
		}
	case RabbitMQService:
		mgmtPortStr := service.ExtraAttributes["mgmt_port"]
		mgmtPort := 15672
		force := false

		if mgmtPortStr != "" {
			tmp, err := strconv.ParseInt(mgmtPortStr, 10, 0)
			if err != nil {
				mgmtPort = int(tmp)
				force = true
			} else {
				logger.V(1).Printf("%#v is not a valid port number for service RabbitMQ", mgmtPortStr)
			}
		}

		if ip := service.AddressForPort(mgmtPort, "tcp", force); ip != "" {
			username := service.ExtraAttributes["username"]
			if username == "" {
				username = "guest"
			}

			password := service.ExtraAttributes["password"]
			if password == "" {
				password = "guest"
			}

			url := fmt.Sprintf("http://%s:%d", ip, mgmtPort)
			input, err = rabbitmq.New(url, username, password)
		}
	case RedisService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = redis.New(fmt.Sprintf("tcp://%s:%d", ip, port))
		}
	case ZookeeperService:
		if ip, port := service.AddressPort(); ip != "" {
			input, err = zookeeper.New(fmt.Sprintf("%s:%d", ip, port))
		}
	case CustomService:
		return nil
	default:
		logger.V(1).Printf("service type %s don't support metrics", service.ServiceType)
	}

	if err != nil {
		return err
	}

	if input != nil {
		logger.V(2).Printf("Add input for service %v on container %s", service.Name, service.ContainerID)

		input = modify.AddRenameCallback(input, func(labels map[string]string, annotations types.MetricAnnotations) (map[string]string, types.MetricAnnotations) {
			annotations.ServiceName = service.Name
			annotations.ContainerID = service.ContainerID

			labels[types.LabelMetaContainerName] = service.ContainerName
			labels[types.LabelMetaServiceName] = service.ContainerName
			labels[types.LabelMetaContainerID] = service.ContainerName

			_, port := service.AddressPort()
			if port != 0 {
				labels[types.LabelMetaServicePort] = strconv.FormatInt(int64(port), 10)
			}

			if service.ContainerName != "" {
				if annotations.BleemeoItem != "" {
					annotations.BleemeoItem = service.ContainerName + "_" + annotations.BleemeoItem
				} else {
					annotations.BleemeoItem = service.ContainerName
				}
			}

			return labels, annotations
		})

		return d.addInput(input, service)
	}

	return nil
}

func (d *Discovery) addInput(input telegraf.Input, service Service) error {
	if d.coll == nil {
		return nil
	}

	inputID, err := d.coll.AddInput(input, service.Name)
	if err != nil {
		return err
	}

	key := NameContainer{
		Name:          service.Name,
		ContainerName: service.ContainerName,
	}
	d.activeCollector[key] = collectorDetails{
		inputID: inputID,
	}

	return nil
}

func urlForPHPFPM(service Service) string {
	url := service.ExtraAttributes["stats_url"]
	if url != "" {
		return url
	}

	if service.ExtraAttributes["port"] != "" && service.IPAddress != "" {
		return fmt.Sprintf("fcgi://%s:%s/status", service.IPAddress, service.ExtraAttributes["port"])
	}

	for _, v := range service.ListenAddresses {
		if v.Network() != tcpPortocol {
			continue
		}

		return fmt.Sprintf("fcgi://%s/status", v.String())
	}

	return ""
}
