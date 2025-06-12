package scheduler

import (
    "os"
    "path/filepath"
    "k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/rest"
)

func loadKubeConfig() (*rest.Config, error) {
	// Try in-cluster config
	if config, err := rest.InClusterConfig(); err == nil {
		return config, nil
	}

	// Fall back to local auth
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		kubeconfig = "/etc/kubernetes/admin.conf"
	}

	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

