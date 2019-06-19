# Bleemeo Monitoring Agent


Bleemeo Agent have been designed to be a central piece of
a monitoring infrastructure. It gather all information and
send it to... something. We provide drivers for storing in
an InfluxDB database directly or a secure connection (MQTT over SSL) to
Bleemeo Cloud platform.


## Install


Install Bleemeo-agent:
```
pip install bleemeo-agent
```

If you want to use the Bleemeo Cloud solution see https://docs.bleemeo.com/agent/install-agent/.

## Test and Develop


If you want to test and or develop on bleemeo-agent, here are the step to run from a git checkout:

* create a virtualenv:
```
mkvirtualenv -p /usr/bin/python3 bleemeo-agent
```

* Install Go and dep if not already installed:
```
sudo apt install golang go-dep
```

* Install dependencies and build the Go libcabi:
```
(export GOPATH=$(pwd)/agentgo && cd agentgo/src/agentgo && dep ensure)
(export GOPATH=$(pwd)/agentgo && go build -o agentgo/libcabi.so -buildmode=c-shared agentgo/cabi)
```

* Run Golang linter:
```
docker run --rm -v $(pwd):/src:ro -w /src -e GOPATH=/src/agentgo golangci/golangci-lint:v1.17.0 golangci-lint run agentgo/src/agentgo/...
```

* Run Go tests:
```
(export GOPATH=$(pwd)/agentgo && cd agentgo/ && go test agentgo/...)
```


* Install `bleemeo-agent` with its dependencies:
```
pip install -U setuptools
pip install -r requirements-dev.txt
```

* Install telegraf and configure it:
```
sudo sh -c "echo deb http://packages.bleemeo.com/telegraf/ $(lsb_release --codename | cut -f2) main > /etc/apt/sources.list.d/bleemeo-telegraf.list"
sudo apt-key adv --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys 9B8BDA4BE10E9F2328D40077E848FD17FC23F27E
sudo apt-get update

sudo apt-get install telegraf
sudo install -m 0644 packaging/common/telegraf.conf /etc/telegraf/telegraf.d/bleemeo.conf
sudo touch /etc/telegraf/telegraf.d/bleemeo-generated.conf
sudo chown $USER /etc/telegraf/telegraf.d/bleemeo-generated.conf
sudo service telegraf restart
```

* Update your credentials in `etc/agent.conf.d/90-local.conf`:
```
vim etc/agent.conf.d/90-local.conf

# Add something like
bleemeo:
   account_id: ...
   registration_key: ...
```

* Run bleemeo-agent from repository root (where README and setup.py are):
```
bleemeo-agent
```

* Bleemeo agent have a local web UI accessible on http://localhost:8015

