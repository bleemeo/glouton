<p align="center">
   <img src="logo_glouton.svg" alt="Glouton" height="300"/>
</p>

[![Go Report Card](https://goreportcard.com/badge/github.com/bleemeo/glouton)](https://goreportcard.com/report/github.com/bleemeo/glouton)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](https://github.com/bleemeo/glouton/blob/master/LICENSE)
![Platform](https://img.shields.io/badge/platform-linux%20%7C%20windows%20%7C%20macos-informational)
[![Docker Image Size](https://img.shields.io/docker/image-size/bleemeo/glouton)](https://hub.docker.com/r/bleemeo/glouton/tags)

**Glouton** is a monitoring agent that makes observing your infrasture easy. Glouton retrieves metrics from **node exporter** and multiple **telegraf inputs** and expose them through a **Prometheus endpoint** and a **dashboard**. It also automatically discovers your [services](#automatically-discovered-services) to retrieve relevant metrics.

TODO: Introduce Bleemeo?

<img src="webui/preview/dashboard.png" alt="Dashboard"/>

## Features

- [**Automatic discovery and configuration of services**](#automatically-discovered-services) to generate checks and metrics
- Can be used as the main **[Prometheus endpoint](#metrics-endpoint)** for another scrapper
- Support **[Nagios checks](https://docs.bleemeo.com/metrics-sources/custom/#reference-for-custom-check), [NRPE](https://docs.bleemeo.com/agent/configuration/#nrpeaddress) and [StatsD](https://docs.bleemeo.com/metrics-sources/statsd)** metrics ingester
- **Kubernetes native support** creating metrics and checks for pods
- Monitor your printers and network devices with [**SNMP**](https://docs.bleemeo.com/agent/snmp)
- **[JMX support](https://docs.bleemeo.com/metrics-sources/java)** to monitor your Java applications.

### Automatically discovered services

Glouton automatically detects and generates metrics for your services. Supported services include **Apache**, **Cassandra**, **Redis**, **Elasticsearch**, **Nginx**, **PostgreSQL**, and many others. The full list of services and generated metrics can be found [here](https://docs.bleemeo.com/metrics-sources/services-metrics).

### Metrics endpoint

A metrics endpoint is available on port http://localhost:8015/metrics by default (this can be connfigured [here](https://docs.bleemeo.com/agent/configuration#weblisteneraddress)). This is endpoint can be scrapped by Prometheus for example to retrieve the metrics and show them on graphs in Grafana.

## Docker compose example

A docker compose file is available to quickly setup a full monitoring stack. It includes Grafana, Glouton and a Prometheus configured to scrap Glouton's metrics endpoint.

```
TODO: Setup compose file.
docker-compose up -d
```

## Install

TODO: deb/rpm installation / docker / windows

If you want to use the Bleemeo Cloud solution, see https://docs.bleemeo.com/agent/installation.

## Configuration

The full configuration file with all available options is available [here](https://docs.bleemeo.com/agent/configuration).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).
