# Glouton Monitoring Agent

![Glouton Logo](logo_glouton.svg)

Glouton have been designed to be a central piece of
a monitoring infrastructure. It gather all information and
send it to... something. We provide drivers for storing in
an InfluxDB database directly or a secure connection (MQTT over SSL) to
Bleemeo Cloud platform.

## Install

If you want to use the Bleemeo Cloud solution see https://docs.bleemeo.com/agent/install-agent/.

## Build a release

Our release version will be set by goreleaser from the current date.

- Glouton have a local UI written in ReactJS. The JS files need to be built before
  the Go binary by running:

```
docker run --rm -u $UID -e HOME=/tmp/home \
   -v $(pwd):/src -w /src/webui \
   node:lts \
   sh -c 'rm -fr node_modules && npm install && npm run deploy'
```

- Then build the release binaries and Docker image using Goreleaser:

```
docker run --rm -u $UID:`getent group docker|cut -d: -f 3` -e HOME=/go/pkg -e CGO_ENABLED=0 \
   -v $(pwd):/src -w /src \
   -v /var/run/docker.sock:/var/run/docker.sock \
   --entrypoint '' \
   goreleaser/goreleaser:v0.137 sh -c 'go test ./... && goreleaser --rm-dist --snapshot'
```

Release files are present in dist/ folder and a Docker image is build (glouton:latest).

- To build an all-in-one installer for Windows, run:

```
docker build -t bleemeo/installer_builder packaging/windows/
docker run --rm -e COMMIT_HASH="$(git rev-parse --short HEAD)" -v "$(pwd):/work" bleemeo/installer_builder
```

The final executable will be `dist/windows_installer.exe`.

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

Glouton require Golang 1.13. If your system does not provide it, you may run all Go command using Docker.
For example to run test:

```
GOCMD="docker run --net host --rm -ti -v $(pwd):/srv/workspace -w /srv/workspace -u $UID -e HOME=/tmp/home golang go"

$GOCMD test ./...
```

The following will assume "go" is golang 1.13 or more, if not replace it with $GOCMD or use an alias:
```
alias go=$GOCMD
```

Glouton use golangci-lint as linter. You may run it with:
```
mkdir -p /tmp/golangci-lint-cache; docker run --rm -v $(pwd):/app -u $UID -v /tmp/golangci-lint-cache:/go/pkg -e HOME=/go/pkg -e GOOS=linux -e GOARCH=amd64 -w /app golangci/golangci-lint:v1.27 golangci-lint run && docker run --rm -v $(pwd):/app -u $UID -v /tmp/golangci-lint-cache:/go/pkg -e HOME=/go/pkg -e GOOS=windows -e GOARCH=amd64 -w /app golangci/golangci-lint:v1.27 golangci-lint run
```
(This will check the code for both windows/amd64 and linux/amd64)

Glouton use Go tests, you may run them with:

```
go test ./... || echo "TEST FAILED"
```

If you updated GraphQL schema or JS files, rebuild JS files (see build a release) and run:

```
go generate glouton/...
```

Then run Glouton from source:

```
go run glouton
```

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
   node:12.13.0 \
   sh -c 'npm install && npm start'
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
