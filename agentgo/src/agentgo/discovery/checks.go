package discovery

import (
	"agentgo/check"
	"context"
	"fmt"
	"log"
)

func (d *Discovery) configureChecks(oldServices, services map[nameContainer]Service) (err error) {
	for key := range oldServices {
		if _, ok := services[key]; !ok {
			d.removeCheck(key)
		}
	}

	for key, service := range services {
		oldService, ok := oldServices[key]
		if !ok || serviceNeedUpdate(oldService, service) {
			d.removeCheck(key)
			err = d.createCheck(service)
			if err != nil {
				return
			}
		}
	}
	return nil
}

func (d *Discovery) removeCheck(key nameContainer) {
	if cancel, ok := d.activeCheckCancelFunc[key]; ok {
		log.Printf("DBG2: Remove check for service %v on container %s", key.name, key.containerID)
		cancel()
		delete(d.activeCheckCancelFunc, key)
	}
}

func (d *Discovery) createCheck(service Service) error {
	if !service.Active {
		return nil
	}

	log.Printf("DBG2: Add check for service %v on container %s", service.Name, service.ContainerID)
	di := servicesDiscoveryInfo[service.Name]

	var primaryAddress string
	if di.ServiceProtocol == tcpPortocol {
		primaryAddress = fmt.Sprintf("%s:%d", addressForPort(service, di), di.ServicePort)
	}

	tcpAddresses := make([]string, 0)
	for _, a := range service.ListenAddresses {
		if a.Network() != tcpPortocol {
			continue
		}
		if a.String() == primaryAddress {
			continue
		}
		tcpAddresses = append(tcpAddresses, a.String())
	}
	if primaryAddress != "" || len(tcpAddresses) > 0 {
		tcpCheck := check.NewTCP(
			primaryAddress,
			tcpAddresses,
			fmt.Sprintf("%s_status", service.Name),
			service.ContainerName,
		)
		d.addCheck(tcpCheck.Run, service)
	}

	return nil
}

func (d *Discovery) addCheck(runFunc func(context.Context), service Service) {
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
