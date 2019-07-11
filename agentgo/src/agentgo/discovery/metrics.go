package discovery

import (
	"agentgo/inputs/apache"
	"agentgo/inputs/memcached"
	"agentgo/inputs/modify"
	"agentgo/inputs/redis"
	"fmt"
	"log"
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
		log.Printf("DBG2: Remove input for service %v on container %s", key.name, key.containerID)
		delete(d.activeInput, key)
		d.coll.RemoveInput(inputID)
	}
}

func (d *Discovery) createInput(service Service) error {
	if !service.Active {
		return nil
	}

	log.Printf("DBG2: Add input for service %v on container %s", service.Name, service.ContainerID)
	di := servicesDiscoveryInfo[service.Name]

	var input telegraf.Input
	var err error
	switch service.Name {
	case "apache":
		if address := addressForPort(service, di); address != "" {
			statusURL := fmt.Sprintf("http://%s:%d/server-status?auto", address, di.ServicePort)
			if di.ServicePort == 80 {
				statusURL = fmt.Sprintf("http://%s/server-status?auto", address)
			}
			input, err = apache.New(statusURL)
		}
	case "memcached":
		if address := addressForPort(service, di); address != "" {
			input, err = memcached.New(fmt.Sprintf("%s:%d", address, di.ServicePort))
		}
	case "redis":
		if address := addressForPort(service, di); address != "" {
			input, err = redis.New(fmt.Sprintf("tcp://%s:%d", address, di.ServicePort))
		}
	default:
		log.Printf("DBG: service type %s don't support metrics", service.Name)
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
	inputID := d.coll.AddInput(input, service.Name)
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
