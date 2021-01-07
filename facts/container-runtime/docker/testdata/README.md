# Generate/update testdata

Not everything is updatable. cgroup data & top output are manually done.

## Docker

For docker, we use minikube also but stop kubelet to clean Kubernetes container:
```
minikube start
minikube ssh -- sudo systemctl stop kubelet
minikube ssh -- sudo systemctl restart docker
minikube ssh -- docker system prune -f

eval $(minikube docker-env)
(cd facts/container-runtime/docker/testdata && docker-compose up -d)

_testdir="facts/container-runtime/docker/testdata/docker-`docker info --format '{{ .ServerVersion}}'`"
mkdir ${_testdir}
docker version --format '{{ .Server|json}}' > ${_testdir}/docker-version.json
docker inspect `docker ps -a --format '{{ .ID }}'` > ${_testdir}/docker-containers.json
```
