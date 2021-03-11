# Glouton Monitoring Agent

![Glouton Logo](logo_glouton.svg)

[![Go Report Card](https://goreportcard.com/badge/github.com/bleemeo/glouton)](https://goreportcard.com/report/github.com/bleemeo/glouton)
![Docker Image Version (latest by date)](https://img.shields.io/docker/v/bleemeo/glouton)
![Docker Image Size (latest by date)](https://img.shields.io/docker/image-size/bleemeo/glouton)
[![Open Source? Yes!](https://badgen.net/badge/Open%20Source%20%3F/Yes%21/blue?icon=github)](https://github.com/bleemeo/glouton/)


Glouton have been designed to be a central piece of
a monitoring infrastructure. It gather all information and
send it to... something. We provide drivers for storing in
an InfluxDB database directly or a secure connection (MQTT over SSL) to
Bleemeo Cloud platform.

## Install

If you want to use the Bleemeo Cloud solution see https://docs.bleemeo.com/agent/installation.

## Build a release

Our release version will be set by goreleaser from the current date.

The release build will
* Build the local UI written in ReactJS using npm and webpack.
* Compile the Go binary for supported systems
* Build an Windows installer using NSIS

The build process use Docker and is run by the build script:
```
# Optional, to speed-up subsequent build
mkdir -p .build-cache
./build.sh
```

Release files are present in dist/ folder and a Docker image is build (glouton:latest).

## Run Glouton

On Linux amd64, after building the release you may run it with:

```
./dist/glouton_linux_amd64/glouton
```

Before running the binary, you may want to configure it with:

- (optional) Configure your credentials for Bleemeo Cloud platform:

```
export GLOUTON_BLEEMEO_ACCOUNT_ID=YOUR_ACCOUNT_ID
export GLOUTON_BLEEMEO_REGISTRATION_KEY=YOUR_REGISTRATION_KEY
```

- (optional) If the Bleemeo Cloud platform is running locally:

```
export GLOUTON_BLEEMEO_API_BASE=http://localhost:8000
export GLOUTON_BLEEMEO_MQTT_HOST=localhost
export GLOUTON_BLEEMEO_MQTT_PORT=1883
export GLOUTON_BLEEMEO_MQTT_SSL=False
```

## Run on Docker (with JMX)

Glouton could be run using Docker, optionally with JMX metrics using jmxtrans (a JMX proxy which
query JVM over JMX and send metrics over the graphite protocol to Glouton).

To use jmxtrans, two containers will be run, one with Glouton and one with jmxtrans and a shared volume between
them will allow Glouton to write jmxtrans configuration file.

Like when running Glouton without docker, you may optionally configure your Bleemeo credentials:

```
export GLOUTON_BLEEMEO_ACCOUNT_ID=YOUR_ACCOUNT_ID
export GLOUTON_BLEEMEO_REGISTRATION_KEY=YOUR_REGISTRATION_KEY
```

Then using docker-compose, start Glouton and jmxtrans:

```
docker-compose up -d
```

## Test and Develop

To build binary you may use build.sh script. For example to just
compile Go binary (skip building JS, Docker image and Windows installer):
```
mkdir -p .build-cache
./build.sh go
```

Then run Glouton:
```
./glouton
```

Glouton use golangci-lint as linter. You may run it with:
```
mkdir -p .build-cache  # enable cache and speed-up build/lint run
./lint.sh
```

If you updated GraphQL schema or JS files, rebuild JS files by running build.sh:

```
./build.sh
```

Note: on Windows, you should consider setting an environment variable to disable CGO when building/testing, lest you get funny messages like 'exec: "gcc": executable file not found in %PATH%'.
For Powershell, you may set it with `$env:CGO_ENABLED = 0`.

### Developping the local UI JavaScript

When working on the JavaScript rebuilding the Javascript bundle and running go generate could be slow
and will use minified JavaScript file which are harded to debug.

To avoid this, you may want to run and use webpack-dev-server which will serve non-minified
JavaScript file and rebuild the JavaScript bundle on the file. When doing a change in
any JavaScript files, you will only need to refresh the page on your browser.

To run with this configuration, start webpack-dev-server:
```
docker run --net host --rm -ti -u $UID -e HOME=/tmp/home \
   -v $(pwd):/src -w /src/webui \
   node:lts \
   sh -c 'npm install && npm start'
```

Glouton use eslint as linter. You may run it with:
```
cd webui; npm run lint
```

Glouton use prettier too. You may run it with:
```
cd webui; npm run pretify
```

Then tell Glouton to use JavaScript file from webpack-dev-server:
```
export GLOUTON_WEB_STATIC_CDN_URL=http://localhost:3015
go run glouton
```

### Updating dependencies

To update dependencies, you can run:

```
go get -u ./...
```

This should only update to latest minor or patch version. For major version, you need to specify the dependency explicitly,
possible with the version or commit hash. For example:

```
go get github.com/influxdata/telegraf@1.12.1
```

Finally, it may worse removing all "// indirect" and running go mod tidy, to ensure
only needed indirect dependencies are present.

Running go mod tidy & test before commiting the updated go.mod is recommended:
```
go mod tidy
go test ./...
```

### Note on VS code

Glouton use Go module. VS code support for Go module require usage of gppls.
Enable "Use Language Server" in VS code option for Go.

To install or update gopls, use:

```
(cd /tmp; GO111MODULE=on go get golang.org/x/tools/gopls@latest)
```
