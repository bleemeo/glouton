## Build cache

Enable the cache to speed-up build and lint.
```sh
docker volume create glouton-buildcache
```

## Test and Develop

To build binary you may use the `build.sh` script. For example to just
compile a Go binary (skip building JS, Docker image and Windows installer):
```sh
./build.sh go
```

Then run Glouton:
```sh
./glouton
```

Glouton uses golangci-lint as linter. You may run it with:
```sh
./lint.sh
```

If you updated GraphQL schema or JS files, rebuild JS files by running build.sh:

```sh
./build.sh
```

**Note:** on Windows, if you are using the Go compiler without the `build.sh` script and 
get `"gcc": executable file not found"` error, try setting the environment variable `CGO_ENABLED` to 0.

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
./glouton
```

## Build a release

Our release version will be set from the current date.

The release build will
* Build the local UI written in ReactJS using npm and webpack.
* Compile the Go binary for supported systems
* Build Docker image using Docker buildx
* Build an Windows installer using NSIS

A builder needs to be created to build multi-arch images if it doesn't exist.
```sh
docker buildx create --name glouton-builder
```

### Test release

To do a test release, run:
```sh
export GLOUTON_VERSION="$(date -u +%y.%m.%d.%H%M%S)"
export GLOUTON_BUILDX_OPTION="--builder glouton-builder -t glouton:latest --load"

./build.sh
unset GLOUTON_VERSION GLOUTON_BUILDX_OPTION
```

Release files are present in dist/ folder and a Docker image is build (glouton:latest) and loaded to
your Docker images.

### Production release

For production releases, you will want to build the Docker image for multiple architecture, which requires to
push the image into a registry. Set image tags ("-t" options) to the wanted destination and ensure you
are authorized to push to the destination registry:
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
