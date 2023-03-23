# Create testdata

Note: Some information in test file are manually extracted. This is typically the case for
container IP addresses.

Start it using ONE of the following method and define the corresponding _testdir variable:
```
minikube start --container-runtime=containerd
_testdir=facts/container-runtime/containerd/testdata/minikube-1.20.0
```


Then:
```
minikube ssh -- sudo systemctl stop kubelet
minikube ssh -- sudo ctr -n k8s.io task rm -f $(minikube ssh -- sudo ctr -n k8s.io task ls -q | tr -d '\r')
minikube ssh -- sudo ctr -n k8s.io container rm $(minikube ssh -- sudo ctr -n k8s.io container ls -q | tr -d '\r')
minikube ssh -- sudo ctr -n k8s.io image rm $(minikube ssh -- sudo ctr -n k8s.io image ls  -q | tr -d '\r')
minikube ssh -- sudo ctr namespace rm k8s.io

minikube ssh -- sudo ctr image pull docker.io/library/rabbitmq:latest
minikube ssh -- sudo ctr run -d docker.io/library/rabbitmq:latest rabbitmqInternal
minikube ssh -- sudo ctr run -d --label glouton.check.ignore.port.5672=false --label glouton.check.ignore.port.4369=TrUe docker.io/library/rabbitmq:latest rabbitLabels
minikube ssh -- sudo ctr run -d docker.io/library/rabbitmq:latest notRunning true
minikube ssh -- sudo ctr run -d --label glouton.enable=off docker.io/library/rabbitmq:latest gloutonIgnore


mkdir ${_testdir}

CGO_ENABLED=0 GOOS=linux go build -o /tmp/containerd-testdata facts/container-runtime/containerd/testdata/containerd-testdata.go && \
base64 < /tmp/containerd-testdata | minikube ssh --native-ssh=false 'base64 -d > containerd-testdata' && \
minikube ssh -- chmod +x containerd-testdata && \
minikube ssh -- sudo ./containerd-testdata -create-redis; \
minikube ssh -- sudo ./containerd-testdata > ${_testdir}/containerd.json
```
