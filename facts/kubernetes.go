package facts

import (
	"context"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// KubernetesProvider provide information about Kubernetes & PODs
type KubernetesProvider struct {
	NodeName   string
	KubeConfig string

	l          sync.Mutex
	client     *kubernetes.Clientset
	lastUpdate time.Time
	pods       []corev1.Pod
}

// PODs returns the list of PODs. If possible only list pods running on local node
func (k *KubernetesProvider) PODs(ctx context.Context, maxAge time.Duration) ([]corev1.Pod, error) {
	k.l.Lock()
	defer k.l.Unlock()

	if k.client == nil {
		err := k.init()
		if err != nil {
			return nil, err
		}
	}

	if time.Since(k.lastUpdate) > maxAge {
		err := k.updatePODs()
		if err != nil {
			return nil, err
		}
	}

	return k.pods, nil
}

func (k *KubernetesProvider) init() error {
	var (
		config *rest.Config
		err    error
	)

	if k.KubeConfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", k.KubeConfig)
	} else {
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		return err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	k.client = clientset

	return nil
}

func (k *KubernetesProvider) updatePODs() error {
	opts := metav1.ListOptions{}

	if k.NodeName != "" {
		opts.FieldSelector = "spec.nodeName=" + k.NodeName
	}

	list, err := k.client.CoreV1().Pods("").List(metav1.ListOptions{})
	if err != nil {
		return err
	}

	k.pods = list.Items

	return nil
}
