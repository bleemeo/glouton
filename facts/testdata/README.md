# Generate/update testdate

## Kubernetes

Start minikube:
```
MINIKUBE_VERSION=v1.18.0
minikube start --kubernetes-version=${MINIKUBE_VERSION}
```

Apply k8s.yaml:
```
minikube minikube kubectl apply -f ./facts/testdata/k8s.yaml
```

Enable Docker environment for minikube:
```
eval $(minikube docker-env)
```

Stop the delete-me-once containers:
```
docker stop -t0 `docker ps --format '{{ .ID }}'  --filter name=delete-me-once`
```

Wait until all pods are ready and crash-loop is in CrashLoopBackOff.

Then:
```
docker inspect `docker ps -a --format '{{ .ID }}'  --filter name=_default_` > facts/testdata/minikube-${MINIKUBE_VERSION}/docker.json
minikube kubectl -- get pods -o yaml > facts/testdata/minikube-${MINIKUBE_VERSION}/pods.yaml

```