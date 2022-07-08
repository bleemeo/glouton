<p align="center">
   <img src="logo_glouton.svg" alt="Glouton" height="300"/>
</p>

[![Go Report Card](https://goreportcard.com/badge/github.com/bleemeo/glouton)](https://goreportcard.com/report/github.com/bleemeo/glouton)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](https://github.com/bleemeo/glouton/blob/master/LICENSE)
![Platform](https://img.shields.io/badge/platform-linux%20%7C%20windows%20%7C%20macos-informational)
[![Docker Image Size](https://img.shields.io/docker/image-size/bleemeo/glouton)](https://hub.docker.com/r/bleemeo/glouton/tags)

**Glouton** is a monitoring agent that makes observing your infrasture easy. Glouton retrieves metrics from **node exporter** and multiple **telegraf inputs** and expose them through a **Prometheus endpoint** and a **dashboard**. It also automatically discovers your [services](#automatically-discovered-services) to retrieve relevant metrics. 


<img src="webui/preview/dashboard.png" alt="Dashboard"/>

## Features

- **Automatic discovery and configuration of services** to generate checks and metrics
- Can be used as main **Prometheus endpoint** for another scrapper
- Support **Nagios checks, nrpe and Statsd** metrics ingester
- Available as **Linux deb and rpm packages** for Linux distributions, as a **Docker container and Windows package**
- **Kubernetes native support** creating metrics and checks for pods

## Automatically discovered services

TODO: All logos of auto discovered services
## Install

TODO: deb/rpm installation / docker / windows

If you want to use the Bleemeo Cloud solution see https://docs.bleemeo.com/agent/installation.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).
