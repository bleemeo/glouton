package discovery

import (
	"agentgo/check"
	"context"
	"fmt"
	"log"
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
	if cancel, ok := d.activeCheckCancelFunc[key]; ok {
		log.Printf("DBG2: Remove check for service %v on container %s", key.name, key.containerID)
		cancel()
		delete(d.activeCheckCancelFunc, key)
	}
}

func (d *Discovery) createCheck(service Service) {
	if !service.Active {
		return
	}

	log.Printf("DBG2: Add check for service %v on container %s", service.Name, service.ContainerID)

	di := servicesDiscoveryInfo[service.Name]
	primaryIP := addressForPort(service, di)
	tcpAddresses := make([]string, 0)
	for _, a := range service.ListenAddresses {
		if a.Network() != tcpPortocol {
			continue
		}
		tcpAddresses = append(tcpAddresses, a.String())
	}

	switch service.Name {
	case MemcachedService, RabbitMQService, RedisService, ZookeeperService:
		d.createTCPCheck(service, di, primaryIP, tcpAddresses)
	case ApacheService, InfluxDBService, NginxService, SquidService:
		d.createHTTPCheck(service, di, primaryIP, tcpAddresses)
	default:
		d.createTCPCheck(service, di, primaryIP, tcpAddresses)
	}
}

func (d *Discovery) createTCPCheck(service Service, di discoveryInfo, primaryIP string, tcpAddresses []string) {

	var primaryAddress string
	if di.ServiceProtocol == tcpPortocol && primaryIP != "" {
		primaryAddress = fmt.Sprintf("%s:%d", primaryIP, di.ServicePort)
	}

	var tcpSend, tcpExpect []byte
	switch service.Name {
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
			tcpSend,
			tcpExpect,
			fmt.Sprintf("%s_status", service.Name),
			service.ContainerName,
			d.acc,
		)
		d.addCheck(tcpCheck.Run, service)
	} else {
		log.Printf("DBG: No check for service type %#v", service.Name)
	}
}

func (d *Discovery) createHTTPCheck(service Service, di discoveryInfo, primaryIP string, tcpAddresses []string) {
	if primaryIP == "" {
		d.createTCPCheck(service, di, primaryIP, tcpAddresses)
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
		service.ContainerName,
		d.acc,
	)
	d.addCheck(httpCheck.Run, service)
}

func (d *Discovery) addCheck(runFunc func(context.Context), service Service) {
	if d.acc == nil {
		return
	}
	key := nameContainer{
		name:        service.Name,
		containerID: service.ContainerID,
	}
	ctx, cancel := context.WithCancel(context.Background())
	waitC := make(chan interface{})
	cancelWait := func() {
		cancel()
		<-waitC
	}
	go func() {
		defer close(waitC)
		runFunc(ctx)
	}()
	d.activeCheckCancelFunc[key] = cancelWait
}
