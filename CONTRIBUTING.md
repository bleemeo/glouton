## Build cache (optional)

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

If you are using Docker, you can quickly build a Docker image that use the Glouton you just build:

```
# Remember to run ./build.sh go before
./build.sh docker-fast
```

This result in an image tag "glouton:latest". You might need to re-tag is to use it with your existing
docker-compose:

```
docker tag glouton:latest bleemeo/bleemeo-agent:proposed
```

### Developing on MacOS

If you can't run `./glouton` because it is a Linux binary, you can simply run `go run .`.

When loading the UI, if Glouton is not able to discover your Docker containers, try changing the Docker socket with :

```sh
export GLOUTON_CONTAINER_RUNTIME_DOCKER_ADDRESSES="unix://${HOME}/.docker/run/docker.sock"
```

### One-time rebuild UI

If you need to build the UI once, follow those instruction. If you develop on the local UI,
you probably want to follow "Developing the local UI" a bit later which will allow to iterate faster.

To run a single build of UI do:

```
./build.sh only-js
./build.sh go

# If using Docker
./build.sh docker-fast
```

### Linters

Glouton uses golangci-lint as linter. You may run it with:

```sh
./lint.sh
```

### Full build

If you updated JS files, rebuild them by running build.sh:

```sh
./build.sh
```

**Note:** on Windows, if you are using the Go compiler without the `build.sh` script and
get `"gcc": executable file not found"` error, try setting the environment variable `CGO_ENABLED` to 0.

#### Developing the local UI

When working on the JavaScript rebuilding the Javascript bundle could be slow
and will use minified JavaScript file which are harder to debug.

To avoid this, you may want to run and use webpack-dev-server which will serve non-minified
JavaScript file and rebuild the JavaScript bundle on the file. When doing a change in
any JavaScript files, you will only need to refresh the page on your browser.

To run with this configuration, start webpack-dev-server:

```sh
docker run --rm -ti -u $UID -e HOME=/tmp/home \
   -v $(pwd):/src -w /src/webui \
   -p 127.0.0.1:3015:3015 \
   node:22 \
   sh -c 'npm install && npm start'
```

Then tell Glouton to use JavaScript file from webpack-dev-server:

```sh
export GLOUTON_WEB_STATIC_CDN_URL=http://localhost:3015
./glouton
```

Glouton uses eslint as linter. You may run it with:

```sh
(cd webui; npm run lint)
```

Glouton uses prettier too. You may run it with:

```sh
(cd webui; npm run prettify)
```

### Developing the Windows installer

`packaging/windows` contains two folders:

- `installer` contains a [WiX](https://wixtoolset.org/) project
  to build the MSI installer.
- `chocolatey` contains a [Chocolatey](https://docs.chocolatey.org/en-us/) project.

If you are working on Windows, the folder `packaging/windows/installer` contains a Visual Studio project.
Visual Studio has a WiX extension that brings auto-completion and WiX templates for easier development.
For the extension you will need to install [.NET 3.5](https://www.microsoft.com/fr-fr/download/details.aspx?id=21)
and [WiX toolset](https://wixtoolset.org/releases/).

If you want to edit the UI to add or modify a dialog, you can use [WiXEdit](https://github.com/WixEdit/WixEdit)
on Windows.

On Linux, the `build.sh` script will build the MSI and the chocolatey package.

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

The release files are created in the `dist/` folder and a Docker image named `glouton:latest` is built.

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
