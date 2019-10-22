# Glouton Monitoring Agent

Glouton have been designed to be a central piece of
a monitoring infrastructure. It gather all information and
send it to... something. We provide drivers for storing in
an InfluxDB database directly or a secure connection (MQTT over SSL) to
Bleemeo Cloud platform.

## Install

If you want to use the Bleemeo Cloud solution see https://docs.bleemeo.com/agent/install-agent/.

## Test and Develop

If you want to install from source and or develop on Glouton, here are the step to run from a git checkout:

Those step of made for Ubuntu 19.04 or more, but appart the installation of Golang 1.12 the same step should apply on any systems.

- Install Golang 1.12, activate it and install golangci-lint (this is needed only once):

```
sudo apt install golang-1.12 git
export PATH=/usr/lib/go-1.12/bin:$PATH

(cd /tmp; GO111MODULE=on go get github.com/golangci/golangci-lint/cmd/golangci-lint@v1.17.1)
(cd /tmp; GO111MODULE=on go get github.com/goreleaser/goreleaser@v0.119)
```

- If not yet done, activate Golang 1.12:

```
export PATH=/usr/lib/go-1.12/bin:$PATH
```

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

- Run development version of the agent:

```
export GLOUTON_LOGGING_LEVEL=0  # 0: is the default. Increase to get more logs
go run glouton
```

- To include the Agent UI (http://localhost:8015), run

```
(cd ./webui && npm i && npm run deploy)
```

- Prepare a release (or just run some test and linter during development):
   - Optionally, try to update dependencies: `go get -u` then `go mod tidy`
   - For Telegraf, the update must specify the version: e.g. `go get github.com/influxdata/telegraf@1.12.1`
   - Run Go generate to update generated files (static JS files & GraphQL schema): `go generate glouton/...`
   - Run GoLang linter: `~/go/bin/golangci-lint run ./...`
   - Run Go tests: `go test glouton/...`

- Build the release binaries and Docker image:

```
~/go/bin/goreleaser --rm-dist --snapshot
```

### Note on VS code

Glouton use Go module. VS code support for Go module require usage of gppls.
Enable "Use Language Server" in VS code option for Go.

To install or update gopls, use:

```
(cd /tmp; GO111MODULE=on go get golang.org/x/tools/gopls@latest)
```
