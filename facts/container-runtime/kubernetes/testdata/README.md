# Generate/update testdata

## Kubernetes + Docker

We use minikube:

Start it using ONE of the following method and define the corresponding _testdir variable:
```
minikube start --kubernetes-version='1.20.0' --container-runtime='docker'
_testdir="facts/container-runtime/kubernetes/testdata/with-docker-v1.20.0"

minikube start --kubernetes-version='1.18.0' --container-runtime='docker' --driver='virtualbox' --cni=calico
_testdir="facts/container-runtime/kubernetes/testdata/with-docker-in-vbox-v1.18.0"
```

Then:
```
eval $(minikube docker-env)

minikube kubectl -- apply -f ./facts/container-runtime/kubernetes/testdata/k8s.yml
docker run -d --label test=42 --name docker_default_without_k8s redis:latest sleep 99d

sleep 90 # need to wait for container to start

# Stop the delete-me-once containers
docker stop -t0 `docker ps --format '{{ .ID }}'  --filter name=delete-me-once`

sleep 10 # need to wait for container to restart

mkdir ${_testdir}
docker version --format '{{ .Server|json}}' > ${_testdir}/docker-version.json
docker inspect `docker ps -a --format '{{ .ID }}' --filter name=_default_` > ${_testdir}/docker-containers.json
minikube kubectl -- get pods -o yaml > ${_testdir}/pods.yaml
minikube kubectl -- get node -o yaml > ${_testdir}/node.yaml
minikube kubectl -- version -o yaml > ${_testdir}/version.yaml

```
