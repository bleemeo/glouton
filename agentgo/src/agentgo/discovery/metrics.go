package discovery

import (
	"agentgo/inputs/apache"
	"agentgo/inputs/elasticsearch"
	"agentgo/inputs/memcached"
	"agentgo/inputs/modify"
	"agentgo/inputs/mongodb"
	"agentgo/inputs/mysql"
	"agentgo/inputs/nginx"
	"agentgo/inputs/phpfpm"
	"agentgo/inputs/postgresql"
	"agentgo/inputs/rabbitmq"
	"agentgo/inputs/redis"
	"agentgo/logger"
	"fmt"
	"net"
	"strconv"

	"github.com/influxdata/telegraf"
)

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
		logger.V(2).Printf("Remove input for service %v on container %s", key.name, key.containerID)
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
	di := servicesDiscoveryInfo[service.Name]

	var input telegraf.Input
	var err error
	switch service.Name {
	case ApacheService:
		if address := addressForPort(service, di); address != "" {
			statusURL := fmt.Sprintf("http://%s:%d/server-status?auto", address, di.ServicePort)
			if di.ServicePort == 80 {
				statusURL = fmt.Sprintf("http://%s/server-status?auto", address)
			}
			input, err = apache.New(statusURL)
		}
	case ElasticSearchService:
		if address := addressForPort(service, di); address != "" {
			input, err = elasticsearch.New(fmt.Sprintf("http://%s:%d", address, di.ServicePort))
		}
	case MemcachedService:
		if address := addressForPort(service, di); address != "" {
			input, err = memcached.New(fmt.Sprintf("%s:%d", address, di.ServicePort))
		}
	case MongoDBService:
		if address := addressForPort(service, di); address != "" {
			input, err = mongodb.New(fmt.Sprintf("mongodb://%s:%d", address, di.ServicePort))
		}
	case MySQLService:
		if address := addressForPort(service, di); address != "" && service.ExtraAttributes["password"] != "" {
			username := service.ExtraAttributes["username"]
			if username == "" {
				username = "root"
			}
			input, err = mysql.New(fmt.Sprintf("%s:%s@tcp(%s:%d)/", username, service.ExtraAttributes["password"], address, di.ServicePort))
		}
	case NginxService:
		if address := addressForPort(service, di); address != "" {
			input, err = nginx.New(fmt.Sprintf("http://%s:%d/nginx_status", address, di.ServicePort))
		}
	case PHPFPMService:
		statsURL := urlForPHPFPM(service)
		if statsURL != "" {
			input, err = phpfpm.New(statsURL)
		}
	case PostgreSQLService:
		if address := addressForPort(service, di); address != "" && service.ExtraAttributes["password"] != "" {
			username := service.ExtraAttributes["username"]
			if username == "" {
				username = "postgres"
			}
			input, err = postgresql.New(fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=postgres sslmode=disable", address, di.ServicePort, username, service.ExtraAttributes["password"]))
		}
	case RabbitMQService:
		if address := addressForPort(service, di); address != "" {
			username := service.ExtraAttributes["username"]
			password := service.ExtraAttributes["password"]
			mgmtPort := service.ExtraAttributes["mgmt_port"]
			if username == "" {
				username = "guest"
			}
			if password == "" {
				password = "guest"
			}
			if mgmtPort == "" {
				mgmtPort = "15672"
			}
			url := fmt.Sprintf("http://%s:%s", service.IPAddress, mgmtPort)
			input, err = rabbitmq.New(url, username, password)
		}
	case RedisService:
		if address := addressForPort(service, di); address != "" {
			input, err = redis.New(fmt.Sprintf("tcp://%s:%d", address, di.ServicePort))
		}
	default:
		logger.V(1).Printf("service type %s don't support metrics", service.Name)
	}

	if input != nil {
		if service.ContainerName != "" {
			input = modify.AddItem(input, service.ContainerName)
		}
		if err != nil {
			return err
		}
		return d.addInput(input, service)
	}

	return nil
}

func (d *Discovery) addInput(input telegraf.Input, service Service) error {
	if d.coll == nil {
		return nil
	}
	inputID := d.coll.AddInput(input, string(service.Name))
	key := nameContainer{
		name:        service.Name,
		containerID: service.ContainerID,
	}
	d.activeInput[key] = inputID
	return nil
}

// addressForPort returns the IP address for the servicePort or empty if it don't listen on this port
func addressForPort(service Service, di discoveryInfo) string {
	if di.ServicePort == 0 {
		return ""
	}
	for _, a := range service.ListenAddresses {
		if a.Network() != di.ServiceProtocol {
			continue
		}
		address, portStr, err := net.SplitHostPort(a.String())
		if err != nil {
			continue
		}
		port, err := strconv.ParseInt(portStr, 10, 0)
		if err != nil {
			continue
		}
		if address == "0.0.0.0" {
			address = service.IPAddress
		}
		if int(port) == di.ServicePort {
			return address
		}
	}
	return ""
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
