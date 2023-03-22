# Generate/update testdata

## Kubernetes

We use minikube to create the cluster:

Minikube was started using ONE of the following method and define the corresponding _testdir variable:
```
minikube start --kubernetes-version='1.20.0' --container-runtime='docker'
_testdir="facts/container-runtime/kubernetes/testdata/with-docker-v1.20.0"

minikube start --kubernetes-version='1.18.0' --container-runtime='docker' --driver='virtualbox' --cni=calico
_testdir="facts/container-runtime/kubernetes/testdata/with-docker-in-vbox-v1.18.0"

minikube start --kubernetes-version='1.19.0' --container-runtime='containerd' --driver='virtualbox' --cni=calico
_testdir="facts/container-runtime/kubernetes/testdata/containerd-in-vbox-v1.19.0"

minikube start --container-runtime='containerd' --driver qemu2
_testdir="facts/container-runtime/kubernetes/testdata/containerd-in-qemu-arm-minikube-v1.29.0"
```

Then (carefull, some part are Docker specific, some containerd specific):
```
minikube kubectl -- apply -f ./facts/container-runtime/kubernetes/testdata/k8s.yml

# When docker
eval $(minikube docker-env)
docker run -d --label test=42 --name docker_default_without_k8s redis:latest sleep 99d

# When containerd
minikube ssh -- sudo ctr image pull docker.io/library/redis:latest
minikube ssh -- sudo ctr run -d --label test=42 docker.io/library/redis:latest docker_default_without_k8s sleep 99d

sleep 90 # need to wait for container to start

# Stop the delete-me-once containers.
minikube ssh -- sudo pkill -9 -f "'sleep 9999d'"

sleep 10 # need to wait for container to restart

mkdir ${_testdir}
minikube kubectl -- get pods -o yaml > ${_testdir}/pods.yaml
minikube kubectl -- get node -o yaml > ${_testdir}/node.yaml
minikube kubectl -- version -o yaml > ${_testdir}/version.yaml

# When Docker
docker version --format '{{ .Server|json}}' > ${_testdir}/docker-version.json
docker inspect `docker ps -a --format '{{ .ID }}' --filter name=_default_` > ${_testdir}/docker-containers.json

# When containerd
CGO_ENABLED=0 GOOS=linux go build -o /tmp/containerd-testdata facts/container-runtime/containerd/testdata/containerd-testdata.go && \
base64 < /tmp/containerd-testdata | minikube ssh --native-ssh=false 'base64 -d > containerd-testdata' && \
minikube ssh -- chmod +x containerd-testdata && \
minikube ssh -- sudo ./containerd-testdata > ${_testdir}/containerd.json

```
