## Build a release

Our release version will be set from the current date.

The release build will
* Build the local UI written in ReactJS using npm and webpack.
* Compile the Go binary for supported systems
* Build Docker image using Docker buildx
* Build an Windows installer using NSIS

The build process use Docker and is run by the build script:

* The first time, create resource:
```sh
docker volume create glouton-buildcache  # (optional) enable cache and speed-up build/lint run

docker buildx create --name glouton-builder  # Needed to building multi-arch images
```

* Then for each release:
```sh
export GLOUTON_VERSION="$(date -u +%y.%m.%d.%H%M%S)"
export GLOUTON_BUILDX_OPTION="--builder glouton-builder -t glouton:latest --load"

./build.sh
unset GLOUTON_VERSION GLOUTON_BUILDX_OPTION
```

Release files are present in dist/ folder and a Docker image is build (glouton:latest) and loaded in
your Docker images.


For real release, you will want to build the Docker image for multiple architecture, which require to
push image into a registry. Set image tags ("-t" options) to the wanted destination and ensure you
are authorized to push in destination registry:
```sh
export GLOUTON_VERSION="$(date -u +%y.%m.%d.%H%M%S)"
export GLOUTON_BUILDX_OPTION="--builder glouton-builder --platform linux/amd64,linux/arm64/v8,linux/arm/v7 -t glouton:latest -t glouton:${GLOUTON_VERSION} --push"

./build.sh
unset GLOUTON_VERSION GLOUTON_BUILDX_OPTION
```

## Run Glouton

On Linux amd64, after building the release you may run it with:

```sh
./dist/glouton_linux_amd64/glouton
```

Before running the binary, you may want to configure it with:

- (optional) Configure your credentials for Bleemeo Cloud platform:

```sh
export GLOUTON_BLEEMEO_ACCOUNT_ID=YOUR_ACCOUNT_ID
export GLOUTON_BLEEMEO_REGISTRATION_KEY=YOUR_REGISTRATION_KEY
```

- (optional) If the Bleemeo Cloud platform is running locally:

```sh
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

```sh
export GLOUTON_BLEEMEO_ACCOUNT_ID=YOUR_ACCOUNT_ID
export GLOUTON_BLEEMEO_REGISTRATION_KEY=YOUR_REGISTRATION_KEY
```

Then using docker-compose, start Glouton and jmxtrans:

```sh
docker-compose up -d
```

## Test and Develop

To build binary you may use build.sh script. For example to just
compile Go binary (skip building JS, Docker image and Windows installer):
```sh
docker volume create glouton-buildcache  # (optional) enable cache and speed-up build/lint run

./build.sh go
```

Then run Glouton:
```sh
./glouton
```

Glouton use golangci-lint as linter. You may run it with:
```sh
docker volume create glouton-buildcache  # (optional) enable cache and speed-up build/lint run

./lint.sh
```

If you updated GraphQL schema or JS files, rebuild JS files by running build.sh:

```sh
./build.sh
```

Note: on Windows, you should consider setting an environment variable to disable CGO when building/testing, lest you get funny messages like 'exec: "gcc": executable file not found in %PATH%'.
For Powershell, you may set it with `$env:CGO_ENABLED = 0`.

### Developping the local UI

When working on the JavaScript rebuilding the Javascript bundle could be slow
and will use minified JavaScript file which are harder to debug.

To avoid this, you may want to run and use webpack-dev-server which will serve non-minified
JavaScript file and rebuild the JavaScript bundle on the file. When doing a change in
any JavaScript files, you will only need to refresh the page on your browser.

To run with this configuration, start webpack-dev-server:
```sh
docker run --net host --rm -ti -u $UID -e HOME=/tmp/home \
   -v $(pwd):/src -w /src/webui \
   node:16 \
   sh -c 'npm install && npm start'
```

Glouton use eslint as linter. You may run it with:
```sh
(cd webui; npm run lint)
```

Glouton use prettier too. You may run it with:
```sh
(cd webui; npm run pretify)
```

Then tell Glouton to use JavaScript file from webpack-dev-server:
```sh
export GLOUTON_WEB_STATIC_CDN_URL=http://localhost:3015
go run glouton
```

### Updating dependencies

To update dependencies, you can run:

```sh
go get -u ./...
```

This should only update to latest minor or patch version. For major version, you need to specify the dependency explicitly,
possible with the version or commit hash. For example:

```sh
go get github.com/influxdata/telegraf@1.12.1
```

Running go mod tidy & test before commiting the updated go.mod is recommended:
```sh
go mod tidy
go test ./...
```
