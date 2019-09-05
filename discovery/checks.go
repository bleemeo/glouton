package discovery

import (
	"agentgo/check"
	"agentgo/logger"
	"agentgo/task"
	"fmt"
)

func (d *Discovery) configureChecks(oldServices, services map[nameContainer]Service) {
	for key := range oldServices {
		if _, ok := services[key]; !ok {
			d.removeCheck(key)
		}
	}

	for key, service := range services {
		oldService, ok := oldServices[key]
		if !ok || serviceNeedUpdate(oldService, service) {
			d.removeCheck(key)
			d.createCheck(service)
		}
	}
}

func (d *Discovery) removeCheck(key nameContainer) {
	if d.taskRegistry == nil {
		return
	}
	if checkID, ok := d.activeCheck[key]; ok {
		logger.V(2).Printf("Remove check for service %v on container %s", key.name, key.containerID)
		delete(d.activeCheck, key)
		d.taskRegistry.RemoveTask(checkID)
	}
}

func (d *Discovery) createCheck(service Service) {
	if !service.Active {
		return
	}

	logger.V(2).Printf("Add check for service %v on container %s", service.Name, service.ContainerID)

	di := servicesDiscoveryInfo[service.Name]
	primaryIP := addressForPort(service, di)
	tcpAddresses := make([]string, 0)
	for _, a := range service.ListenAddresses {
		if a.Network() != tcpPortocol {
			continue
		}
		tcpAddresses = append(tcpAddresses, a.String())
	}

	labels := map[string]string{
		"service_name": string(service.Name),
	}
	if service.ContainerName != "" {
		labels["item"] = service.ContainerName
		labels["container_id"] = service.ContainerID
		labels["container_name"] = service.ContainerName
	}

	switch service.Name {
	case DovecoteService, MemcachedService, RabbitMQService, RedisService, ZookeeperService:
		d.createTCPCheck(service, di, primaryIP, tcpAddresses, labels)
	case ApacheService, InfluxDBService, NginxService, SquidService:
		d.createHTTPCheck(service, di, primaryIP, tcpAddresses, labels)
	case NTPService:
		if primaryIP != "" {
			check := check.NewNTP(
				fmt.Sprintf("%s:%d", primaryIP, di.ServicePort),
				tcpAddresses,
				fmt.Sprintf("%s_status", service.Name),
				labels,
				d.acc,
			)
			d.addCheck(check.Run, service)
		} else {
			d.createTCPCheck(service, di, primaryIP, tcpAddresses, labels)
		}
	default:
		d.createTCPCheck(service, di, primaryIP, tcpAddresses, labels)
	}
}

func (d *Discovery) createTCPCheck(service Service, di discoveryInfo, primaryIP string, tcpAddresses []string, labels map[string]string) {

	var primaryAddress string
	if di.ServiceProtocol == tcpPortocol && primaryIP != "" {
		primaryAddress = fmt.Sprintf("%s:%d", primaryIP, di.ServicePort)
	}

	var tcpSend, tcpExpect, tcpClose []byte
	switch service.Name {
	case DovecoteService:
		tcpSend = []byte("001 NOOP\n")
		tcpExpect = []byte("001 OK")
		tcpClose = []byte("002 LOGOUT\n")
	case MemcachedService:
		tcpSend = []byte("version\r\n")
		tcpExpect = []byte("VERSION")
	case RabbitMQService:
		tcpSend = []byte("PINGAMQP")
		tcpExpect = []byte("AMQP")
	case RedisService:
		tcpSend = []byte("PING\n")
		tcpExpect = []byte("+PONG")
	case ZookeeperService:
		tcpSend = []byte("ruok\n")
		tcpExpect = []byte("imok")
	}
	if primaryAddress != "" || len(tcpAddresses) > 0 {
		tcpCheck := check.NewTCP(
			primaryAddress,
			tcpAddresses,
			!di.DisablePersistentConnection,
			tcpSend,
			tcpExpect,
			tcpClose,
			fmt.Sprintf("%s_status", service.Name),
			labels,
			d.acc,
		)
		d.addCheck(tcpCheck.Run, service)
	} else {
		logger.V(1).Printf("No check for service type %#v", service.Name)
	}
}

func (d *Discovery) createHTTPCheck(service Service, di discoveryInfo, primaryIP string, tcpAddresses []string, labels map[string]string) {
	if primaryIP == "" {
		d.createTCPCheck(service, di, primaryIP, tcpAddresses, labels)
		return
	}
	url := fmt.Sprintf("http://%s:%d", primaryIP, di.ServicePort)
	expectedStatusCode := 0
	if service.Name == SquidService {
		// Agent does a normal HTTP request, but squid expect a proxy. It expect
		// squid to reply with a 400 - Bad request.
		expectedStatusCode = 400
	}
	if service.Name == InfluxDBService {
		url += "/ping"
	}
	httpCheck := check.NewHTTP(
		url,
		tcpAddresses,
		expectedStatusCode,
		fmt.Sprintf("%s_status", service.Name),
		labels,
		d.acc,
	)
	d.addCheck(httpCheck.Run, service)
}

func (d *Discovery) addCheck(task task.Runner, service Service) {
	if d.acc == nil || d.taskRegistry == nil {
		return
	}
	key := nameContainer{
		name:        service.Name,
		containerID: service.ContainerID,
	}
	id, err := d.taskRegistry.AddTask(task, fmt.Sprintf("check for %s", service.Name))
	if err != nil {
		logger.V(1).Printf("Unable to add check: %v", err)
	}
	d.activeCheck[key] = id
}
