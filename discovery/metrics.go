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
	"agentgo/collector"
	"agentgo/inputs/apache"
	"agentgo/inputs/cpu"
	"agentgo/inputs/disk"
	"agentgo/inputs/diskio"
	"agentgo/inputs/elasticsearch"
	"agentgo/inputs/haproxy"
	"agentgo/inputs/mem"
	"agentgo/inputs/memcached"
	"agentgo/inputs/modify"
	"agentgo/inputs/mongodb"
	"agentgo/inputs/mysql"
	netInput "agentgo/inputs/net"
	"agentgo/inputs/nginx"
	"agentgo/inputs/phpfpm"
	"agentgo/inputs/postgresql"
	"agentgo/inputs/process"
	"agentgo/inputs/rabbitmq"
	"agentgo/inputs/redis"
	"agentgo/inputs/swap"
	"agentgo/inputs/system"
	"agentgo/inputs/zookeeper"
	"agentgo/logger"
	"fmt"
	"strconv"

	"github.com/influxdata/telegraf"
)

// InputOption are option used by system inputs
type InputOption struct {
	DFRootPath      string
	DFPathBlacklist []string
	NetIfBlacklist  []string
	IODiskWhitelist []string
}

// AddDefaultInputs adds system inputs to a collector
func AddDefaultInputs(coll *collector.Collector, option InputOption) error {
	var input telegraf.Input
	var err error

	input, err = system.New()
	if err != nil {
		return err
	}
	if _, err = coll.AddInput(input, "system"); err != nil {
		return err
	}

	input, err = process.New()
	if err != nil {
		return err
	}
	if _, err = coll.AddInput(input, "process"); err != nil {
		return err
	}

	input, err = cpu.New()
	if err != nil {
		return err
	}
	if _, err = coll.AddInput(input, "cpu"); err != nil {
		return err
	}

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

	input, err = netInput.New(option.NetIfBlacklist)
	if err != nil {
		return err
	}
	if _, err = coll.AddInput(input, "net"); err != nil {
		return err
	}

	if option.DFRootPath != "" {
		input, err = disk.New(option.DFRootPath, option.DFPathBlacklist)
		if err != nil {
			return err
		}
		if _, err = coll.AddInput(input, "disk"); err != nil {
			return err
		}
	}

	input, err = diskio.New(option.IODiskWhitelist)
	if err != nil {
		return err
	}
	if _, err = coll.AddInput(input, "diskio"); err != nil {
		return err
	}
	return nil
}

func (d *Discovery) configureMetricInputs(oldServices, services map[nameContainer]Service) (err error) {
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
	if oldService.IPAddress != service.IPAddress || oldService.Active != service.Active {
		return true
	}
	if len(oldService.ListenAddresses) != len(service.ListenAddresses) {
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
	return false
}

func (d *Discovery) removeInput(key nameContainer) {
	if d.coll == nil {
		return
	}
	if inputID, ok := d.activeInput[key]; ok {
		logger.V(2).Printf("Remove input for service %v on container %s", key.name, key.containerName)
		delete(d.activeInput, key)
		d.coll.RemoveInput(inputID)
	}
}

//nolint: gocyclo
func (d *Discovery) createInput(service Service) error {
	if !service.Active {
		return nil
	}

	logger.V(2).Printf("Add input for service %v on container %s", service.Name, service.ContainerID)
	var input telegraf.Input
	var err error
	switch service.Name {
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
			password := service.ExtraAttributes["password"]
			if username == "" {
				username = "guest"
			}
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
	default:
		logger.V(1).Printf("service type %s don't support metrics", service.Name)
	}
	if err != nil {
		return err
	}

	if input != nil {
		extraLabels := map[string]string{
			"service_name": string(service.Name),
		}
		if service.ContainerName != "" {
			extraLabels["item"] = service.ContainerName
			extraLabels["container_id"] = service.ContainerID
			extraLabels["container_name"] = service.ContainerName
		}
		input = modify.AddLabels(input, extraLabels)
		return d.addInput(input, service)
	}

	return nil
}

func (d *Discovery) addInput(input telegraf.Input, service Service) error {
	if d.coll == nil {
		return nil
	}
	inputID, err := d.coll.AddInput(input, string(service.Name))
	if err != nil {
		return err
	}
	key := nameContainer{
		name:          service.Name,
		containerName: service.ContainerName,
	}
	d.activeInput[key] = inputID
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
